// session_service.go — the generic runner-owned background-session service (Cutover A,
// A-task-2): the mechanism that spawns, tracks, supervises and finalizes DETACHED capture
// sessions for instrument verbs. A capture session is a recorder process that must outlive
// the `charly check run` INVOCATION that started it (the instrument lifecycle dispatches
// start/stop through separate verb calls); the service owns the detach TRANSPORT (never the
// provider), the handle state, and the orphan sweep.
//
// Transport (runner-chosen, provider-invisible):
//   - systemd-run --user --collect --unit charly-capture-<venue-scoped-id> <recorder argv>
//     when a user systemd session is available (RDD-2: verified in this environment). With
//     --collect the UNIT NAME is the handle's liveness key — no pidfile. `systemctl --user
//     stop <unit>` SIGTERMs the transient service, which the recorder's own SIGTERM trap
//     answers with a deterministic end-of-stream marker; rc=5 (unit already gone) is
//     tolerated and every stop is bounded by a timeout.
//   - setsid + pidfile fallback (the spec/proc detached-process pattern): a Setpgid leader
//     with the pid persisted to the handle's state dir, SIGTERM to the process group on
//     stop, SIGKILL escalation after proc.ProcessShutdownGrace.
//
// Handle state lives under .check/<bed>/<calver>/capture/<session-id>/ (the state dir):
// handle.json is written by THIS service at spawn and rewritten at stop — the disk state is
// the single source of truth, so a session survives invocation boundaries and the bedSetup
// orphan sweep can recover a crashed run's sessions. Providers never see the transport:
// they receive only the session id + state dir in their verb input and write their own
// evidence row (row.json, #EvidenceRow shape) into the state dir at stop.

package check

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/proc"
)

// sessionUnitPrefix names the transient units the service registers; the full unit is
// sessionUnitPrefix + deploykit.SanitizeUnitName(venue-scoped id) (dots and slashes
// become dashes, so any authored id survives the systemd unit-naming rule).
const sessionUnitPrefix = "charly-capture-"

// sessionTransport names the detach transport a handle used. The provider NEVER learns it:
// it is runner-internal state, recorded only so the same runner can stop what it started.
type sessionTransport string

const (
	sessionTransportSystemd sessionTransport = "systemd"
	sessionTransportSetsid  sessionTransport = "setsid"
)

// sessionStopTimeout bounds ONE unit stop (systemctl stop can hang on a wedged service);
// the orphan sweep and the teardown finalize bound every stop with it (RDD-2).
const sessionStopTimeout = 15 * time.Second

// sessionSpawnOpts is a RECORDER-independent spawn request — the caller asks ONLY for the
// recorder command + session identity; every transport decision stays inside the service.
type sessionSpawnOpts struct {
	SessionID string            // venue-scoped identity: <bed>.<member>.<id>
	Command   []string          // recorder argv (never empty)
	Dir       string            // recorder working directory (artifact target dir)
	Env       map[string]string // recorder env overrides (applied over the caller env)
	LogDir    string            // the run's .check/<bed>/<calver> — the state dir derives from it
}

// sessionHandle is the persisted session state (state dir handle.json). Disk state is the
// source of truth: the record written at spawn is what the orphan sweep and the teardown
// finalize read, whether from this process, a later invocation, or a crashed one.
type sessionHandle struct {
	SessionID string            `json:"session"`
	StateDir  string            `json:"state_dir"`
	Transport sessionTransport  `json:"transport"`
	Unit      string            `json:"unit,omitempty"`
	PID       int               `json:"pid,omitempty"`
	Pidfile   string            `json:"pidfile,omitempty"`
	Command   []string          `json:"command,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Status    string            `json:"status"` // "active" | "stopped"
	StartedAt string            `json:"started_at,omitempty"`
	StoppedAt string            `json:"stopped_at,omitempty"`
}

const (
	sessionStatusActive  = "active"
	sessionStatusStopped = "stopped"
)

// sessionStateDir returns the state dir for one session: the run's capture root +
// <session-id>. The capture root is per-run state (like summary.yml), so consecutive runs
// of the same bed — and every segment of one run — derive the same session path only
// within ONE .check/<bed>/<calver>; the orphan sweep scans across calevers.
func sessionStateDir(logDir, sessionID string) string {
	return filepath.Join(logDir, "capture", sessionID)
}

// handlePath is the persisted handle file inside a session's state dir.
func handlePath(stateDir string) string { return filepath.Join(stateDir, "handle.json") }

// systemdUserSessionAvailable reports whether a per-user systemd manager is present to host
// charly-capture-* transient units. A package-level var so tests can pin either side
// deterministically; the default probe is quick and non-destructive (is-system-running
// exits 0 for running/degraded — any answer within the timeout counts as "the manager is
// there"; the unit spawn itself still reports its own failures loudly).
var systemdUserSessionAvailable = func() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "systemctl", "--user", "is-system-running").Run() == nil
}

// spawnSession starts ONE detached capture session and persists its handle. The transport
// is chosen HERE — the caller (a capture provider over the seam, or the runner itself)
// never picks it and never spawns a process.
func spawnSession(ctx context.Context, opts sessionSpawnOpts) (*sessionHandle, error) {
	if opts.SessionID == "" {
		return nil, fmt.Errorf("session spawn: empty session id")
	}
	if len(opts.Command) == 0 {
		return nil, fmt.Errorf("session spawn %q: empty recorder command", opts.SessionID)
	}
	stateDir := sessionStateDir(opts.LogDir, opts.SessionID)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("session spawn %q: state dir %s: %w", opts.SessionID, stateDir, err)
	}
	h := &sessionHandle{
		SessionID: opts.SessionID,
		StateDir:  stateDir,
		Command:   append([]string(nil), opts.Command...),
		Env:       cloneEnv(opts.Env),
		Status:    sessionStatusActive,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	var err error
	if systemdUserSessionAvailable() {
		err = spawnSystemdUnit(ctx, h, opts)
	} else {
		err = spawnSetsidProcess(ctx, h, opts)
	}
	if err != nil {
		return nil, fmt.Errorf("session spawn %q: %w", opts.SessionID, err)
	}
	if werr := writeSessionHandle(h); werr != nil {
		// The process is already up; a handle we cannot persist is unrecoverable state —
		// stop the session so a sweep never sees a live unit with no record.
		stopSession(ctx, h)
		return nil, fmt.Errorf("session spawn %q: persisting handle: %w", opts.SessionID, werr)
	}
	return h, nil
}

// spawnSystemdUnit starts the recorder as a transient user service: systemd-run --user
// --collect --unit charly-capture-<id> -- <argv...>. --collect makes the unit disappear on
// exit, so the UNIT NAME is the liveness key; the recorder traps SIGTERM (systemctl stop)
// and emits its end-of-stream marker. The service inherits the caller env; Env pairs ride
// --setenv.
func spawnSystemdUnit(ctx context.Context, h *sessionHandle, opts sessionSpawnOpts) error {
	unit := sessionUnitPrefix + deploykit.SanitizeUnitName(opts.SessionID)
	argv := []string{"systemd-run", "--user", "--collect", "--unit", unit}
	for k, v := range opts.Env {
		argv = append(argv, "--setenv", k+"="+v)
	}
	argv = append(argv, "--")
	argv = append(argv, opts.Command...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}
	// The service is detached by systemd; systemd-run's own stdout is only its
	// "Running as unit:" line, which must not leak into the bed's step logs.
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemd-run (unit %s): %v: %s", unit, err, strings.TrimSpace(string(out)))
	}
	h.Transport = sessionTransportSystemd
	h.Unit = unit
	return nil
}

// spawnSetsidProcess starts the recorder as a setsid(2) process-group leader (the
// spec/proc pattern; the stop ladder is ProcessShutdownGrace's order) and persists the pid
// to a pidfile — the systemd-less fallback, same lifecycle contract: the process survives
// the parent, the pidfile is the liveness key, SIGTERM triggers the recorder's
// end-of-stream marker.
func spawnSetsidProcess(ctx context.Context, h *sessionHandle, opts sessionSpawnOpts) error {
	// NOT exec.CommandContext: the recorder is DETACHED by design (setsid + pidfile,
	// the handle is the liveness key) and must outlive the caller. The reverse-leg
	// InvokeProvider that dispatches this seam wraps its ctx with a timeout + defer
	// cancel() — the cancel fires the moment the spawn call returns, and
	// CommandContext would SIGKILL the just-spawned recorder before it ever dials
	// (RCA'd live: every instrument-bed recorder died at spawn with an empty
	// recorder.log and no evidence row). The ctx bounds nothing here: the spawn is
	// fast, and the process lifetime is owned by the handle + the stop ladder.
	cmd := exec.Command(opts.Command[0], opts.Command[1:]...)
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}
	cmd.Env = append(os.Environ(), envPairs(opts.Env)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// The recorder is detached — its stdout/stderr belong to its own session, not the bed's
	// step log. The disk handle + the recorder's end-of-stream artifact ARE the record; the
	// recorder's OWN stderr is retained to <state_dir>/recorder.log so a recorder that
	// exits without its end-of-stream artifact (a dial failure, a crash) is diagnosable
	// instead of a silent "evidence row missing" (R1: the failure must be visible).
	cmd.Stdout = nil
	var logf *os.File
	if f, lerr := os.Create(filepath.Join(h.StateDir, "recorder.log")); lerr == nil {
		logf = f
		cmd.Stderr = logf
	} else {
		cmd.Stderr = nil
	}
	if err := cmd.Start(); err != nil {
		if logf != nil {
			_ = logf.Close()
		}
		return fmt.Errorf("setsid start: %w", err)
	}
	// The recorder is detached and outlives the caller: the child inherited its own
	// fd reference at fork, so the parent's copy must be closed here or it leaks on
	// every spawn (B18).
	if logf != nil {
		_ = logf.Close()
	}
	pid := cmd.Process.Pid
	pidfile := filepath.Join(h.StateDir, "pid")
	if err := os.WriteFile(pidfile, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		return fmt.Errorf("setsid pidfile %s: %w", pidfile, err)
	}
	h.Transport = sessionTransportSetsid
	h.PID = pid
	h.Pidfile = pidfile
	// Reap the child in the background so a finished recorder never leaves a zombie behind
	// (the handle flips to stopped when it exits on its own).
	go func() {
		_ = cmd.Wait()
		h.Status = sessionStatusStopped
		h.StoppedAt = time.Now().UTC().Format(time.RFC3339)
		_ = writeSessionHandle(h)
	}()
	return nil
}

// cloneEnv copies an env map (spawn inputs must not alias the caller's map).
func cloneEnv(env map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = v
	}
	return out
}

// envPairs renders an env map as the K=V slice a command environment expects, in sorted
// key order — the recorder's env must be reproducible across spawns and tests.
func envPairs(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

// SessionHandleFromDisk loads a persisted handle from its state dir; a missing handle is
// the (nil, nil) case — no session on record, nothing to reap or finalize.
func SessionHandleFromDisk(stateDir string) (*sessionHandle, error) {
	b, err := os.ReadFile(handlePath(stateDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var h sessionHandle
	if err := json.Unmarshal(b, &h); err != nil {
		return nil, fmt.Errorf("session handle %s: %w", handlePath(stateDir), err)
	}
	return &h, nil
}

func writeSessionHandle(h *sessionHandle) error {
	b, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(handlePath(h.StateDir), b, 0o600)
}

// stopSession finalizes ONE session: unit stop (bounded, rc=5 tolerated) or the
// SIGTERM→SIGKILL process-group ladder (proc.ProcessShutdownGrace). Idempotent: a stopped
// handle is a no-op. Best-effort by design — the finalize and the sweep must not fail the
// bed.
func stopSession(ctx context.Context, h *sessionHandle) {
	if h == nil || h.Status == sessionStatusStopped {
		return
	}
	switch h.Transport {
	case sessionTransportSystemd:
		stopSystemdUnit(ctx, h.Unit)
	case sessionTransportSetsid:
		stopSetsidProcess(ctx, h)
	default:
		return
	}
	h.Status = sessionStatusStopped
	h.StoppedAt = time.Now().UTC().Format(time.RFC3339)
	_ = writeSessionHandle(h)
}

// stopSystemdUnit stops one transient unit under the same bounded-timeout discipline the
// ephemeral-TTL reaper used: systemctl --user stop <unit>; an rc=5 (unit not loaded — the
// recorder already exited and --collect released the unit) is the "already gone" case, not
// an error (RDD-2).
func stopSystemdUnit(ctx context.Context, unit string) {
	if unit == "" {
		return
	}
	sctx, cancel := context.WithTimeout(ctx, sessionStopTimeout)
	defer cancel()
	out, err := exec.CommandContext(sctx, "systemctl", "--user", "stop", unit).CombinedOutput()
	if err == nil {
		return
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 5 {
		return // unit not loaded — already torn down
	}
	fmt.Fprintf(os.Stderr, "warning: stopping session unit %q: %v: %s\n", unit, err, strings.TrimSpace(string(out)))
}

// stopSetsidProcess runs the shutdown ladder (SIGTERM to the group → grace → SIGKILL to
// the group — proc.ShutdownProcessGroup's order) with no cmd to wait on: the pidfile
// carries the group-leader pid and liveness polls kill(pid, 0).
func stopSetsidProcess(ctx context.Context, h *sessionHandle) {
	if h.PID <= 0 {
		return
	}
	// SIGTERM to the whole process group (negative pid — the Setpgid leader).
	_ = syscall.Kill(-h.PID, syscall.SIGTERM)
	deadline := time.Now().Add(proc.ProcessShutdownGrace)
	for time.Now().Before(deadline) {
		if !processAlive(h.PID) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
	if !processAlive(h.PID) {
		return
	}
	_ = syscall.Kill(-h.PID, syscall.SIGKILL)
	deadline = time.Now().Add(proc.ProcessShutdownGrace)
	for time.Now().Before(deadline) && processAlive(h.PID) {
		time.Sleep(50 * time.Millisecond)
	}
}

// processAlive reports whether pid exists (the kill(pid, 0) liveness probe).
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// SessionLiveness reports whether a persisted handle's session is still running: the unit
// is-active probe (exit 0) for a systemd handle, kill(pid, 0) for a setsid handle. A
// stopped handle is not alive by construction.
func SessionLiveness(ctx context.Context, h *sessionHandle) bool {
	if h == nil || h.Status == sessionStatusStopped {
		return false
	}
	switch h.Transport {
	case sessionTransportSystemd:
		return exec.CommandContext(ctx, "systemctl", "--user", "is-active", h.Unit).Run() == nil
	case sessionTransportSetsid:
		return processAlive(h.PID)
	}
	return false
}

// sweepStaleSessions stops every still-active capture session recorded under a bed's run
// tree — .check/<bed>/*/capture/<id>/handle.json — the bedSetup orphan sweep: a crashed
// or killed run leaves its recorder units armed, and this is the same reaper pointed at
// the stale handles (bounded stops, rc=5 tolerated). Best-effort by design.
func sweepStaleSessions(ctx context.Context, bedCheckDir string) {
	captureDirs, err := filepath.Glob(filepath.Join(bedCheckDir, "*", "capture", "*"))
	if err != nil {
		return
	}
	for _, dir := range captureDirs {
		h, herr := SessionHandleFromDisk(dir)
		if herr != nil || h == nil || h.Status != sessionStatusActive {
			continue
		}
		fmt.Fprintf(os.Stderr, "charly check run: reaping stale capture session %q\n", h.SessionID)
		stopSession(ctx, h)
	}
}

// finalizeSessionDir stops every still-active session recorded under ONE run's capture
// root — the teardown-owner enforcement: the runner's cleanup (and its deferred
// finalizer) finalizes all live sessions on EVERY terminal path before returning.
func finalizeSessionDir(ctx context.Context, logDir string) {
	stateDirs, err := filepath.Glob(filepath.Join(logDir, "capture", "*"))
	if err != nil {
		return
	}
	for _, dir := range stateDirs {
		h, herr := SessionHandleFromDisk(dir)
		if herr != nil || h == nil || h.Status != sessionStatusActive {
			continue
		}
		fmt.Fprintf(os.Stderr, "charly check run: finalizing capture session %q\n", h.SessionID)
		stopSession(ctx, h)
	}
}

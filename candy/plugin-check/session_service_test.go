package check

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// session_service_test.go — the background-session service's deterministic core: transport
// selection, the setsid spawn→stop lifecycle against a REAL detached recorder, handle
// round-trips, the orphan sweep, and idempotent stops. The systemd unit path is
// environment-dependent (RDD-2 proved it live), so the systemd side is covered by the
// selection test + the live bed R10; the full lifecycle here runs on the setsid transport,
// which is deterministic in any environment.

// forceSetsidTransport pins the transport probe so the setsid path is deterministic.
func forceSetsidTransport(t *testing.T) {
	t.Helper()
	old := systemdUserSessionAvailable
	systemdUserSessionAvailable = func() bool { return false }
	t.Cleanup(func() { systemdUserSessionAvailable = old })
}

func TestSpawnSessionSetsidLifecycle(t *testing.T) {
	forceSetsidTransport(t)
	dir := t.TempDir()
	logDir := filepath.Join(dir, ".check", "check-test", "2026.999.9999")
	h, err := spawnSession(context.Background(), sessionSpawnOpts{
		SessionID: "check-test.vm.screen",
		Command:   []string{"sh", "-c", "sleep 30"},
		LogDir:    logDir,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	// Read the lifecycle fields through the locked snapshot: the reaper goroutine can
	// transition Status concurrently (spawns a detached recorder that may exit on its own),
	// so a direct struct read under -race is a data race, not a test of the service.
	status, transport, _, pid := h.snapshot()
	if transport != sessionTransportSetsid {
		t.Fatalf("transport = %q, want setsid", transport)
	}
	if status != sessionStatusActive {
		t.Fatalf("status = %q, want active", status)
	}
	if pid <= 0 {
		t.Fatalf("pid not recorded")
	}
	if _, err := os.Stat(h.Pidfile); err != nil {
		t.Fatalf("pidfile %s missing: %v", h.Pidfile, err)
	}
	_ = h

	// The handle must be on disk (the cross-invocation source of truth) and readable.
	disk, derr := SessionHandleFromDisk(h.StateDir)
	if derr != nil || disk == nil {
		t.Fatalf("handle from disk: %v %v", disk, derr)
	}
	if disk.SessionID != "check-test.vm.screen" || disk.Transport != sessionTransportSetsid {
		t.Fatalf("disk handle wrong: %+v", disk)
	}

	// Liveness: the process is alive before the stop, dead after.
	if !SessionLiveness(context.Background(), disk) {
		t.Fatal("session should be alive after spawn")
	}
	stopSession(context.Background(), disk)
	if SessionLiveness(context.Background(), disk) {
		t.Fatal("session should be dead after stop")
	}
	if disk.Status != sessionStatusStopped {
		t.Fatalf("status = %q after stop, want stopped", disk.Status)
	}
	// Idempotent: stopping again is a no-op, never an error.
	stopSession(context.Background(), disk)
}

func TestSpawnSessionRejectsBadInput(t *testing.T) {
	forceSetsidTransport(t)
	_, err := spawnSession(context.Background(), sessionSpawnOpts{SessionID: "x", LogDir: t.TempDir()})
	if err == nil {
		t.Fatal("empty command must error")
	}
	_, err = spawnSession(context.Background(), sessionSpawnOpts{Command: []string{"sh", "-c", "true"}, LogDir: t.TempDir()})
	if err == nil {
		t.Fatal("empty session id must error")
	}
}

// TestOrphanSweepReapsStaleSession plants a REAL detached recorder under a "previous run"
// calver and proves the bedSetup sweep finalizes it (the crashed-run recovery path).
func TestOrphanSweepReapsStaleSession(t *testing.T) {
	forceSetsidTransport(t)
	base := t.TempDir()
	oldCalver := filepath.Join(base, "check-orphan-bed", "2026.100.0100")
	h, err := spawnSession(context.Background(), sessionSpawnOpts{
		SessionID: "check-orphan-bed.vm.screen",
		Command:   []string{"sh", "-c", "sleep 30"},
		LogDir:    oldCalver,
	})
	if err != nil {
		t.Fatalf("plant stale session: %v", err)
	}

	sweepStaleSessions(context.Background(), filepath.Join(base, "check-orphan-bed"))

	disk, _ := SessionHandleFromDisk(h.StateDir)
	if disk == nil || disk.Status != sessionStatusStopped {
		t.Fatalf("stale session not finalized: %+v", disk)
	}
	if SessionLiveness(context.Background(), disk) {
		t.Fatal("stale recorder process still alive after sweep")
	}
}

// TestSweepIgnoresFreshStateDir: a capture dir with no handle (or a stopped handle) must
// not trip the sweep (the rc=5/already-gone tolerance).
func TestSweepToleratesHandlelessStateDir(t *testing.T) {
	forceSetsidTransport(t)
	base := t.TempDir()
	orphanDir := filepath.Join(base, "check-x", "2026.101.0101", "capture", "some-id")
	if err := os.MkdirAll(orphanDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sweepStaleSessions(context.Background(), filepath.Join(base, "check-x")) // must not panic/error
	// A stopped handle: the sweep skips it silently.
	logDir := filepath.Join(base, "check-x", "2026.102.0102")
	h, err := spawnSession(context.Background(), sessionSpawnOpts{
		SessionID: "check-x.vm.t",
		Command:   []string{"sh", "-c", "sleep 30"},
		LogDir:    logDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	stopSession(context.Background(), h)
	sweepStaleSessions(context.Background(), filepath.Join(base, "check-x"))
	disk, _ := SessionHandleFromDisk(h.StateDir)
	if disk == nil || disk.Status != sessionStatusStopped {
		t.Fatalf("stopped handle disturbed by sweep: %+v", disk)
	}
}

// TestFinalizeSessionDir stops live sessions under ONE run dir (the teardown-owner path).
func TestFinalizeSessionDir(t *testing.T) {
	forceSetsidTransport(t)
	logDir := filepath.Join(t.TempDir(), ".check", "check-f", "2026.103.0103")
	h, err := spawnSession(context.Background(), sessionSpawnOpts{
		SessionID: "check-f.vm.live",
		Command:   []string{"sh", "-c", "sleep 30"},
		LogDir:    logDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	finalizeSessionDir(context.Background(), logDir)
	disk, _ := SessionHandleFromDisk(h.StateDir)
	if disk == nil || disk.Status != sessionStatusStopped {
		t.Fatalf("finalize left session live: %+v", disk)
	}
	if SessionLiveness(context.Background(), disk) {
		t.Fatal("recorder still alive after finalize")
	}
}

// TestSessionEnvPairsDeterministic: the recorder env must be sorted (reproducible spawns).
func TestSessionEnvPairsDeterministic(t *testing.T) {
	got := envPairs(map[string]string{"Z": "1", "A": "2", "M": "3"})
	want := []string{"A=2", "M=3", "Z=1"}
	if len(got) != len(want) {
		t.Fatalf("envPairs = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("envPairs = %v, want %v", got, want)
		}
	}
}

// TestSessionStateDirShape: the state dir lives under the run's capture root.
func TestSessionStateDirShape(t *testing.T) {
	got := sessionStateDir("/a/.check/b/2026.1.2", "b.vm.screen")
	want := filepath.Join("/a/.check/b/2026.1.2", "capture", "b.vm.screen")
	if got != want {
		t.Fatalf("state dir = %q, want %q", got, want)
	}
}

// TestSessionSystemdRunArgsPinsWorkingDirectory is the regression test for the
// appium/check-android-emulator-pod evidence-row split (RCA: opencharly/pod-android-emulator-layer#10):
// the recorder's state_dir / artifact_dir are the run's RELATIVE `.check/<bed>/<calver>/...` paths,
// but a `systemd-run --user` transient unit's ExecStart runs under the user manager's default
// WorkingDirectory (the caller's home), so without `--working-directory=` the recorder wrote under
// `$HOME/.check/...` while the runner polled the worktree-relative `.check/...` — a silent
// evidence-row-missing split. The flag must ALWAYS be present, pinned to the runner's own cwd when no
// explicit Dir is given.
func TestSessionSystemdRunArgsPinsWorkingDirectory(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got, err := sessionSystemdRunArgs("charly-capture-bed.vm.appium-screen", sessionSpawnOpts{
		SessionID: "bed.vm.appium-screen",
		Command:   []string{"/usr/local/bin/charly-appium", "__dummy-arg"},
		Env:       map[string]string{"CHARLY_APPIUM_STATE_DIR": ".check/bed/2026.1.1/capture/bed.vm.appium-screen"},
	})
	if err != nil {
		t.Fatalf("sessionSystemdRunArgs: %v", err)
	}
	// Invariants, not an exact argv: a real regression (the flag dropped or retargeted) is
	// distinguishable from a benign addition, exactly as the plugin-fleet precedent test asserts.
	if !slices.Contains(got, "--working-directory="+wd) {
		t.Errorf("systemd-run argv MUST pin --working-directory=%s (the caller's cwd); got %v", wd, got)
	}
	// The command must stay contiguous AND last: systemd-run treats the first non-flag word as
	// the command, so any flag appended after it is handed to the recorder, not to systemd-run.
	tail := []string{"/usr/local/bin/charly-appium", "__dummy-arg"}
	for i, w := range tail {
		if g := got[len(got)-len(tail)+i]; g != w {
			t.Errorf("tail[%d] = %q, want %q (recorder argv must be contiguous and last)", i, g, w)
		}
	}
	// An explicit Dir is honoured and absolutized (a relative Dir would re-introduce the split).
	got2, err := sessionSystemdRunArgs("u", sessionSpawnOpts{Dir: ".", Command: []string{"x"}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got2, "--working-directory="+wd) {
		t.Errorf("relative Dir must be absolutized; got %v", got2)
	}
}

// TestSpawnSystemdUnitRecordsUnderProjectCwd is the LIVE integration test for the appium
// evidence-row split (RCA: opencharly/pod-android-emulator-layer#10, opencharly/plugin-check#68):
// it drives the REAL spawnSystemdUnit path against a live `systemd-run --user` and proves the
// load-bearing property the unit test can only assert structurally — the recorder's RELATIVE
// paths resolve under the project cwd, not the user manager's default $HOME. A recorder whose
// argv contains a relative artifact path is spawned; the test then asserts that path landed under
// cwd and did NOT land under $HOME. SKIPS cleanly when no live user systemd session is present
// (R-live-or-skip), never a mock of the systemd boundary.
func TestSpawnSystemdUnitRecordsUnderProjectCwd(t *testing.T) {
	if !systemdUserSessionAvailable() {
		t.Skip("no live user systemd session (systemd-run --user unavailable) — skipping the live spawn proof")
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		t.Skip("systemd-run not in PATH — skipping the live spawn proof")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	rel := filepath.Join("relcheck", "recorder-marker.txt")
	cwdRelPath := filepath.Join(project, rel)
	homeRelPath := filepath.Join(home, rel)
	// Precondition: the probe path must not already exist under $HOME, or the negative
	// assertion could pass for the wrong reason.
	if _, err := os.Stat(homeRelPath); err == nil {
		t.Fatalf("probe path %s already exists under $HOME; cannot assert the split", homeRelPath)
	}
	// Chdir so the runner-root premise holds (main.go's os.Chdir(cli.Dir) has already run by the
	// time any spawn executes); restore in cleanup because t.Chdir is unavailable before go1.24.
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	sessionID := "check-live.vm.appium-screen"
	h, err := spawnSession(context.Background(), sessionSpawnOpts{
		SessionID: sessionID,
		Command:   []string{"sh", "-c", "mkdir -p \"$(dirname '" + rel + "')\"; : > '" + rel + "'"},
		LogDir:    ".check",
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		stopSession(ctx, h)
	})
	if h.Transport != sessionTransportSystemd {
		t.Fatalf("expected the systemd transport, got %q", h.Transport)
	}
	// The transient unit's ExecStart is asynchronous; pace on the observable artifact (R4 — no
	// sleep/retry), bounded by process shutdown grace.
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, statErr := os.Stat(cwdRelPath); statErr == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("recorder marker %s never appeared under the project cwd — --working-directory= is not pinned", cwdRelPath)
		}
		time.Sleep(100 * time.Millisecond)
	}
	// THE NEGATIVE ASSERTION — this is the bug: pre-fix the marker lands under $HOME instead.
	if _, statErr := os.Stat(homeRelPath); statErr == nil {
		t.Fatalf("recorder wrote %s under $HOME — the transient unit ran at the manager default, not the project cwd", homeRelPath)
	}
}

// TestSpawnSessionStopWithinTimeout: a stop must return within the bounded window even for
// a recorder that ignores SIGTERM (the SIGKILL escalation ladder ends it).
func TestSpawnSessionStopEscalates(t *testing.T) {
	forceSetsidTransport(t)
	h, err := spawnSession(context.Background(), sessionSpawnOpts{
		SessionID: "check-e.vm.stuck",
		// Ignore SIGTERM (trap "") — only the SIGKILL escalation can end it.
		Command: []string{"sh", "-c", "trap '' TERM; while true; do sleep 1; done"},
		LogDir:  filepath.Join(t.TempDir(), ".check", "check-e", "2026.104.0104"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	stopSession(ctx, h)
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("stop took %v — the bounded escalation ladder is broken", d)
	}
	if SessionLiveness(context.Background(), h) {
		t.Fatal("stuck recorder still alive")
	}
}

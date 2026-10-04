package check

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/opencharly/spec/proc"
)

// Every bed run must SAY WHY it ended (opencharly/charly#779).
//
// The defect this file closes: writeBedSummary had exactly three call sites — the
// prereq-skip arm, the fail() tail and the success tail — so any death that was not one of
// those three left a run dir holding per-step logs and NO summary.yml. Such a run is
// indistinguishable from a run that is merely slow, and there is nothing in the dir to
// diagnose from: the driver is unnamed and the in-flight step is unrecorded.
//
// Two mechanisms are needed, because they cover DISJOINT exit paths:
//
//   - The DEFERRED writer in runCheckBed covers returns and panics — everything that
//     unwinds. It CANNOT cover the signal path: proc.InstallSignalHandler runs its cleanups
//     and its hooks and then re-raises with the default disposition
//     (spec/proc/cleanup.go:130-142), so the process dies without unwinding and NO deferred
//     function runs.
//   - This file's registered SHUTDOWN HOOK covers exactly that path — runShutdownHooks() is
//     invoked before the re-raise — and writes a stopped-state verdict from primitives only.
//
// SIGKILL is uncatchable (spec/proc/cleanup.go:126-129; this plugin's own stop_cmd.go
// escalates to it), so a SIGKILLed run still cannot record a verdict. That is a stated
// bound, not an oversight.

// bedDriver identifies the binary driving this run. Recorded because a run dir that cannot
// name its driver cannot be reproduced: a worktree/dev build and an installed /usr/bin/charly
// are otherwise indistinguishable in the step logs. Size+mtime is the discriminator, and it
// needs no subprocess — which matters, because this value is read on the signal path where
// spawning a child would be both unbounded and unsafe.
type bedDriver struct {
	Path  string
	Size  int64
	MTime string
	// Version is the driver's `charly version` identity — the CalVer stamped into the binary
	// at build time, and the one field that distinguishes a dev/worktree build from a released
	// one by VALUE rather than by artifact hash (opencharly/charly#779, item 2).
	Version string
}

// buildCalVerLDFlag is the linker variable scripts/bootstrap-charly.sh stamps the binary's
// CalVer identity into: `-ldflags "-X main.BuildCalVer=<calver>"`. Reading it out of the
// build info is what lets this plugin report the SAME string `charly version` reports
// (charly/charly/version.go: CharlyVersion) without importing charly's main package.
const buildCalVerLDFlag = "main.BuildCalVer="

// charlyBuildVersion reports the running binary's `charly version` identity.
//
// It reads THIS process's build info rather than a variable, because the plugin cannot import
// charly's main package and the CalVer reaches a plugin by no env var and no exported constant
// (measured: every occurrence in charly is package main's own var, its tests, and the two
// scripts that pass the flag). ReadBuildInfo is in-process and bounded, which matters because
// this value is read on the SIGNAL path, where spawning a child would be neither.
//
// Main.Version is NOT a substitute: for a build stamped this way it is "(devel)", while the
// CalVer survives inside the recorded `-ldflags` setting (both measured on a real bootstrap-form
// build). An unstamped build reports "unknown", which is CharlyVersion()'s own word for one.
func charlyBuildVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok || bi == nil {
		return "unknown"
	}
	return buildVersionFrom(bi.Settings, bi.Main.Version)
}

// buildVersionFrom is the decision, split out so it is testable against real debug.BuildSetting
// values: the live read above can only ever observe THIS binary's own settings, so a test that
// called it alone could not exercise the stamped case at all.
func buildVersionFrom(settings []debug.BuildSetting, mainVersion string) string {
	for _, s := range settings {
		if s.Key == "-ldflags" {
			if v := buildCalVerFromLDFlags(s.Value); v != "" {
				return v
			}
		}
	}
	if mainVersion != "" && mainVersion != "(devel)" {
		return mainVersion
	}
	return "unknown"
}

// buildCalVerFromLDFlags extracts the CalVer from a recorded -ldflags string, or "" when the
// string carries no stamp. The value is a whitespace-separated flag list in which the stamp
// appears as a field of its own after -X (e.g. `-X main.BuildCalVer=2026.277.1533`), and it may
// sit beside other -X assignments, so the field is matched by prefix rather than parsed
// positionally.
func buildCalVerFromLDFlags(ldflags string) string {
	for _, f := range strings.Fields(ldflags) {
		if v, found := strings.CutPrefix(f, buildCalVerLDFlag); found {
			return v
		}
	}
	return ""
}

// currentBedDriver resolves the active executable. os.Executable() answers for THIS process,
// which is the point: `command -v charly` answers for the asking shell, not for the process
// whose argv[0] we are recording.
func currentBedDriver() bedDriver {
	d := bedDriver{Path: "unknown", Version: charlyBuildVersion()}
	path, err := os.Executable()
	if err != nil || path == "" {
		return d
	}
	d.Path = path
	if st, err := os.Stat(path); err == nil {
		d.Size = st.Size()
		d.MTime = st.ModTime().UTC().Format(time.RFC3339)
	}
	return d
}

// bedRunState is the in-flight run's stopped-state snapshot. It carries PRIMITIVES ONLY: the
// shutdown hook runs on the signal-handler goroutine, concurrently with the step sequence on
// the main one, so reading the live *bedRunResult (its Step slice in particular) there would
// race the appending main goroutine. The hook therefore never touches the live result — it
// synthesizes its own from these fields.
type bedRunState struct {
	mu     sync.Mutex
	dir    string
	bed    string
	calver string
	driver bedDriver
	start  time.Time
	step   string // the step in flight; "" between steps
	// lastStep is the last step ENTERED, and it is never cleared. The step closures clear `step`
	// with a defer, and a defer RUNS WHILE A PANIC UNWINDS — so by the time the panic arm of the
	// verdict writer reads the state, `step` is already "" and the one field that says where the
	// run died would be lost. (Measured, not assumed: see the `panic` subtest of
	// TestBedVerdictWrittenOnEveryUnwindingExit, which fails on `in_flight_step` without this
	// field — it reproduces the production closure frame, so the unwind really happens.)
	lastStep string
	done     bool // a verdict has been written for this run
}

var (
	// The hook is registered ONCE per process, not once per run: RegisterShutdownHook has no
	// unregister, so registering per run would accumulate one hook per bed in a roster and
	// fire them all at exit. One hook consults the current run instead.
	bedVerdictOnce sync.Once
	bedVerdictMu   sync.Mutex
	bedVerdictCur  *bedRunState
)

// beginBedVerdict opens the verdict guarantee for one run and arms the process-wide shutdown
// hook (once). dir is the run dir, which bedSetup created and which nothing removes — the
// only RemoveAll in the bed session is the per-bed CONFIG temp dir (bed_session.go:98/:384).
func beginBedVerdict(dir, bed, calver string) *bedRunState {
	s := &bedRunState{dir: dir, bed: bed, calver: calver, driver: currentBedDriver(), start: time.Now()}
	bedVerdictMu.Lock()
	bedVerdictCur = s
	bedVerdictMu.Unlock()
	bedVerdictOnce.Do(func() { proc.RegisterShutdownHook(onBedShutdown) })
	return s
}

// endBedVerdict closes the guarantee for this run: the deferred writer has already produced
// the verdict, so the hook must no longer consider this run in flight.
func endBedVerdict(s *bedRunState) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.done = true
	s.mu.Unlock()
	bedVerdictMu.Lock()
	if bedVerdictCur == s {
		bedVerdictCur = nil
	}
	bedVerdictMu.Unlock()
}

// enterStep / leaveStep publish the step in flight. Called from the step and phase closures,
// so the recorded name is the same string the step's own log is keyed by. leaveStep clears the
// CURRENT step only; lastStep deliberately survives it (see bedRunState).
func (s *bedRunState) enterStep(name string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.step = name
	if name != "" {
		s.lastStep = name
	}
	s.mu.Unlock()
}

func (s *bedRunState) leaveStep() { s.enterStep("") }

// snapshotStep reads the CURRENT in-flight step under the lock. This is the SIGNAL path's read
// (writeStopped): the handler records and then re-raises without unwinding
// (spec/proc/cleanup.go:130-142), so no closure defer has run and this is the step the process
// actually died in. It is "" only when the signal arrived between steps.
func (s *bedRunState) snapshotStep() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.step
}

// snapshotStepAfterUnwind reads the step that is still the truth AFTER the stack has unwound —
// the PANIC path's read. It must not be snapshotStep: the step closures clear `step` with a
// defer, that defer runs while the panic propagates, and the deferred writer only reads the
// state once the panic has reached it — so by then `step` is "" and the one field that says
// where the run died would be gone. Falling back to the last step ENTERED restores it.
func (s *bedRunState) snapshotStepAfterUnwind() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.step != "" {
		return s.step
	}
	return s.lastStep
}

// onBedShutdown is the shutdown hook body: it runs on the signal-handler goroutine before the
// re-raise that kills the process. Best-effort and bounded by contract
// (spec/proc/cleanup.go:58) — it takes one lock and writes one small file, and it must never
// spawn, wait, or retry.
func onBedShutdown() {
	bedVerdictMu.Lock()
	s := bedVerdictCur
	bedVerdictMu.Unlock()
	if s == nil {
		return
	}
	s.writeStopped("process shutdown hook fired (SIGTERM/SIGINT/SIGHUP or an explicit shutdown) while the run was still in flight")
}

// writeVerdictOnce is the ONE serialized verdict write: a run gets exactly ONE verdict, whichever
// of the two writers — the deferred writer or the shutdown hook — reaches it first.
//
// The state lock is held ACROSS the file write so the two can never interleave or clobber each
// other. Without it there is a real, if narrow, race: a signal delivered while the main goroutine
// was already unwinding would let the hook overwrite a truthful, complete, finished verdict with a
// stopped-state one. Holding the lock makes "first writer wins" an invariant rather than a hope.
// Bounded by contract: one small write, no spawn, no wait (spec/proc/cleanup.go:58).
func (s *bedRunState) writeVerdictOnce(write func()) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return false
	}
	write()
	s.done = true
	return true
}

// writeStopped records the stopped-state verdict, unless one has already been written.
//
// The hook cannot name the signal — InstallSignalHandler consumes it and re-raises without
// passing it through — so the reason names the mechanism and the step in flight names the
// place. That is still the whole point of the fix: the run dir now says where it stopped.
func (s *bedRunState) writeStopped(reason string) {
	// The step is read BEFORE the verdict lock is taken — snapshotStep takes that same lock.
	step := s.snapshotStep()
	// Synthesized, never the live result: see bedRunState's note on the race.
	res := &bedRunResult{
		Bed:           s.bed,
		CalVer:        s.calver,
		Driver:        s.driver,
		OK:            false,
		FailExitCode:  1,
		Stopped:       true,
		StoppedReason: reason,
		InFlightStep:  step,
	}
	s.writeVerdictOnce(func() { writeBedSummary(s.dir, res) })
}

// recordBedVerdict is the ONE writer of a finished run's verdict. runCheckBed defers it, so
// every UNWINDING exit — an early return, a returned error, a panic — passes through here
// (opencharly/charly#779). writeBedSummary previously had three ad-hoc call sites (the
// prereq-skip arm, the fail() tail, the success tail), so a bed that died any other way left a
// run dir holding per-step logs and NO summary.yml — indistinguishable from a run that is
// merely slow, with nothing in the dir to diagnose from.
//
// It takes the named results by POINTER because a deferred call must observe the values as the
// function actually returned them. r is the recovered panic value (nil when there was none);
// re-panicking stays the caller's job, so recording and crash semantics stay separate.
func recordBedVerdict(dir, bed string, state *bedRunState, r any, res **bedRunResult, err *error) {
	if state == nil {
		// Last-resort path: a finalizer that panics would cost the verdict AND change the crash.
		state = &bedRunState{bed: bed}
	}
	if r != nil {
		if *res == nil {
			*res = &bedRunResult{Bed: bed, CalVer: state.calver}
		}
		(*res).Driver = state.driver
		(*res).OK = false
		(*res).FailExitCode = 1
		(*res).Stopped = true
		(*res).StoppedReason = fmt.Sprintf("panic: %v", r)
		// After-unwind read, NOT snapshotStep: the step closure's deferred leaveStep has already
		// cleared the current step on the way here. See snapshotStepAfterUnwind.
		(*res).InFlightStep = state.snapshotStepAfterUnwind()
		state.writeVerdictOnce(func() { writeBedSummary(dir, *res) })
		return
	}
	if *res == nil {
		// The run never got as far as creating a result: record the failure anyway, so it is
		// visible in the run dir and not only in the caller's error string.
		*res = &bedRunResult{Bed: bed, CalVer: state.calver}
	}
	if *err != nil && !(*res).SkippedPrereq {
		// A skip is a deliberate, documented non-run (ok: true, skip_reason set); every other
		// error is a failure, including one raised before any step recorded itself.
		(*res).OK = false
		if (*res).FailExitCode == 0 {
			(*res).FailExitCode = 1
		}
	}
	(*res).Driver = state.driver
	state.writeVerdictOnce(func() { writeBedSummary(dir, *res) })
}

// stopReason formats the stopped-state fields for the summary. Kept here so the summary
// formatter in bed_run.go stays a pure writer. The reason is quoted through evidence.go's
// yamlScalar (the file's ONE scalar-quoting helper): a panic's "runtime error: index out of
// range" carries ": ", which an unquoted scalar would turn into nested structure — the record
// must survive a real YAML parser, not just a grep.
func stoppedLine(reason string) string {
	if reason == "" {
		return "stopped: true\n"
	}
	return "stopped: true\nstopped_reason: " + yamlScalar(reason) + "\n"
}

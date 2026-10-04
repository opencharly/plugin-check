package check

import (
	"fmt"
	"os"
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
}

// currentBedDriver resolves the active executable. os.Executable() answers for THIS process,
// which is the point: `command -v charly` answers for the asking shell, not for the process
// whose argv[0] we are recording.
func currentBedDriver() bedDriver {
	d := bedDriver{Path: "unknown"}
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
	done   bool   // a verdict has been written for this run
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
// so the recorded name is the same string the step's own log is keyed by.
func (s *bedRunState) enterStep(name string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.step = name
	s.mu.Unlock()
}

func (s *bedRunState) leaveStep() { s.enterStep("") }

// snapshotStep reads the in-flight step under the lock, for the deferred writer's panic and
// stopped-state records on the main goroutine.
func (s *bedRunState) snapshotStep() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.step
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

// writeStopped records the stopped-state verdict, unless one has already been written.
//
// The hook cannot name the signal — InstallSignalHandler consumes it and re-raises without
// passing it through — so the reason names the mechanism and the step in flight names the
// place. That is still the whole point of the fix: the run dir now says where it stopped.
func (s *bedRunState) writeStopped(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return
	}
	// Synthesized, never the live result: see bedRunState's note on the race.
	res := &bedRunResult{
		Bed:           s.bed,
		CalVer:        s.calver,
		Driver:        s.driver,
		OK:            false,
		FailExitCode:  1,
		Stopped:       true,
		StoppedReason: reason,
		InFlightStep:  s.step,
	}
	writeBedSummary(s.dir, res)
	s.done = true
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
		(*res).InFlightStep = state.snapshotStep()
		writeBedSummary(dir, *res)
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
	writeBedSummary(dir, *res)
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

package check

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readSummary reads the run dir's summary.yml, failing the test when there is none.
//
// "There is no summary.yml" IS the defect under test (opencharly/charly#779), so every caller
// here treats an absent file as a failure rather than skipping — a run dir with step logs and no
// verdict cannot be diagnosed, which is exactly what made the runs behind #779 unreproducible.
func readSummary(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "summary.yml"))
	if err != nil {
		t.Fatalf("no summary.yml in the run dir %s: %v (the run left logs and no verdict)", dir, err)
	}
	return string(raw)
}

func mustContain(t *testing.T, got, want, what string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("%s: summary.yml = %q, want it to contain %q", what, got, want)
	}
}

// TestBedVerdictWrittenOnEveryUnwindingExit is the regression test for opencharly/charly#779.
// writeBedSummary had exactly three ad-hoc call sites — the prereq-skip arm, the fail() tail and
// the success tail — so any OTHER death left a run dir with per-step logs and no summary.yml.
// recordBedVerdict is now the single writer on every unwinding path; each subtest asserts the run
// dir carries a verdict for one of them, and asserts the fields a reader needs to tell the states
// apart (ok, stopped, in_flight_step, skip_reason).
func TestBedVerdictWrittenOnEveryUnwindingExit(t *testing.T) {
	// notStopped fails if a summary claims the run was stopped: `stopped:` must appear ONLY on the
	// signal/panic paths, so its absence is itself the statement that the run exited normally.
	notStopped := func(t *testing.T, got, what string) {
		t.Helper()
		if strings.Contains(got, "stopped:") {
			t.Fatalf("%s: summary.yml = %q, want NO stopped: line", what, got)
		}
	}

	t.Run("panic", func(t *testing.T) {
		dir := t.TempDir()
		state := &bedRunState{dir: dir, bed: "check-foo", calver: "2026.275.1200"}
		res := &bedRunResult{Bed: "check-foo", CalVer: "2026.275.1200", OK: true}
		var err error

		// THE PRODUCTION FRAME, not a shortcut — this subtest must exercise the real UNWIND.
		// In bed_run.go a step closure publishes itself and clears itself with a defer, and that
		// defer RUNS WHILE THE PANIC PROPAGATES — before runCheckBed's own deferred writer gets
		// control and reads the state. An earlier revision of this test called enterStep and then
		// the writer directly, with no unwind in between, so it passed even though the production
		// panic path had lost the step entirely: `leaveStep` had already blanked it. Reproducing
		// the closure shape here is what makes the in_flight_step assertion below load-bearing.
		stepClosure := func() {
			state.enterStep("check-live")
			defer state.leaveStep()
			panic("runtime error: index out of range")
		}
		func() {
			defer func() {
				r := recover()
				recordBedVerdict(dir, "check-foo", state, r, &res, &err)
			}()
			stepClosure()
		}()

		got := readSummary(t, dir)
		mustContain(t, got, "stopped: true", "panic path")
		// The reason must survive a real YAML parser: it carries ": ", which unquoted would parse
		// as nested structure instead of a scalar.
		mustContain(t, got, `stopped_reason: "panic: runtime error: index out of range"`, "panic path")
		// The step the run was IN when it died, read after the unwind cleared it.
		mustContain(t, got, "in_flight_step: check-live", "panic path")
		mustContain(t, got, "ok: false", "panic path")
	})

	t.Run("error", func(t *testing.T) {
		dir := t.TempDir()
		state := &bedRunState{dir: dir, bed: "check-foo", calver: "2026.275.1200"}
		res := &bedRunResult{
			Bed: "check-foo", CalVer: "2026.275.1200", OK: true,
			Step: []stepResult{{Name: "image-build", OK: true}},
		}
		err := errors.New("deploy add failed")

		recordBedVerdict(dir, "check-foo", state, nil, &res, &err)

		got := readSummary(t, dir)
		mustContain(t, got, "ok: false", "error path")
		mustContain(t, got, "  - name: image-build", "error path")
		notStopped(t, got, "error path")
	})

	t.Run("error before any result existed", func(t *testing.T) {
		// The path that used to be entirely silent: the run died before res was built. The
		// deferred writer must still leave a verdict rather than only an error string.
		dir := t.TempDir()
		state := &bedRunState{dir: dir, bed: "check-foo", calver: "2026.275.1200"}
		var res *bedRunResult
		err := errors.New("evidence dir unwritable")

		recordBedVerdict(dir, "check-foo", state, nil, &res, &err)

		got := readSummary(t, dir)
		mustContain(t, got, "bed: check-foo", "pre-result error path")
		mustContain(t, got, "ok: false", "pre-result error path")
	})

	t.Run("success", func(t *testing.T) {
		dir := t.TempDir()
		state := &bedRunState{dir: dir, bed: "check-foo", calver: "2026.275.1200"}
		res := &bedRunResult{
			Bed: "check-foo", CalVer: "2026.275.1200", OK: true,
			Step: []stepResult{{Name: "check-live", OK: true}},
		}
		var err error

		recordBedVerdict(dir, "check-foo", state, nil, &res, &err)

		got := readSummary(t, dir)
		mustContain(t, got, "ok: true", "success path")
		notStopped(t, got, "success path")
	})

	t.Run("prereq skip stays a pass", func(t *testing.T) {
		// A prereq skip is a deliberate, documented non-run: ok: true with the reason recorded.
		// It must NOT be downgraded to a failure by the generic error arm.
		dir := t.TempDir()
		state := &bedRunState{dir: dir, bed: "check-foo", calver: "2026.275.1200"}
		res := &bedRunResult{
			Bed: "check-foo", CalVer: "2026.275.1200", OK: true,
			SkippedPrereq: true, SkipReason: "no GPU available",
		}
		var err error = &CheckSkippedError{Msg: "skipped (no GPU available)"}

		recordBedVerdict(dir, "check-foo", state, nil, &res, &err)

		got := readSummary(t, dir)
		mustContain(t, got, "ok: true", "prereq skip")
		// Previously the reason existed only in the returned error; the run dir could not say why
		// nothing ran.
		mustContain(t, got, `skip_reason: "no GPU available"`, "prereq skip")
		notStopped(t, got, "prereq skip")
	})
}

// TestBedShutdownHookRecordsTheRunInFlight pins the OTHER half of the guarantee, the one a defer
// structurally cannot cover: InstallSignalHandler re-raises with the default disposition
// (spec/proc/cleanup.go:130-142), so nothing unwinds and no deferred function runs. The registered
// shutdown hook is the vehicle that does run, and it must leave a verdict naming the step that was
// in flight.
func TestBedShutdownHookRecordsTheRunInFlight(t *testing.T) {
	dir := t.TempDir()
	state := beginBedVerdict(dir, "check-foo", "2026.275.1200")
	defer endBedVerdict(state)

	state.enterStep("check-live")
	onBedShutdown()

	got := readSummary(t, dir)
	mustContain(t, got, "stopped: true", "shutdown hook")
	mustContain(t, got, "in_flight_step: check-live", "shutdown hook")
	mustContain(t, got, "ok: false", "shutdown hook")

	// A verdict is written ONCE. A second firing — the hook runs again at exit, or a second
	// signal arrives — must not rewrite the record the first one produced.
	state.enterStep("teardown")
	onBedShutdown()
	if again := readSummary(t, dir); again != got {
		t.Fatalf("the shutdown hook rewrote an already-written verdict:\n--- first ---\n%s\n--- second ---\n%s", got, again)
	}
}

// TestBedShutdownHookIsDisarmedAfterANormalRun guards the property that makes the process-wide
// hook safe: it is registered ONCE per process, but it must only ever act on the run that is still
// in flight. A run that finished and disarmed must not have its verdict overwritten when the
// process later exits (a roster of beds would otherwise be clobbered at teardown).
func TestBedShutdownHookIsDisarmedAfterANormalRun(t *testing.T) {
	dir := t.TempDir()
	state := beginBedVerdict(dir, "check-foo", "2026.275.1200")
	res := &bedRunResult{Bed: "check-foo", CalVer: "2026.275.1200", OK: true}
	var err error
	recordBedVerdict(dir, "check-foo", state, nil, &res, &err)
	finished := readSummary(t, dir)

	endBedVerdict(state)
	state.enterStep("teardown")
	onBedShutdown()

	if after := readSummary(t, dir); after != finished {
		t.Fatalf("the hook acted on a DISARMED run:\n--- finished ---\n%s\n--- after shutdown ---\n%s", finished, after)
	}
}

// TestWriteBedSummaryNamesTheDriver pins the other half of #779's diagnosability complaint: a run
// dir that cannot name the binary that produced it cannot be reproduced. currentBedDriver stats
// the RUNNING executable (no subprocess), so a worktree/dev build and an installed /usr/bin/charly
// are told apart by size+mtime.
func TestWriteBedSummaryNamesTheDriver(t *testing.T) {
	d := currentBedDriver()
	if d.Path == "" || d.Path == "unknown" {
		t.Fatalf("currentBedDriver().Path = %q, want the running executable's path", d.Path)
	}
	if d.Size <= 0 {
		t.Fatalf("currentBedDriver().Size = %d, want the stat'd size of %s", d.Size, d.Path)
	}
	if d.MTime == "" {
		t.Fatalf("currentBedDriver().MTime is empty, want the stat'd mtime of %s", d.Path)
	}
	// The version must be filled in HERE, not only in the summary test below: that one constructs
	// a bedDriver literal, so it would still pass if currentBedDriver stopped recording the
	// version at all — and the version would then be silently missing from every real run dir.
	if d.Version == "" {
		t.Fatal("currentBedDriver().Version is empty, want the running binary's `charly version` string")
	}

	dir := t.TempDir()
	writeBedSummary(dir, &bedRunResult{Bed: "check-foo", CalVer: "2026.275.1200", OK: true, Driver: d})
	got := readSummary(t, dir)
	mustContain(t, got, "driver: "+d.Path, "driver record")
	mustContain(t, got, "driver_version: ", "driver record")
	mustContain(t, got, "driver_size: ", "driver record")
	mustContain(t, got, "driver_mtime: ", "driver record")
	if strings.Contains(got, "stopped:") {
		t.Fatalf("summary.yml = %q, want no stopped: line on a normal run", got)
	}
}

// TestFirstVerdictWins pins the ordering invariant BETWEEN the two writers: a run gets exactly one
// verdict, and the one written first stands.
//
// The narrow case it closes is a signal delivered while the main goroutine was ALREADY unwinding —
// without a lock shared across the write, the hook would overwrite a truthful, complete, finished
// verdict with a stopped-state one. The hook is deliberately left ARMED here (no endBedVerdict
// before it fires), so it genuinely tries; only the shared write lock can stop it.
func TestFirstVerdictWins(t *testing.T) {
	dir := t.TempDir()
	state := beginBedVerdict(dir, "check-foo", "2026.275.1200")
	res := &bedRunResult{Bed: "check-foo", CalVer: "2026.275.1200", OK: true}
	var err error
	recordBedVerdict(dir, "check-foo", state, nil, &res, &err)
	finished := readSummary(t, dir)
	mustContain(t, finished, "ok: true", "first verdict")

	state.enterStep("teardown")
	onBedShutdown()

	if after := readSummary(t, dir); after != finished {
		t.Fatalf("the hook overwrote the verdict the deferred writer had already recorded:\n--- first ---\n%s\n--- after the hook ---\n%s", finished, after)
	}
	endBedVerdict(state) // leave the process-global registry as we found it
}

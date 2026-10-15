package check

// bed_keeper_stop_test.go — coverage for the §5.3.2 keeper-stop decision (RCA
// 2026.261.1050): after an on_finalize golden capture the keeper domain MUST be
// stopped, or it holds an exclusive qemu write lock on
// snapshots/<name>/disk.qcow2 and every anchored clone fails with
// `Failed to get shared "write" lock` (proven live). The capture and the
// keeper-stop share ONE predicate (capturesGolden) so they can never diverge —
// this test pins that predicate, so reverting the keeper-stop (or the
// condition that guards it) fails here instead of blocking a live clone.

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

func TestCapturesGolden_FreshVMBedWithOnFinalize(t *testing.T) {
	snap := &spec.VmSnapshotPolicy{OnFinalize: "golden"}
	vm := spec.CheckBedReply{IsVM: true}

	// The fresh lane of a VM bed with an on_finalize policy captures — and so
	// must stop the keeper.
	if !capturesGolden(vm, bedRunOpts{}, snap) {
		t.Fatal("a fresh VM bed with on_finalize must capture the golden (and stop the keeper)")
	}
}

func TestCapturesGolden_AnchoredLaneDoesNot(t *testing.T) {
	snap := &spec.VmSnapshotPolicy{OnFinalize: "golden"}
	vm := spec.CheckBedReply{IsVM: true}

	// The anchored lane REVERTS to the golden; it must not re-capture (and so
	// must not stop a keeper it did not create). Stopping the keeper here would
	// be pointless churn, but capturing would be wrong.
	if capturesGolden(vm, bedRunOpts{Anchor: "golden"}, snap) {
		t.Fatal("an anchored lane must NOT capture the golden")
	}
}

func TestCapturesGolden_NonVMAndNoPolicy(t *testing.T) {
	snap := &spec.VmSnapshotPolicy{OnFinalize: "golden"}

	if capturesGolden(spec.CheckBedReply{IsVM: false}, bedRunOpts{}, snap) {
		t.Error("a non-VM bed must not capture a golden")
	}
	if capturesGolden(spec.CheckBedReply{IsVM: true}, bedRunOpts{}, nil) {
		t.Error("a VM bed with no snapshot policy must not capture a golden")
	}
	if capturesGolden(spec.CheckBedReply{IsVM: true}, bedRunOpts{}, &spec.VmSnapshotPolicy{}) {
		t.Error("a VM bed with an empty OnFinalize must not capture a golden")
	}
}

// TestBedReclaimsVmStateDir pins the #73 predicate: only a PLAIN disposable VM bed reclaims
// its per-domain state dir at teardown. Golden-capturing, keep-venue, and anchored runs must
// NOT — their state dir holds the captured golden / kept venue that later lanes depend on.
func TestBedReclaimsVmStateDir(t *testing.T) {
	vm := spec.CheckBedReply{IsVM: true}
	golden := &spec.VmSnapshotPolicy{OnFinalize: "golden"}

	// The #73 case: a plain disposable VM bed → reclaim.
	if !bedReclaimsVmStateDir(vm, bedRunOpts{}, nil) {
		t.Error("a plain disposable VM bed must reclaim its state dir (plugin-check#73)")
	}
	// Golden capture — the golden lives in <state>/snapshots/<name>/ → keep.
	if bedReclaimsVmStateDir(vm, bedRunOpts{}, golden) {
		t.Error("a golden-capturing run must NOT reclaim the state dir (throws the golden away)")
	}
	// --keep / --keep-venue → keep the venue.
	if bedReclaimsVmStateDir(vm, bedRunOpts{Keep: true}, nil) {
		t.Error("a --keep run must NOT reclaim the state dir")
	}
	// Anchored lane → reverts to the golden → keep.
	if bedReclaimsVmStateDir(vm, bedRunOpts{Anchor: "golden"}, nil) {
		t.Error("an anchored run must NOT reclaim the state dir")
	}
	// Non-VM → no per-domain VM state dir.
	if bedReclaimsVmStateDir(spec.CheckBedReply{IsVM: false}, bedRunOpts{}, nil) {
		t.Error("a non-VM bed has no VM state dir to reclaim")
	}
}

// TestVmBedDestroyArgs pins the EMITTED argv, not merely the predicate: only the plain
// disposable VM bed's final teardown carries --disk. Deleting the condition (or dropping
// --disk) fails here.
func TestVmBedDestroyArgs(t *testing.T) {
	vm := spec.CheckBedReply{IsVM: true, VMTemplate: "r10-vm", BedDomain: "check-r10-two-vm"}
	golden := &spec.VmSnapshotPolicy{OnFinalize: "golden"}

	plain := vmBedDestroyArgs(vm, bedRunOpts{}, nil)
	if !contains(plain, "--disk") {
		t.Errorf("plain VM bed teardown must pass --disk (plugin-check#73): %v", plain)
	}
	// The base argv is unchanged; --disk is appended.
	want := []string{"vm", "destroy", "r10-vm", "--domain", "check-r10-two-vm", "--if-exists", "--disk"}
	if len(plain) != len(want) {
		t.Fatalf("plain argv = %v, want %v", plain, want)
	}
	for i := range want {
		if plain[i] != want[i] {
			t.Fatalf("plain argv = %v, want %v", plain, want)
		}
	}

	if contains(vmBedDestroyArgs(vm, bedRunOpts{}, golden), "--disk") {
		t.Error("a golden-capturing run's teardown must NOT pass --disk")
	}
	if contains(vmBedDestroyArgs(vm, bedRunOpts{Keep: true}, nil), "--disk") {
		t.Error("a --keep run's teardown must NOT pass --disk")
	}
	if contains(vmBedDestroyArgs(vm, bedRunOpts{Anchor: "golden"}, nil), "--disk") {
		t.Error("an anchored run's teardown must NOT pass --disk")
	}
}

func contains(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
}

// TestGoldenCaptureSteps pins the EMITTED STEP SEQUENCE, not merely the predicate: the
// capture step (§5.3) AND the keeper-stop step (§5.3.2) are both returned by the ONE
// goldenCaptureSteps function the runner executes, so deleting the keeper-stop fails here
// even though capturesGolden still returns true. This is the coverage the validator
// demanded (B12) — the predicate-only tests above stayed green when the step call was
// removed.
func TestGoldenCaptureSteps(t *testing.T) {
	d := spec.CheckBedReply{IsVM: true, VMTemplate: "check-omarchy-eval-base-inst", BedDomain: "check-omarchy-eval-base-inst"}
	snap := &spec.VmSnapshotPolicy{OnFinalize: "golden", Mode: "external", Consistent: true}

	if !capturesGolden(d, bedRunOpts{}, snap) {
		t.Fatal("precondition: a fresh VM bed with on_finalize must capture")
	}
	want := []bedStep{
		{Name: "snapshot-capture", Argv: []string{"vm", "snapshot", "create-consistent", "check-omarchy-eval-base-inst", "golden", "--domain", "check-omarchy-eval-base-inst", "--mode", "external"}},
		{Name: "snapshot-stop-keeper", Argv: []string{"vm", "stop", "check-omarchy-eval-base-inst", "--domain", "check-omarchy-eval-base-inst"}},
	}
	got := goldenCaptureSteps(d, snap)
	if len(got) != len(want) {
		t.Fatalf("got %d steps (%v), want %d (capture THEN keeper-stop)", len(got), got, len(want))
	}
	for i := range want {
		if got[i].Name != want[i].Name || !equalArgv(got[i].Argv, want[i].Argv) {
			t.Fatalf("step %d = {%s %v}, want {%s %v}", i, got[i].Name, got[i].Argv, want[i].Name, want[i].Argv)
		}
	}
}

// equalArgv compares two argv slices element-wise (no reflect import needed).
func equalArgv(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// keeperStopStep returns the emitted keeper-stop argv for a policy, or fails the test
// when the sequence carries no keeper stop at all.
func keeperStopStep(t *testing.T, d spec.CheckBedReply, snap *spec.VmSnapshotPolicy) []string {
	t.Helper()
	for _, s := range goldenCaptureSteps(d, snap) {
		if s.Name == "snapshot-stop-keeper" {
			return s.Argv
		}
	}
	t.Fatal("goldenCaptureSteps emitted no keeper stop")
	return nil
}

// TestBedKeeperStop_ForceStopDeclared pins charly#934's knob: a bed whose keeper guest
// ignores ACPI declares force_stop: true on its snapshot policy and the emitted
// keeper-stop step then carries --force, while an undeclared policy keeps the graceful
// form it has always had. The ONLY difference is the declared intent — measured on a
// live keeper: graceful failed for the full 3m0s cap, forced released it in 38s, and
// `vm list` reported the domain shut off and still DEFINED (the venue stays KEPT).
func TestBedKeeperStop_ForceStopDeclared(t *testing.T) {
	d := spec.CheckBedReply{IsVM: true, VMTemplate: "omarchy-vm", BedDomain: "check-omarchy-pr-vm-anchored"}
	graceful := &spec.VmSnapshotPolicy{OnFinalize: "golden"}
	forced := &spec.VmSnapshotPolicy{OnFinalize: "golden", ForceStop: true}

	wantGraceful := []string{"vm", "stop", "omarchy-vm", "--domain", "check-omarchy-pr-vm-anchored"}
	wantForced := []string{"vm", "stop", "omarchy-vm", "--domain", "check-omarchy-pr-vm-anchored", "--force"}

	if got := keeperStopStep(t, d, graceful); !equalArgv(got, wantGraceful) {
		t.Errorf("undeclared policy: keeper stop %v, want the graceful form %v", got, wantGraceful)
	}
	if got := keeperStopStep(t, d, forced); !equalArgv(got, wantForced) {
		t.Errorf("force_stop: true: keeper stop %v, want the forced form %v", got, wantForced)
	}
	if got := keeperStopPath(keeperStopStep(t, d, forced)); got != "forced" {
		t.Errorf("the note must name the forced path, got %q", got)
	}
	if got := keeperStopPath(keeperStopStep(t, d, graceful)); got != "graceful" {
		t.Errorf("the note must name the graceful path, got %q", got)
	}
}

// TestBedKeeperStopRetry_OneBoundedFallback pins the BOUND on the force fallback: a
// failed graceful keeper stop retries once with the forced argv, a failed FORCED one
// retries not at all, and no other step ever falls back. So a bed whose guest ignores
// ACPI is released by the fallback, and a real failure is still reported rather than
// looped over (charly#934).
func TestBedKeeperStopRetry_OneBoundedFallback(t *testing.T) {
	d := spec.CheckBedReply{IsVM: true, VMTemplate: "omarchy-vm", BedDomain: "check-omarchy-pr-vm-anchored"}
	graceful := keeperStopArgs(d, false)
	forced := keeperStopArgs(d, true)

	got, ok := keeperStopRetry("snapshot-stop-keeper", graceful, d)
	if !ok || !keeperStopForced(got) {
		t.Fatalf("a failed graceful keeper stop must retry with the forced argv; got %v ok=%v", got, ok)
	}
	if _, ok := keeperStopRetry("snapshot-stop-keeper", forced, d); ok {
		t.Error("a failed FORCED keeper stop must not retry — the fallback is bounded to one attempt")
	}
	if _, ok := keeperStopRetry("snapshot-capture", graceful, d); ok {
		t.Error("only the keeper stop has a fallback; the capture must not")
	}
}

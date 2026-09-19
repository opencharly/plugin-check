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

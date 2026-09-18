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

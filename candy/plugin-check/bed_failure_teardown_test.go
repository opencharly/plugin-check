package check

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// The venue half of the exit-path guarantee (opencharly/plugin-check#75): a bed that dies mid-step
// must dispose of its deployed target, and retention must be an explicit opt-in. Before this
// change the failure tail had only the retention notice, so an aborted bed left a live disposable
// target — for a VM bed a whole booted guest holding vCPUs and RAM — until some later run of the
// SAME bed happened to reclaim it.

func disposableRoot() spec.DeployNode {
	yes := true
	return spec.DeployNode{Disposable: &yes}
}

// TestKeepVenue pins the DECISION: which venues are kept, and which are disposed of. Kept separate
// from the acts below so the policy is readable as a table.
func TestKeepVenue(t *testing.T) {
	ephemeral := spec.DeployNode{Ephemeral: &spec.EphemeralLifetime{KeepOnFailure: true}}
	ordinary := spec.DeployNode{}
	cases := []struct {
		name string
		opts bedRunOpts
		root spec.DeployNode
		want bool
	}{
		{"disposable root, no retention flag → dispose", bedRunOpts{}, disposableRoot(), false},
		{"--keep-on-failure → keep", bedRunOpts{KeepOnFailure: true}, disposableRoot(), true},
		{"--keep (already meant 'do not tear down') → keep", bedRunOpts{Keep: true}, disposableRoot(), true},
		{"keep_venue: policy forces Keep → keep", bedRunOpts{Keep: true, KeepVenue: true}, disposableRoot(), true},
		{"ephemeral.keep_on_failure (the score path's own field) → keep", bedRunOpts{}, ephemeral, true},
		{"root not marked disposable is never destroyed unattended → keep", bedRunOpts{}, ordinary, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := keepVenue(tc.opts, tc.root); got != tc.want {
				t.Errorf("keepVenue() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestBedVenueTeardown_RefusesWhenTheVenueIsKept is the block-1 regression: the ONE owner must read
// the SAME decision as the failure tail. The defect was a defer that called the teardown on a run
// whose failure tail had just kept the venue, so `--keep-on-failure` printed "left running for
// debugging" and then destroyed the target anyway.
func TestBedVenueTeardown_RefusesWhenTheVenueIsKept(t *testing.T) {
	cases := []struct {
		name     string
		state    venueTeardownState
		opts     bedRunOpts
		root     spec.DeployNode
		wantCall int
	}{
		{"disposable root, default → dispose", venueTeardownState{deployed: true}, bedRunOpts{}, disposableRoot(), 1},
		{"--keep-on-failure → keep, no destroy", venueTeardownState{deployed: true}, bedRunOpts{KeepOnFailure: true}, disposableRoot(), 0},
		{"--keep → keep, no destroy", venueTeardownState{deployed: true}, bedRunOpts{Keep: true}, disposableRoot(), 0},
		{"non-disposable root → keep, no destroy", venueTeardownState{deployed: true}, bedRunOpts{}, spec.DeployNode{}, 0},
		{"never deployed → nothing to do", venueTeardownState{}, bedRunOpts{}, disposableRoot(), 0},
		{"already torn down → no second destroy (R4)", venueTeardownState{deployed: true, tornDown: true}, bedRunOpts{}, disposableRoot(), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			state := tc.state
			if err := bedVenueTeardown(&state, tc.opts, tc.root, func() error {
				calls++
				return nil
			}); err != nil {
				t.Fatalf("bedVenueTeardown: %v", err)
			}
			if calls != tc.wantCall {
				t.Fatalf("teardown calls = %d, want %d", calls, tc.wantCall)
			}
			if tc.wantCall == 1 && !state.tornDown {
				t.Error("a destructive teardown must mark the state torn down, so the owner's second caller is a no-op")
			}
		})
	}
}

// TestBedFailedVenue_ThenTheOwnerRefuses is the unit-level reproduction of the full failing path:
// the failure tail keeps the venue under --keep-on-failure, and the every-unwinding-path defer that
// runs after it (same state, same decision) must NOT destroy it.
func TestBedFailedVenue_ThenTheOwnerRefuses(t *testing.T) {
	stepErr := errors.New("check-live exited 2")
	var out bytes.Buffer
	state := &venueTeardownState{deployed: true}
	calls := 0
	teardown := func() error { calls++; return nil }

	got := bedFailedVenue(&out, "check-x", spec.CheckBedReply{IsVM: true}, "", bedRunOpts{KeepOnFailure: true}, disposableRoot(), teardown, stepErr)
	// ... and then the deferred owner runs, exactly as runCheckBed's defer does.
	if derr := bedVenueTeardown(state, bedRunOpts{KeepOnFailure: true}, disposableRoot(), teardown); derr != nil {
		t.Fatalf("bedVenueTeardown: %v", derr)
	}

	if !errors.Is(got, stepErr) {
		t.Errorf("returned error = %v, want the original step error %v", got, stepErr)
	}
	if calls != 0 {
		t.Fatalf("venue teardown calls = %d, want 0: --keep-on-failure must survive the deferred owner", calls)
	}
	if !strings.Contains(out.String(), "left running for debugging") {
		t.Errorf("retention notice missing although the venue was kept:\n%s", out.String())
	}
}

// TestBedFailedVenue_TearsDownADisposableRootByDefault is the regression: it fails on the
// pre-#75 behaviour, where the failure tail printed the retention notice and never ran the
// teardown. The teardown is not asserted to SUCCEED here — only to be ATTEMPTED, once — because
// the act is best-effort by contract.
func TestBedFailedVenue_TearsDownADisposableRootByDefault(t *testing.T) {
	stepErr := errors.New("check-live (charly check live check-r10-two-vm-fail) exited 2 after 41s")
	calls := 0
	var out bytes.Buffer

	got := bedFailedVenue(&out, "check-r10-two-vm-fail", spec.CheckBedReply{IsVM: true}, "", bedRunOpts{}, disposableRoot(), func() error {
		calls++
		return nil
	}, stepErr)

	if calls != 1 {
		t.Fatalf("venue teardown calls = %d, want 1: a disposable root must not survive a failed run", calls)
	}
	if !errors.Is(got, stepErr) {
		t.Errorf("returned error = %v, want the original step error %v", got, stepErr)
	}
	if strings.Contains(out.String(), "left running for debugging") {
		t.Errorf("retention notice printed although the venue was torn down:\n%s", out.String())
	}
}

// TestBedFailedVenue_RetentionIsExplicit keeps the debugging affordance the issue's thread insists
// must not be deleted: with the explicit flag the venue is kept AND the operator is told how to
// reach and destroy it.
func TestBedFailedVenue_RetentionIsExplicit(t *testing.T) {
	stepErr := errors.New("check-live exited 2")
	calls := 0
	var out bytes.Buffer

	got := bedFailedVenue(&out, "check-omarchy-desktop-vm", spec.CheckBedReply{
		IsVM:       true,
		VMTemplate: "omarchy-vm",
		BedDomain:  "check-omarchy-desktop-vm",
	}, "", bedRunOpts{KeepOnFailure: true}, disposableRoot(), func() error {
		calls++
		return nil
	}, stepErr)

	if calls != 0 {
		t.Fatalf("venue teardown calls = %d, want 0 under --keep-on-failure", calls)
	}
	if !errors.Is(got, stepErr) {
		t.Errorf("returned error = %v, want the original step error %v", got, stepErr)
	}
	if !strings.Contains(out.String(), "left running for debugging") {
		t.Errorf("--keep-on-failure must still tell the operator the venue is up:\n%s", out.String())
	}
}

// TestBedFailedVenue_TeardownFailureNeverReplacesTheStepError pins the property the issue's own
// scope names: on the failure path the teardown is best-effort, so a failed `vm destroy` is a
// warning and the ORIGINAL step error is what the caller sees.
func TestBedFailedVenue_TeardownFailureNeverReplacesTheStepError(t *testing.T) {
	stepErr := errors.New("check-live exited 2")
	teardownErr := errors.New("vm destroy charly-check-x: timed out")
	var out bytes.Buffer

	got := bedFailedVenue(&out, "check-x", spec.CheckBedReply{IsVM: true}, "", bedRunOpts{}, disposableRoot(), func() error {
		return teardownErr
	}, stepErr)

	if !errors.Is(got, stepErr) {
		t.Errorf("returned error = %v, want the original step error %v (a teardown failure must not mask it)", got, stepErr)
	}
	if errors.Is(got, teardownErr) {
		t.Errorf("returned error is the teardown error; the step error must win")
	}
	if !strings.Contains(out.String(), teardownErr.Error()) {
		t.Errorf("the swallowed teardown failure must still be reported as a warning:\n%s", out.String())
	}
}

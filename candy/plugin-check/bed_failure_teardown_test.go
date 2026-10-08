package check

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// The venue half of the exit-path guarantee (opencharly/plugin-check#75): a bed that dies mid-step
// must dispose of its deployed target, and retention must be an explicit opt-in that is scoped to
// the path that asked for it. Before this change the failure tail had only the retention notice, so
// an aborted bed left a live disposable target — for a VM bed a whole booted guest holding vCPUs and
// RAM — until some later run of the SAME bed happened to reclaim it.

func disposableRoot() spec.DeployNode {
	yes := true
	return spec.DeployNode{Disposable: &yes}
}

// TestKeepVenueOnSuccess pins the SUCCESS tail's decision: it is the PRE-CHANGE gate exactly
// (`opts.Keep`), so a passing run disposes of its venue even under `--keep-on-failure` — no
// success-path behaviour changed by this PR (round-5 block). The predicate takes no root by
// construction, so the root-scoped arms (`ephemeral.keep_on_failure:`, `disposable:`) belong to the
// FAILURE path and are pinned there, in TestKeepVenueOnFailure and in the two cross-checks below.
func TestKeepVenueOnSuccess(t *testing.T) {
	cases := []struct {
		name string
		opts bedRunOpts
		want bool
	}{
		{"no flag → dispose", bedRunOpts{}, false},
		{"--keep → keep", bedRunOpts{Keep: true}, true},
		{"keep_venue: policy forces Keep → keep", bedRunOpts{Keep: true, KeepVenue: true}, true},
		{"--keep-on-failure alone → NOT kept on a passing run", bedRunOpts{KeepOnFailure: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := keepVenueOnSuccess(tc.opts); got != tc.want {
				t.Errorf("keepVenueOnSuccess() = %v, want %v", got, tc.want)
			}
		})
	}
	// The disposability arm belongs to the FAILURE predicate only, so a non-disposable root keeps
	// its pre-change SUCCESS behaviour (dispose) while the failure path never destroys it.
	if keepVenueOnSuccess(bedRunOpts{}) != false {
		t.Error("success tail must dispose for a non-disposable root exactly as before this change")
	}
	if !keepVenueOnFailure(bedRunOpts{}, spec.DeployNode{}) {
		t.Error("failure path must keep a non-disposable root (Disposable-Only Autonomy)")
	}
	// The root's own ephemeral arm belongs to the failure path: the same request that is ignored by
	// the success gate keeps the venue when the run fails.
	keepOnFailureRoot := disposableRoot()
	keepOnFailureRoot.Ephemeral = &spec.EphemeralLifetime{KeepOnFailure: true}
	if keepVenueOnSuccess(bedRunOpts{}) {
		t.Error("root-scoped retention must not leak into the success gate")
	}
	if !keepVenueOnFailure(bedRunOpts{}, keepOnFailureRoot) {
		t.Error("root's ephemeral.keep_on_failure must keep the venue on the FAILURE path")
	}
}

// TestKeepVenueOnFailure pins the FAILURE-scoped decision: the same table, plus the two requests
// that only ever mean "let me inspect a failure".
func TestKeepVenueOnFailure(t *testing.T) {
	ephemeral := spec.DeployNode{Ephemeral: &spec.EphemeralLifetime{KeepOnFailure: true}}
	cases := []struct {
		name string
		opts bedRunOpts
		root spec.DeployNode
		want bool
	}{
		{"disposable root, no flag → dispose", bedRunOpts{}, disposableRoot(), false},
		{"--keep-on-failure → keep", bedRunOpts{KeepOnFailure: true}, disposableRoot(), true},
		{"--keep → keep", bedRunOpts{Keep: true}, disposableRoot(), true},
		{"ephemeral.keep_on_failure → keep", bedRunOpts{}, ephemeral, true},
		{"root not marked disposable → keep", bedRunOpts{}, spec.DeployNode{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := keepVenueOnFailure(tc.opts, tc.root); got != tc.want {
				t.Errorf("keepVenueOnFailure() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestBedVenueTeardown_EnforcesTheCallersDecision pins the ONE owner: it destroys exactly when the
// caller's path policy said so, once, and never before the bed had a target.
func TestBedVenueTeardown_EnforcesTheCallersDecision(t *testing.T) {
	cases := []struct {
		name     string
		state    venueTeardownState
		keep     bool
		wantCall int
	}{
		{"not kept and deployed → dispose", venueTeardownState{deployed: true}, false, 1},
		{"kept → no destroy", venueTeardownState{deployed: true}, true, 0},
		{"never deployed → nothing to do", venueTeardownState{}, false, 0},
		{"already torn down → no second destroy (R4)", venueTeardownState{deployed: true, tornDown: true}, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			state := tc.state
			if err := bedVenueTeardown(&state, tc.keep, func() error {
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

// TestBedVenueTeardown_SuccessTailDisposesUnderKeepOnFailure is the round-2 block: the SUCCESS tail
// reads keepVenueAlways, so a run that PASSED with `--keep-on-failure` still disposes of its venue —
// the flag is failure-scoped, as its own help says.
func TestBedVenueTeardown_SuccessTailDisposesUnderKeepOnFailure(t *testing.T) {
	opts := bedRunOpts{KeepOnFailure: true}
	state := &venueTeardownState{deployed: true}
	calls := 0

	// Exactly the success tail's call: bedVenueTeardown(venue, keepVenueOnSuccess(opts), cleanup).
	if err := bedVenueTeardown(state, keepVenueOnSuccess(opts), func() error {
		calls++
		return nil
	}); err != nil {
		t.Fatalf("bedVenueTeardown: %v", err)
	}
	if calls != 1 {
		t.Fatalf("venue teardown calls = %d, want 1: --keep-on-failure keeps a FAILED run's venue, never a passing run's", calls)
	}
	// ... and the same flag on the FAILURE path keeps it.
	if err := bedVenueTeardown(&venueTeardownState{deployed: true}, keepVenueOnFailure(opts, disposableRoot()), func() error {
		calls++
		return nil
	}); err != nil {
		t.Fatalf("bedVenueTeardown: %v", err)
	}
	if calls != 1 {
		t.Fatalf("venue teardown calls = %d, want 1: the failure path must keep the venue under --keep-on-failure", calls)
	}
}

// TestBedFailedVenue_ThenTheOwnerRefuses is the unit-level reproduction of the full failing path:
// the failure tail keeps the venue under --keep-on-failure, and the every-unwinding-path defer that
// runs after it (same failure predicate, same state) must NOT destroy it.
func TestBedFailedVenue_ThenTheOwnerRefuses(t *testing.T) {
	stepErr := errors.New("check-live exited 2")
	var out bytes.Buffer
	state := &venueTeardownState{deployed: true}
	opts := bedRunOpts{KeepOnFailure: true}
	calls := 0
	teardown := func() error { calls++; return nil }
	// The failure path's owner call, as runCheckBed wires it.
	failOwner := func() error { return bedVenueTeardown(state, keepVenueOnFailure(opts, disposableRoot()), teardown) }

	got := bedFailedVenue(&out, "check-x", spec.CheckBedReply{IsVM: true}, "", opts, disposableRoot(), failOwner, stepErr)
	// ... and then the deferred owner runs, exactly as runCheckBed's defer does.
	if derr := failOwner(); derr != nil {
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

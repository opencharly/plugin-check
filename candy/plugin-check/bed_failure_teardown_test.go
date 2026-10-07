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

// TestRetainVenueOnFailure pins the DECISION: which failures keep the venue, and which dispose of
// it. Kept separate from the act below so the policy is readable as a table.
func TestRetainVenueOnFailure(t *testing.T) {
	ephemeral := spec.DeployNode{Ephemeral: &spec.EphemeralLifetime{KeepOnFailure: true}}
	ordinary := spec.DeployNode{}
	cases := []struct {
		name string
		opts bedRunOpts
		root spec.DeployNode
		want bool
	}{
		{"disposable root, no retention flag → dispose", bedRunOpts{}, disposableRoot(), false},
		{"--keep-on-failure → retain", bedRunOpts{KeepOnFailure: true}, disposableRoot(), true},
		{"--keep (already meant 'do not tear down') → retain", bedRunOpts{Keep: true}, disposableRoot(), true},
		{"keep_venue: policy forces Keep → retain", bedRunOpts{Keep: true, KeepVenue: true}, disposableRoot(), true},
		{"ephemeral.keep_on_failure (the score path's own field) → retain", bedRunOpts{}, ephemeral, true},
		{"root not marked disposable is never destroyed autonomously → retain", bedRunOpts{}, ordinary, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := retainVenueOnFailure(tc.opts, tc.root); got != tc.want {
				t.Errorf("retainVenueOnFailure() = %v, want %v", got, tc.want)
			}
		})
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

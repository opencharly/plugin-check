package check

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestCandyAddStepsIncludesRemoteRefs is the regression test for a bed's `add_candy:`
// silently dropping the candy's baked check: steps when the candy is pinned by @github ref.
//
// A remote ref is the NORMAL way a bed pins a candy since the de-submodule cutover, and the
// old code skipped it outright (`if IsRemoteCandyRef(ref) { continue }`). The bed then ran
// only its own plan steps — and passed, asserting almost nothing, which is worse than
// failing. rp.CandyModels keys a fetched candy by its BareRef, so the local and remote
// lookups are the same lookup.
func TestCandyAddStepsIncludesRemoteRefs(t *testing.T) {
	// Only a check: step. A run: step would route BakeableSteps through
	// spec.OpInContext, which needs the verb catalog the real binary initialises — and
	// the bakeable-vs-not split is not what this test is about.
	plan := []spec.Step{
		{Check: "the candy's own baked assertion", Op: spec.Op{Command: "true"}},
	}
	rp := &spec.ResolvedProject{CandyModels: map[string]spec.CandyModel{
		// Exactly how a FETCHED candy is keyed: no @ prefix, no :version.
		"github.com/opencharly/layer-probe": {Plan: plan},
		"local-probe":                       {Plan: plan},
	}}

	for name, ref := range map[string]string{
		"remote @github ref": "@github.com/opencharly/layer-probe:v2026.242.1200",
		"local bare name":    "local-probe",
	} {
		got := candyAddSteps(rp, []string{ref})
		if len(got) == 0 {
			t.Errorf("%s (%q): no steps collected — the candy's baked checks were dropped", name, ref)
			continue
		}
		found := false
		for _, s := range got {
			if s.Check == "the candy's own baked assertion" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s (%q): collected %d step(s) but not the candy's check", name, ref, len(got))
		}
	}
}

// TestCandyAddStepsUnknownRefIsSkipped — a ref with no scanned candy must not panic or
// invent steps; it is simply contributed nothing.
func TestCandyAddStepsUnknownRefIsSkipped(t *testing.T) {
	rp := &spec.ResolvedProject{CandyModels: map[string]spec.CandyModel{}}
	if got := candyAddSteps(rp, []string{"@github.com/opencharly/layer-absent:v1.2.3", "nope"}); len(got) != 0 {
		t.Fatalf("unknown refs contributed %d step(s), want 0", len(got))
	}
}

package check

import (
	"strings"
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
		got, err := candyAddSteps(rp, []string{ref})
		if err != nil {
			t.Errorf("%s (%q): unexpected error: %v", name, ref, err)
			continue
		}
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
	got, err := candyAddSteps(rp, []string{"@github.com/opencharly/layer-absent:v1.2.3", "nope"})
	if err != nil {
		t.Fatalf("unknown refs are contributed nothing, not an error; got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("unknown refs contributed %d step(s), want 0", len(got))
	}
}

// TestCandyAddStepsRejectsAPinTheModelCannotHonour — the VERSION GUARD
// (opencharly/plugin-check#112).
//
// rp.CandyModels is keyed by candy IDENTITY (BareRef), so when the same repo is pinned at two
// coordinates in one project the scan's arbiter keeps ONE winner and this lookup returns it
// whatever the deploy pinned. Before the guard, that silently ran the OTHER version's plan: the
// measured case is `distro-arch`'s bed pinning layer-direnv:v2026.287.2155 while
// layer-agent-forwarding transitively pins :v2026.272.0057, and the child `check live` ran the
// old 9-step plan (no `context:` steps) while the fetched artifact carried the pinned one.
//
// The stamp is what makes this detectable: sdk/loaderkit's scan writes the winning source git
// tag onto the model (`ws.Model.Version = winner.GitTag`), so a model whose Version differs from
// the ref's is provably from another coordinate.
func TestCandyAddStepsRejectsAPinTheModelCannotHonour(t *testing.T) {
	plan := []spec.Step{
		{Check: "the candy's own baked assertion", Op: spec.Op{Command: "true"}},
	}
	rp := &spec.ResolvedProject{CandyModels: map[string]spec.CandyModel{
		"github.com/opencharly/layer-probe": {Plan: plan, Version: "v2026.272.0057"},
	}}
	got, err := candyAddSteps(rp, []string{"@github.com/opencharly/layer-probe:v2026.287.2155"})
	if err == nil {
		t.Fatalf("a pinned ref the resolution cannot honour must FAIL LOUDLY, not resolve to another version's plan; got %d step(s) and no error", len(got))
	}
	for _, want := range []string{"v2026.287.2155", "v2026.272.0057", "github.com/opencharly/layer-probe", "plugin-check#112"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error must name %q so the collision is diagnosable; got: %v", want, err)
		}
	}
	if len(got) != 0 {
		t.Errorf("no steps may be contributed from an unresolvable pin; got %d", len(got))
	}
}

// TestCandyAddStepsToleratesAnUnpinnedRefAndAnUnstampedModel — the guard's two documented
// boundaries: a version-less ref has no pin to honour (identity lookup is correct), and an
// UNSTAMPED model is the deliberate local-shadow case (sdk/loaderkit gives a local candy of the
// same name precedence, as INFO, and a local candy carries no git tag).
func TestCandyAddStepsToleratesAnUnpinnedRefAndAnUnstampedModel(t *testing.T) {
	plan := []spec.Step{
		{Check: "the candy's own baked assertion", Op: spec.Op{Command: "true"}},
	}
	rp := &spec.ResolvedProject{CandyModels: map[string]spec.CandyModel{
		"github.com/opencharly/layer-stamped":  {Plan: plan, Version: "v2026.287.2155"},
		"github.com/opencharly/layer-shadowed": {Plan: plan},
	}}
	for name, ref := range map[string]string{
		"version-less ref, stamped model": "@github.com/opencharly/layer-stamped",
		"pinned ref, unstamped model":     "@github.com/opencharly/layer-shadowed:v2026.287.2155",
		"pinned ref, matching model":      "@github.com/opencharly/layer-stamped:v2026.287.2155",
	} {
		got, err := candyAddSteps(rp, []string{ref})
		if err != nil {
			t.Errorf("%s (%q): must not be gated; got %v", name, ref, err)
			continue
		}
		if len(got) == 0 {
			t.Errorf("%s (%q): no steps collected", name, ref)
		}
	}
}

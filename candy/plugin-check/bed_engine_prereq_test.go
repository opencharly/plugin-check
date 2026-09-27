package check

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestBedEnginePrereqSkip is the R7a gate: a bed pinning a concrete container engine whose CLI
// is absent must be a clean prereq SKIP (never a deploy-add failure), while a bed with no engine
// (or a present engine) must NOT skip. Hermetic — it drives LookPath with a real present binary
// and a guaranteed-absent name, so it needs no project load and no network.
func TestBedEnginePrereqSkip(t *testing.T) {
	// No engine declared → never skipped (no engine requirement).
	if got := bedEnginePrereqSkip(spec.Deploy{}); got != nil {
		t.Fatalf("no engine: expected nil skip, got %+v", got)
	}

	// A present binary (the test runner's own shell) → not skipped.
	if got := bedEnginePrereqSkip(spec.Deploy{Engine: spec.EngineName("sh")}); got != nil {
		t.Fatalf("present engine: expected nil skip, got %+v", got)
	}

	// A guaranteed-absent engine → a skip carrying the engine token + a reason naming it.
	got := bedEnginePrereqSkip(spec.Deploy{Engine: spec.EngineName("no-such-engine-xyz")})
	if got == nil {
		t.Fatal("absent engine: expected a prereq skip, got nil")
	}
	if got.Token != "engine" || got.Vendor != "no-such-engine-xyz" {
		t.Fatalf("absent engine: token/vendor = %q/%q, want engine/no-such-engine-xyz", got.Token, got.Vendor)
	}
	if got.Reason == "" {
		t.Fatal("absent engine: empty reason")
	}
}

// TestPrereqSkipStepName pins the summary step names: the GPU gate keeps its documented
// `prereq-gpu-skipped`, the engine gate records `prereq-engine-skipped`, and both are members
// of the documented `prereq-*-skipped` family.
func TestPrereqSkipStepName(t *testing.T) {
	if got := prereqSkipStepName("engine"); got != "prereq-engine-skipped" {
		t.Fatalf("engine step name = %q, want prereq-engine-skipped", got)
	}
	if got := prereqSkipStepName("gpu:nvidia"); got != "prereq-gpu-skipped" {
		t.Fatalf("gpu step name = %q, want prereq-gpu-skipped", got)
	}
}

package check

import (
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// TestWrapStepsFileSet proves the steps-file override replaces the baked-plan
// set per-arm (pod/vm/local/group all route through the one canonical helper,
// R3). Injected steps -> a Deploy-only steps-file set; none -> the set unchanged.
func TestWrapStepsFileSet(t *testing.T) {
	baked := &kit.LabelDescriptionSet{
		Candy:  []kit.LabeledDescription{{Origin: "baked-candy", Plan: []spec.Step{{Op: spec.Op{ID: "baked"}}}}},
		Deploy: []kit.LabeledDescription{{Origin: "baked-deploy", Plan: []spec.Step{{Op: spec.Op{ID: "baked"}}}}},
	}

	// No injection: unchanged (non-nil, same pointer).
	if got := wrapStepsFileSet(baked, nil, "vm:x"); got != baked {
		t.Fatal("no-injection case must return the baked set unchanged")
	}

	// Injection: Deploy-only steps-file set with exactly the injected steps.
	injected := []spec.Step{{Op: spec.Op{ID: "record-wrap-start"}}}
	got := wrapStepsFileSet(baked, injected, "vm:x")
	if got == baked {
		t.Fatal("injection case must return a new set")
	}
	if len(got.Candy)+len(got.Box) != 0 {
		t.Fatal("injected set must carry no candy/box sections")
	}
	if len(got.Deploy) != 1 || got.Deploy[0].Origin != "steps-file" || got.Deploy[0].Plan[0].Op.ID != "record-wrap-start" {
		t.Fatalf("steps-file set wrong: %+v", got.Deploy)
	}

	// Empty injected slice: unchanged.
	if got := wrapStepsFileSet(baked, []spec.Step{}, "vm:x"); got != baked {
		t.Fatal("empty slice must return the baked set unchanged")
	}
}

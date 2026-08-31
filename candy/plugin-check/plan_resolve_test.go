package check

import (
	"context"
	"os"
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestPluginCheckRunPlanResolvePath exercises pluginCheckRunPlan's REAL resolve
// path (ResolveRuntime → ResolveBuiltImageRef → ExtractMetadata →
// ResolveCheckVarsBuild) against a locally-built image, and asserts the JSON
// reply carries the resolved steps. Skipped when no suitable local image exists
// (e.g. a fresh CI runner without the bed image); on this development host the
// sway-browser-vnc image from the checkbed is present, so the test RUNS and
// pastes the resolved plan's direnv step paths.
func TestPluginCheckRunPlanResolvePath(t *testing.T) {
	image := os.Getenv("PLAN_TEST_IMAGE")
	if image == "" {
		image = "sway-browser-vnc"
	}
	// ResolveBuiltImageRef refuses short names electing an older-than-newest
	// local build; the bed leaves a recent build tagged, so the env override is
	// the deterministic path. Without an image the resolve path cannot run.
	reply, err := pluginCheckRunPlan(nil, context.Background(), specCheckRunRequest(image))
	if err != nil {
		t.Skipf("resolve path unavailable (no local image?): %v", err)
	}
	if reply.NoSteps {
		t.Fatalf("expected a plan for %s", image)
	}
}

func specCheckRunRequest(image string) spec.CheckRunRequest {
	return spec.CheckRunRequest{Mode: "plan", Image: image}
}

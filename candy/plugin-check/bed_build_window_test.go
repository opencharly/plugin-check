package check

import (
	"os"
	"strings"
	"testing"

	"github.com/opencharly/spec/poll"
)

// TestBuildStepEnvSizesTheChildGuardWindow pins the #123 fix: the generated BUILD steps
// carry a build-sized no_progress window to the child through the cli seam's per-call env,
// and that window RESOLVES with the child's other readiness defaults — an override the
// child rejects would silently fall back to the 90s probe window, which is the defect
// itself (the child's idle guard then reads an IO-bound `podman load`/cold export as a
// wedged call and SIGKILLs it).
func TestBuildStepEnvSizesTheChildGuardWindow(t *testing.T) {
	env := buildStepEnv()
	got := env["CHARLY_READINESS_NO_PROGRESS"]
	if got != buildStepNoProgress {
		t.Fatalf("buildStepEnv()[CHARLY_READINESS_NO_PROGRESS] = %q, want %q", got, buildStepNoProgress)
	}

	// The env name and value must be exactly what the child's resolver reads, and the child
	// must not reject the override (ordering: every interval <= no_progress <= absolute_cap).
	t.Setenv("CHARLY_READINESS_NO_PROGRESS", got)
	rr, err := poll.ResolveReadiness(nil)
	if err != nil {
		t.Fatalf("the build step's window does not resolve with the child's readiness defaults: %v", err)
	}
	if rr.NoProgress <= poll.ReadinessNoProgressFallback {
		t.Fatalf("the build step's window resolves to %s, which does not exceed the probe fallback %s",
			rr.NoProgress, poll.ReadinessNoProgressFallback)
	}
}

// TestEveryBuildStepCarriesTheBuildWindow pins the seam across EVERY build call site.
// #123 fixed the ROOT image-build step; #125 found the member image-build-<member>,
// the member and root vm-build, and the update-image-<member> rebuild still inherited
// the 120s probe bound. A build launched with the bare `step` helper can be SIGKILLed
// by the child's idle guard exactly as before, so this test fails if any build call
// site stops using stepEnv(..., buildStepEnv(), ...).
func TestEveryBuildStepCarriesTheBuildWindow(t *testing.T) {
	src, err := os.ReadFile("bed_run.go")
	if err != nil {
		t.Fatalf("reading bed_run.go: %v", err)
	}
	body := string(src)

	forbidden := []string{
		`step("image-build`,   // root + member image builds
		`step("vm-build`,      // root + member VM builds
		`step("update-image-`, // the fresh-rebuild gate's member rebuild
	}
	for _, pat := range forbidden {
		if strings.Contains(body, pat) {
			t.Errorf("build call site uses the bare step helper: %s — every build must go through stepEnv(..., buildStepEnv(), ...) so it carries the build-sized window", pat)
		}
	}

	required := []string{
		`stepEnv("image-build-`,  // member image build
		`stepEnv("image-build",`, // root image build
		`stepEnv("vm-build-`,     // member VM build
		`stepEnv("vm-build",`,    // root VM build
		`stepEnv("update-image-`, // member rebuild at the update gate
	}
	for _, pat := range required {
		if !strings.Contains(body, pat) {
			t.Errorf("missing the build-window seam at %s", pat)
		}
	}
}

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
// by the child's idle guard exactly as before, and a site that passes stepEnv with any
// OTHER env drops the build-sized window just as silently. This test reads bed_run.go,
// finds every line that starts a build (image-build / vm-build / update-image), and
// requires BOTH stepEnv and buildStepEnv() on it; it also pins the site count so a new
// build site cannot be added without the seam.
func TestEveryBuildStepCarriesTheBuildWindow(t *testing.T) {
	src, err := os.ReadFile("bed_run.go")
	if err != nil {
		t.Fatalf("reading bed_run.go: %v", err)
	}
	sites := 0
	for i, line := range strings.Split(string(src), "\n") {
		if !strings.Contains(line, "step") {
			continue
		}
		isBuild := strings.Contains(line, `"image-build`) ||
			strings.Contains(line, `"vm-build`) ||
			strings.Contains(line, `"update-image-`)
		if !isBuild {
			continue
		}
		sites++
		if !strings.Contains(line, "stepEnv(") {
			t.Errorf("bed_run.go:%d launches a build with the bare step helper: %s", i+1, strings.TrimSpace(line))
			continue
		}
		if !strings.Contains(line, "buildStepEnv()") {
			t.Errorf("bed_run.go:%d launches a build without buildStepEnv(): %s", i+1, strings.TrimSpace(line))
		}
	}
	// The root image-build, the member image-build, the member vm-build, the root
	// vm-build and the update-image rebuild.
	if want := 5; sites != want {
		t.Errorf("found %d build call sites, want %d — a new build site needs the stepEnv(..., buildStepEnv(), ...) seam and this count updated", sites, want)
	}
}

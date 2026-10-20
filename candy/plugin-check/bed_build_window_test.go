package check

import (
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

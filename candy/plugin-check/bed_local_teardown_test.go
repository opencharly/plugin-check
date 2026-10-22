package check

import (
	"os"
	"strings"
	"testing"
)

// TestLocalBedTeardownNeverUsesTheContainerVerb is the regression guard for
// opencharly/plugin-check#129. A kind:local (`host: local`) bed was torn down with the
// CONTAINER verb `charly remove --purge` in BOTH cleanup switches, because each switch
// tested d.IsExternal alone and let d.IsLocal fall through into the pod arm. The arm is
// reached only after every authored assertion has passed, so a host venue with no
// container engine (a kind:local venue's normal shape) could never pass a bed — the
// run ended FAIL (steps=N) with `no container engine found (install podman or docker)`
// at the LAST step.
//
// The test reads bed_run.go and requires the ONE teardown decision to own both halves:
//   - no switch may test d.IsExternal alone (the fall-through this defect is made of);
//   - the pre-run switch and the final cleanup switch must both call
//     bedRootTeardownArgs(d, name, isKubeVirt);
//   - the failure-path retention hint for a local bed must print `charly deploy del`,
//     the verb the cleanup path itself issues, never `charly remove`.
//
// It is a source-structure test on purpose: the two switches live inside the bed-run
// closure and are only reachable after a full deploy, so a unit test cannot walk them
// without a venue. The behavioural half of the same predicate is pinned in
// bed_root_teardown_test.go.
func TestLocalBedTeardownNeverUsesTheContainerVerb(t *testing.T) {
	src, err := os.ReadFile("bed_run.go")
	if err != nil {
		t.Fatalf("reading bed_run.go: %v", err)
	}
	text := string(src)

	preRunCalls, cleanupCalls := 0, 0
	for i, line := range strings.Split(text, "\n") {
		// The defect's signature: an in-place arm guarded by IsExternal ONLY, so a
		// local bed falls through to the container arm below it.
		if strings.Contains(line, "if d.IsExternal {") {
			t.Errorf("bed_run.go:%d tests IsExternal alone — a local bed falls through to the container arm: %s",
				i+1, strings.TrimSpace(line))
		}
		if strings.Contains(line, "bestEffort(bedRootTeardownArgs(d, name, isKubeVirt)...)") {
			preRunCalls++
		}
		if strings.Contains(line, `step("cleanup", bedRootTeardownArgs(d, name, isKubeVirt)...)`) {
			cleanupCalls++
		}
	}
	if preRunCalls != 1 {
		t.Errorf("pre-run cleanup routes through bedRootTeardownArgs at %d sites, want 1 — the pre-run and final teardown decisions must be the SAME helper", preRunCalls)
	}
	if cleanupCalls != 1 {
		t.Errorf("final cleanup routes through bedRootTeardownArgs at %d sites, want 1 — the pre-run and final teardown decisions must be the SAME helper", cleanupCalls)
	}

	hint := localRetentionHint(text)
	if hint == "" {
		t.Fatal("could not locate the local retention hint in bed_run.go")
	}
	if !strings.Contains(hint, "charly deploy del") {
		t.Errorf("local retention hint does not print the deploy verb:\n%s", hint)
	}
	if strings.Contains(hint, "charly remove") {
		t.Errorf("local retention hint still prints the container verb `charly remove`:\n%s", hint)
	}
}

// localRetentionHint returns the `case d.IsLocal:` block of printDebugRetentionNotice —
// the operator-facing hint printed when a FAILED local bed's apply is left in place.
func localRetentionHint(src string) string {
	const marker = "local apply left in place for debugging"
	i := strings.Index(src, marker)
	if i < 0 {
		return ""
	}
	start := strings.LastIndex(src[:i], "case d.IsLocal:")
	end := strings.Index(src[i:], "case d.IsExternal:")
	if start < 0 || end < 0 {
		return ""
	}
	return src[start : i+end]
}

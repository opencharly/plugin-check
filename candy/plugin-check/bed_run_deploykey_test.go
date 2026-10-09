package check

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// deployKeyForBed maps a bed's roster address to the deploy key the deploy phase creates.
// Without this mapping a namespaced bed's config/start phases ask `charly config` for a name it
// cannot resolve, and the step fails after a bogus build attempt (see the helper's own comment).
func TestDeployKeyForBed(t *testing.T) {
	for _, tc := range []struct {
		name string
		bed  string
		want string
	}{
		// The defect this exists to prevent: a namespaced bed is addressed `<ns>.<name>` by the
		// roster while its deploy is keyed `<ns>/<name>`.
		{"namespaced bed", "openclaw.check-openclaw-pod", "openclaw/check-openclaw-pod"},
		// A local bed with no namespace must be returned UNCHANGED — the same contract
		// ResolveBedForRoot documents for the build phase ("a local bed is returned unchanged").
		{"local bed", "check-mise-ubuntu-2604", "check-mise-ubuntu-2604"},
		// A nested child is itself a dotted path; only the FIRST dot is the namespace boundary,
		// and spec.DeployKey keeps the remainder intact as the instance.
		{"nested child", "openclaw.check-openclaw-pod.child", "openclaw/check-openclaw-pod.child"},
		// Degenerate inputs must not invent a separator.
		{"leading dot", ".check-x", ".check-x"},
		{"trailing dot", "openclaw.", "openclaw."},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := deployKeyForBed(tc.bed); got != tc.want {
				t.Fatalf("deployKeyForBed(%q) = %q, want %q", tc.bed, got, tc.want)
			}
		})
	}
}

// TestDeployKeyForBed_ContractWithSpec asserts the RESULT against spec.DeployKey's own two
// functions, using literals on BOTH sides.
//
// An earlier revision of this test compared deployKeyForBed(ns+"."+rest) with
// deployKeyForBed(bed) — the helper against itself, restating the split and never touching
// spec.DeployKey. That test could not fail for the right reason (R7: a test that cannot fail is
// invalid). This one fails if the helper's output stops being a key spec.DeployKey would build,
// or stops round-tripping through spec.ParseDeployKey.
func TestDeployKeyForBed_ContractWithSpec(t *testing.T) {
	const (
		bed = "openclaw.check-openclaw-pod"
		ns  = "openclaw"
		rest = "check-openclaw-pod"
	)

	got := deployKeyForBed(bed)

	// 1. The literal key form, spelled out rather than derived.
	if want := "openclaw/check-openclaw-pod"; got != want {
		t.Fatalf("deployKeyForBed(%q) = %q, want the literal deploy key %q", bed, got, want)
	}

	// 2. It must equal what spec.DeployKey builds from the namespace and the remainder —
	//    with both arguments literals, so the helper is not on the right-hand side.
	if want := spec.DeployKey("openclaw", "check-openclaw-pod"); got != want {
		t.Fatalf("deployKeyForBed(%q) = %q, but spec.DeployKey(%q, %q) = %q",
			bed, got, ns, rest, want)
	}

	// 3. And it must round-trip: spec.ParseDeployKey must recover the namespace and instance
	//    the roster address carried. If the separator or the split changes, this fails.
	gotNS, gotRest := spec.ParseDeployKey(got)
	if gotNS != ns || gotRest != rest {
		t.Fatalf("spec.ParseDeployKey(deployKeyForBed(%q)) = (%q, %q), want (%q, %q)",
			bed, gotNS, gotRest, ns, rest)
	}

	// 4. A local bed must still be untouched by all of the above.
	if got := deployKeyForBed("check-mise-ubuntu-2604"); got != "check-mise-ubuntu-2604" {
		t.Fatalf("a local bed must be returned unchanged, got %q", got)
	}
}

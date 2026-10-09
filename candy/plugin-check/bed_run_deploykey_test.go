package check

import "testing"

// deployKeyForBed maps a bed's roster address to the deploy key the deploy phase creates.
// Without this mapping a namespaced bed's config/start phases ask `charly config` for a name it
// cannot resolve, and the step fails after a bogus build attempt (see the helper's own comment,
// and opencharly/plugin-check — W23).
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

// The mapping must agree with spec.DeployKey on the namespace split — the single owner of the
// `<box>/<instance>` key form. If spec.DeployKey's contract changes, this fails rather than
// silently drifting.
func TestDeployKeyForBedAgreesWithSpecDeployKey(t *testing.T) {
	const bed = "openclaw.check-openclaw-pod"
	ns, rest := "openclaw", "check-openclaw-pod"
	if got, want := deployKeyForBed(bed), deployKeyForBed(ns+"."+rest); got != want {
		t.Fatalf("namespace split is not stable: %q vs %q", got, want)
	}
	if got := deployKeyForBed(bed); got != "openclaw/check-openclaw-pod" {
		t.Fatalf("deployKeyForBed(%q) = %q — must equal the spec.DeployKey form", bed, got)
	}
}

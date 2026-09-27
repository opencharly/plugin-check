package check

import (
	"testing"
)

// #75 — bed-scoped fixture image tags (relocated from charly/bed_run_image_tag_test.go, #55 W3
// B2-full). This unit test pins the pure formula; the kubernetes-preresolver node.Version honoring + the
// plugin's per-step --tag threading are integration-proven by the concurrent check-sidecar-pod +
// check-k8s-deploy bed run (the R10 gate).

// TestBedRunImageTag proves the per-RUN bed-scoped tag is <bed>-<calver> and that
// distinct beds/runs yield distinct tags (the collision-free-by-construction
// property). Would FAIL if the formula regressed to a shared/constant tag.
func TestBedRunImageTag(t *testing.T) {
	cases := []struct {
		bed, calver, want string
	}{
		{"check-sidecar-pod", "2026.195.0600", "check-sidecar-pod-2026.195.0600"},
		{"check-k8s-deploy", "2026.195.0600", "check-k8s-deploy-2026.195.0600"},
		{"check-sidecar-pod", "2026.195.0700", "check-sidecar-pod-2026.195.0700"},
		{"", "2026.195.0600", ""}, // no bed → empty (no --tag threaded)
		{"check-x", "", ""},       // no calver → empty
	}
	for _, c := range cases {
		if got := bedRunImageTag(c.bed, c.calver); got != c.want {
			t.Errorf("bedRunImageTag(%q, %q) = %q, want %q", c.bed, c.calver, got, c.want)
		}
	}
	// Two DISTINCT beds at the SAME calver must never collide on the tag string —
	// this is the whole point of #75 (the falsified pre-fix claim was that they were
	// collision-free even sharing a fixture image name).
	a := bedRunImageTag("check-sidecar-pod", "2026.195.0600")
	b := bedRunImageTag("check-k8s-deploy", "2026.195.0600")
	if a == b {
		t.Fatalf("distinct beds produced the SAME bed-scoped tag %q — the #75 collision is not prevented", a)
	}
}

// TestBedBuildsImage pins the image-build predicate. A bed carrying a workload image
// MUST build it before `deploy add` — including an EXTERNAL-IN-PLACE deploy (the
// `kindcluster` substrate's deploy pins `<image>:<deploy>-<calver>` and applies it, so
// the pre-fix guard that excluded external-in-place made the preresolve fail with
// "pinned image … not present in local storage"). A kind:local deploy (no image) and a
// VM/kubevirt substrate (an image-backed CR) must not build. Would FAIL if the
// predicate regressed to excluding external-in-place.
func TestBedBuildsImage(t *testing.T) {
	cases := []struct {
		name                      string
		isVM, isKubeVirt, isLocal bool
		image                     string
		want                      bool
	}{
		{"pod bed with image", false, false, false, "app", true},
		{"external-in-place workload image (kindcluster)", false, false, false, "check-k8s-deploy-app", true},
		{"kind:local (no image)", false, false, true, "", false},
		{"vm bed", true, false, false, "app", false},
		{"kubevirt bed", false, true, false, "app", false},
		{"pod bed without image", false, false, false, "", false},
	}
	for _, c := range cases {
		if got := bedBuildsImage(c.isVM, c.isKubeVirt, c.isLocal, c.image); got != c.want {
			t.Errorf("%s: bedBuildsImage = %v, want %v", c.name, got, c.want)
		}
	}
}

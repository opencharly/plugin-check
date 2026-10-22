package check

import (
	"reflect"
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestBedRootTeardownArgsForEveryVenue pins the ONE root-teardown decision both cleanup
// switches read (opencharly/plugin-check#129). An in-place venue — kind:local (host-rooted)
// or external-in-place — has no container lifecycle, so it must be torn down with the
// deploy-tree verb `charly deploy del` and must NEVER reach the container verb
// `charly remove`. A kubevirt root is also `deploy del` (its plugin owns the CR teardown);
// only a container pod keeps `remove --purge` (the sole path to purgeDeployArtifacts).
//
// The mutant this kills: restoring the pre-fix predicate (an arm that tests IsExternal
// only) makes the local row produce `["remove", …]` and fails the test.
func TestBedRootTeardownArgsForEveryVenue(t *testing.T) {
	const name = "check-k3s-shared-containerd"
	cases := []struct {
		name     string
		d        spec.CheckBedReply
		kubevirt bool
		want     []string
	}{
		{"kind:local host venue → deploy del", spec.CheckBedReply{IsLocal: true}, false, []string{"deploy", "del", name}},
		{"external in-place → deploy del", spec.CheckBedReply{IsExternal: true}, false, []string{"deploy", "del", name}},
		{"kubevirt root → deploy del", spec.CheckBedReply{}, true, []string{"deploy", "del", name}},
		{"container pod → remove --purge", spec.CheckBedReply{}, false, []string{"remove", name, "--purge"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := bedRootTeardownArgs(tc.d, name, tc.kubevirt)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("bedRootTeardownArgs(%+v, kubevirt=%v) = %v, want %v", tc.d, tc.kubevirt, got, tc.want)
			}
			// The defect's user-visible symptom: a local bed invoking the container verb.
			if (tc.d.IsLocal || tc.d.IsExternal) && got[0] == "remove" {
				t.Errorf("in-place venue %+v invoked the container verb %q; it has no container to remove", tc.d, got[0])
			}
		})
	}
}

// TestBedInPlaceCoversBothInPlaceVenues pins the shared predicate: only a local or an
// external-in-place venue is in place. A pod must not be, or its `charly config`/`start`
// bring-up would be skipped as well.
func TestBedInPlaceCoversBothInPlaceVenues(t *testing.T) {
	cases := []struct {
		name string
		d    spec.CheckBedReply
		want bool
	}{
		{"kind:local is in place", spec.CheckBedReply{IsLocal: true}, true},
		{"external-in-place is in place", spec.CheckBedReply{IsExternal: true}, true},
		{"local external is in place", spec.CheckBedReply{IsLocal: true, IsExternal: true}, true},
		{"container pod is NOT in place", spec.CheckBedReply{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bedInPlace(tc.d); got != tc.want {
				t.Fatalf("bedInPlace(%+v) = %v, want %v", tc.d, got, tc.want)
			}
		})
	}
}

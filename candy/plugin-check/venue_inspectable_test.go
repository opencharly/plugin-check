package check

import "testing"

// venueIsInspectable gates the image-label resolve. It must accept a CONTAINER venue whatever shape
// its requested name had, and reject everything else. Before this predicate existed the handler also
// rejected any name containing a dot, which discarded a venue resolveCheckVenue had just resolved
// for a root-namespaced deploy — the `charly check live` "container ... is not running" report.
func TestVenueIsInspectable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		venue *CheckVenue
		want  bool
	}{
		{"container venue", &CheckVenue{Kind: "container", Name: "charly-ns-bed"}, true},
		{"vm venue", &CheckVenue{Kind: "vm", Name: "some-vm"}, false},
		{"host venue", &CheckVenue{Kind: "host", Name: "."}, false},
		{"nil venue", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := venueIsInspectable(tc.venue); got != tc.want {
				t.Fatalf("venueIsInspectable(%+v) = %v, want %v", tc.venue, got, tc.want)
			}
		})
	}
}

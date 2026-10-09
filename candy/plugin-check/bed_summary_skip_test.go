package check

import "testing"

// TestSkippedFromRollup pins the ONE place the bed record learns that an inner step skipped
// (opencharly/plugin-check#93). Before it, a phase that asserted nothing was written exactly like one
// that asserted and passed: `ok: true` either way, which is charly#865's sentence one layer up.
func TestSkippedFromRollup(t *testing.T) {
	cases := []struct {
		name string
		log  string
		want int
	}{
		{"the real rollup line", "\n5 steps: 4 passed, 0 failed, 1 skipped\n", 1},
		{"singular step word", "3 step: 2 passed, 0 failed, 1 skipped", 1},
		{"nothing skipped", "5 steps: 5 passed, 0 failed, 0 skipped", 0},
		{"several skipped", "9 steps: 6 passed, 1 failed, 2 skipped", 2},
		{"no rollup at all is 0, and that is not a claim", "some tool output\nno rollup here\n", 0},
		{"a line that merely mentions 'skipped' is not a count", "the skipped-widgets step failed\n", 0},
	}
	for _, tc := range cases {
		if got := skippedFromRollup(tc.log); got != tc.want {
			t.Errorf("%s: skippedFromRollup = %d, want %d", tc.name, got, tc.want)
		}
	}
}

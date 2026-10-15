package check

import (
	"strings"
	"testing"
)

// no_plan_skip_test.go — the bed runner's half of the same rule: a child phase that resolved
// NOTHING is recorded as a SKIP, never as a PASS.
//
// `charly check run <bed>` runs one `check live` per nested child and appends a stepResult per
// phase, so the classification in step() is what decides whether the run record says a phase
// asserted something. The vacuous case is invisible to the existing `Skipped` counter by
// construction: nothing resolved means no inner step skipped, so `skipped: 0` beside `ok: true` is
// exactly what a fully-green phase looks like (opencharly/plugin-check#110, measured on
// `distro-arch`'s `check-arch-vm` run `2026.287.2125`).
func TestStepSkipReason(t *testing.T) {
	const childLog = "No plan steps defined for this image.\n" +
		"SKIPPED — no plan steps resolved for deployment check-arch-vm.arch-host: this phase asserted nothing, so it is recorded as a SKIP and not as a pass (opencharly/plugin-check#110)\n"

	cases := []struct {
		name     string
		exitCode int
		log      string
		// wantEmpty asserts the step rendered an ordinary verdict.
		wantEmpty bool
		// wantSub is a substring the reason must carry.
		wantSub string
	}{
		{
			name:     "a skipped child phase is a skip, not a verdict",
			exitCode: CheckSkippedExitCode,
			log:      childLog,
			wantSub:  "no plan steps resolved for deployment check-arch-vm.arch-host",
		},
		{
			name:     "a skip with no stated reason still says which code produced it",
			exitCode: CheckSkippedExitCode,
			log:      "No plan steps defined for this image.\n",
			wantSub:  "exited 3 (skipped) without stating a reason",
		},
		{
			name:      "a green phase is not a skip",
			exitCode:  0,
			log:       "9 steps: 9 passed, 0 failed, 0 skipped\n",
			wantEmpty: true,
		},
		{
			name:      "a FAILING phase is not a skip — collapsing 2 into 3 would hide a real red",
			exitCode:  CheckFailExitCode,
			log:       "9 steps: 6 passed, 3 failed, 0 skipped\n",
			wantEmpty: true,
		},
		{
			name:      "an infra failure is not a skip",
			exitCode:  1,
			log:       "charly: error: command \"check\": boom\n",
			wantEmpty: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stepSkipReason(tc.exitCode, tc.log)
			if tc.wantEmpty {
				if got != "" {
					t.Fatalf("stepSkipReason(%d, %q) = %q, want no skip reason", tc.exitCode, tc.log, got)
				}
				return
			}
			if got == "" {
				t.Fatalf("stepSkipReason(%d, %q) = \"\", want the skip reason — an empty resolution recorded as a pass is the defect (opencharly/plugin-check#110)", tc.exitCode, tc.log)
			}
			if !strings.Contains(got, tc.wantSub) {
				t.Fatalf("stepSkipReason(%d) = %q, want it to contain %q — the reason must carry the child's own sentence (or name the code), never a bare marker", tc.exitCode, got, tc.wantSub)
			}
		})
	}
}

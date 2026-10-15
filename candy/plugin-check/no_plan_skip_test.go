package check

import (
	"os"
	"path/filepath"
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

// TestBedSummaryRecordsASkippedPhase gates the OTHER half of the record: the classifier decides a
// step skipped, and `summary.yml` must then say so. A step that resolved nothing has `skipped: 0`
// (nothing was resolved to skip), so without this key the record is byte-identical to a fully-green
// phase's — which is exactly the reading `opencharly/plugin-check#110` is about. The key is emitted
// only when the step skipped, so its PRESENCE is the marker, and its absence on the neighbouring
// green step is the statement that that one did assert something.
func TestBedSummaryRecordsASkippedPhase(t *testing.T) {
	const reason = "no plan steps resolved for deployment check-arch-vm.charly-cachyos-tailscale-test: this phase asserted nothing, so it is recorded as a SKIP and not as a pass (opencharly/plugin-check#110)"
	dir := t.TempDir()
	writeBedSummary(dir, &bedRunResult{
		Bed:    "check-arch-vm",
		CalVer: "2026.288.0001",
		OK:     true,
		Step: []stepResult{
			{Name: "check-live-charly-cachyos-tailscale-test", OK: true, SkipReason: reason},
			{Name: "check-live-arch-host", OK: true},
		},
	})

	raw, err := os.ReadFile(filepath.Join(dir, "summary.yml"))
	if err != nil {
		t.Fatalf("reading summary.yml: %v", err)
	}
	summary := string(raw)

	// Split the steps block so every assertion is about the step it belongs to, not about the file
	// as a whole: the defect this guards against is a reader attributing one phase's status to
	// another, which is how a vacuous child's PASS went unnoticed next to a green sibling.
	blocks := strings.Split(summary, "  - name: ")
	if len(blocks) != 3 {
		t.Fatalf("summary.yml carries %d step blocks, want 2:\n%s", len(blocks)-1, summary)
	}
	skipped, green := blocks[1], blocks[2]
	if !strings.Contains(skipped, "check-live-charly-cachyos-tailscale-test") {
		t.Fatalf("the skipped step is not the first block:\n%s", summary)
	}
	if !strings.Contains(skipped, "skip_reason: ") {
		t.Fatalf("the skipped step's record carries no skip_reason:\n%s\nwant a `skip_reason:` line under it — `ok: true` beside `skipped: 0` is what a fully-green phase looks like, which is the reading this closes", skipped)
	}
	if !strings.Contains(skipped, "this phase asserted nothing") {
		t.Fatalf("the skip_reason does not carry the reason itself:\n%s", skipped)
	}
	if !strings.Contains(skipped, "skipped: 0") {
		t.Fatalf("the skipped step's `skipped:` count should still be 0 — nothing was resolved to skip, and the reason is what marks the step:\n%s", skipped)
	}
	if strings.Contains(green, "skip_reason: ") {
		t.Fatalf("a green step grew a skip_reason, so the key no longer marks anything:\n%s", green)
	}
}

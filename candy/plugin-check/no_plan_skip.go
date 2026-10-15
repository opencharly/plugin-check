package check

import (
	"fmt"
	"os"
	"strings"
)

// An empty plan resolution is a SKIP, never a pass.
//
// Every check mode resolves a plan before running anything, and every mode has a path where the
// resolution comes back EMPTY: an image that baked no `plan:`, a deployment whose source declares
// none, a bed child whose own plan and whose add_candies contribute no steps. That path returned
// nil — exit 0 — so a phase that asserted NOTHING was indistinguishable from a phase that asserted
// everything and passed.
//
// In a bed it was worse than indistinguishable: `charly check run <bed>` runs one `check live` per
// nested child and banks each as a step, so a child that resolved no steps was recorded `ok: true`
// and counted in the summary. Measured on `opencharly/distro-arch`'s `check-arch-vm`, run
// `2026.287.2125`: the phase `check-live-charly-cachyos-tailscale-test` PASSed in 4.057 s whose
// entire log was `No plan steps defined for this image.`, while its sibling
// `check-live-arch-host` ran nine real steps. The vacuous child's PASS carried no information, and
// the run's own step count could not say which of the two it was (opencharly/plugin-check#110).
//
// The exit-code vocabulary already carries the honest answer: `CheckSkippedExitCode` (3) means
// NOTHING RAN, which is exactly what an empty resolution is. R7's "a test that cannot fail is
// invalid" is satisfied by not claiming a pass for it; R8's "skip visibly" by the line below; and
// the phase's own log then says what happened in words rather than in a step count.
//
// This is the correction `stepResult.Skipped` made one layer down for charly#865 /
// opencharly/plugin-check#93 — the record must not read `ok: true` for a phase that asserted
// nothing — applied at the resolution boundary itself. It is deliberately NOT a failure: a bed
// author may compose a child that legitimately declares no plan steps, and a skip is not a red. It
// is also not a pass.
//
// `subject` names what resolved nothing ("this image", "check-arch-vm.arch-host", ...), so the skip
// line says which artifact produced no assertions.
func skipNoPlanSteps(subject string) error {
	msg := fmt.Sprintf("no plan steps resolved for %s: this phase asserted nothing, so it is recorded as a SKIP and not as a pass (opencharly/plugin-check#110)", subject)
	fmt.Fprintf(os.Stderr, "SKIPPED — %s\n", msg)
	return &CheckSkippedError{Msg: msg}
}

// stepSkipReason is the bed runner's half of the rule above: given a child phase's exit code and
// its retained log, it returns the reason the step must be recorded as SKIPPED instead of as a
// PASS, or "" when the step rendered an ordinary verdict.
//
// It is a named function rather than a condition inlined into step() for the reason
// bedRunGuard documents: the decision IS the deliverable, and an inlined one cannot be gated. A
// child that exits with the skip code is a step that ran NOTHING — a plan that resolved empty, or a
// bed whose host prerequisite was absent — and neither is a verdict.
func stepSkipReason(exitCode int, logText string) string {
	if exitCode != CheckSkippedExitCode {
		return ""
	}
	// The child prints its own `SKIPPED — <reason>` line; carry that sentence rather than a
	// restatement, so the run record holds the producer's words (and the issue it names).
	for _, line := range strings.Split(logText, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "SKIPPED — ") {
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "SKIPPED — "))
		}
	}
	return fmt.Sprintf("the phase exited %d (skipped) without stating a reason; a skip is not a verdict", exitCode)
}

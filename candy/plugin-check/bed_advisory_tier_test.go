package check

import "testing"

// The ADVISORY tier is the explicit separation this gate needs: a performance-degradation
// advisory (e.g. the podman store-size nudge) is REPORTED but can never fail a step, while
// a real warning still counts toward the zero-warning bar and an error still hard-fails.
func TestAdvisoryTierIsReportedButNeverGating(t *testing.T) {
	log := "notice: podman store is bloated (66.99GiB reclaimable, ~60% of 110.0GiB) — the overlay-store corruption class tracked in opencharly/charly#173 tracks this factor.\n" +
		"advisory: some other performance note\n" +
		"warning: a real warning\n" +
		"error: a real failure\n"

	d := scanStepDiagnostics(log)
	if d.Advisories != 2 {
		t.Fatalf("Advisories = %d, want 2 (both notice: and advisory: lines)", d.Advisories)
	}
	if d.Warnings != 1 {
		t.Fatalf("Warnings = %d, want 1 — an advisory must NOT be counted as a warning", d.Warnings)
	}
	if d.Errors != 1 {
		t.Fatalf("Errors = %d, want 1", d.Errors)
	}
	// The advisory tier never fails, even under the strictest staged policy.
	if (stepDiagnostics{Advisories: 99}).fails(diagnosticPolicy{ErrorsFatal: true, WarningsFatal: true}) {
		t.Fatal("advisories must never fail a step, under any policy")
	}
	// The warning tier still fails when staged fatal (the R10-equivalent bar).
	if !(stepDiagnostics{Warnings: 1}).fails(diagnosticPolicy{ErrorsFatal: true, WarningsFatal: true}) {
		t.Fatal("a warning must still fail when WarningsFatal is staged on")
	}
	// And the scanner's classification of the real emitter line is a recognizer hit.
	if sev, _, ok := classifyDiagnosticLine("notice: podman store is bloated (1.0GiB reclaimable, ~1% of 9.0GiB)"); !ok || sev != severityAdvisory {
		t.Fatalf("the emitter's notice line classified as (%q, ok=%v), want the advisory tier", sev, ok)
	}
}

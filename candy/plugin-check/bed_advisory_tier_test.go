package check

import (
	"strings"
	"testing"
)

// The ADVISORY tier is COUNTED and (opencharly/charly#739) GATED by default: a non-zero
// advisory count is a defect — the tier exists so a diagnostic can be reported distinctly, NOT
// so it can pass silently. A genuinely expected advisory must be allowlisted explicitly.
func TestAdvisoryTierIsReportedAndGating(t *testing.T) {
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
	// A non-zero advisory count MUST fail once AdvisoriesFatal is on (the default).
	if !(stepDiagnostics{Advisories: 99}).fails(diagnosticPolicy{ErrorsFatal: true, WarningsFatal: false, AdvisoriesFatal: true}) {
		t.Fatal("advisories must fail a step when AdvisoriesFatal is on (the default)")
	}
	// The default policy has it ON — the whole point of #739.
	if !defaultDiagnosticPolicy().AdvisoriesFatal {
		t.Fatal("defaultDiagnosticPolicy must set AdvisoriesFatal: true")
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

// A tier that is counted per step and silently 0 at run level is invisible to the reader who
// checks the summary. This is the defect the live A/B evidence exposed: the image-build step
// carried advisories: 1 while the run rollup printed advisories: 0, because the fold listed
// every other counter and not this one. Pinned here so the class cannot recur.
func TestAdvisoryCountReachesTheRunRollup(t *testing.T) {
	steps := []stepResult{
		{Diag: stepDiagnostics{Advisories: 2, Warnings: 1}},
		{Diag: stepDiagnostics{Advisories: 3, Errors: 1, Allowlisted: 4, CacheHits: 1, CacheSteps: 2}},
	}
	run := rollupStepDiagnostics(steps)
	if run.Advisories != 5 {
		t.Fatalf("run rollup Advisories = %d, want 5 (every step's advisories must reach the run level)", run.Advisories)
	}
	if run.Warnings != 1 || run.Errors != 1 || run.Allowlisted != 4 || run.CacheHits != 1 || run.CacheSteps != 2 {
		t.Fatalf("run rollup lost a tier: warnings=%d errors=%d allowlisted=%d cache=%d/%d",
			run.Warnings, run.Errors, run.Allowlisted, run.CacheHits, run.CacheSteps)
	}
	// The run summary must PRINT it AND state that it is fatal (gated).
	var buf strings.Builder
	writeRunDiagnostics(&buf, run)
	if !strings.Contains(buf.String(), "advisories: 5") {
		t.Fatalf("the run diagnostics block must print the advisory count, got:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "advisories_fatal: true") {
		t.Fatalf("the run diagnostics block must state that advisories ARE fatal (gated), got:\n%s", buf.String())
	}
	// And the per-step console suffix must carry it: a PASS must not be able to hide an advisory.
	if n := diagNotice(stepDiagnostics{Advisories: 1, Allowlisted: 1}); !strings.Contains(n, "advisories=1") {
		t.Fatalf("diagNotice = %q, want it to carry advisories=1", n)
	}
}

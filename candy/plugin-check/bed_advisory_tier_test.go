package check

import (
	"strings"
	"testing"
)

// The ADVISORY tier is COUNTED, REPORTED, and policy-driven — but NOT fatal by DEFAULT.
// bed_diagnostics.go documents a MEASURED promotion ladder that promotes in SEVERITY ORDER
// (error → warning → advisory); the least-severe advisory tier must not gate ahead of the
// warning tier. The PLUMBING gates when AdvisoriesFatal is explicitly set, so promotion is a
// flag flip, never a new feature.
func TestAdvisoryTierIsReportedNotGatingByDefault(t *testing.T) {
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
	// The DEFAULT keeps the advisory tier reported-only: it must NOT gate, or the least-severe
	// tier would red beds that pass today (measured: vm-create 287, deploy-add 975 advisories).
	if defaultDiagnosticPolicy().AdvisoriesFatal {
		t.Fatal("defaultDiagnosticPolicy must NOT set AdvisoriesFatal — the advisory tier is not yet fatal (measured promotion order)")
	}
	if (stepDiagnostics{Advisories: 99}).fails(defaultDiagnosticPolicy()) {
		t.Fatal("a non-zero advisory count must NOT fail a step under the default policy (reported-only)")
	}
	// The plumbing DOES gate when the policy is explicitly staged on — so promotion is a flag
	// flip, not a new feature.
	if !(stepDiagnostics{Advisories: 99}).fails(diagnosticPolicy{ErrorsFatal: true, WarningsFatal: false, AdvisoriesFatal: true}) {
		t.Fatal("advisories must fail a step when AdvisoriesFatal is explicitly staged on")
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
	// The run summary must state the disposition — and it must come FROM THE POLICY, not a
	// hardcoded literal. Under the default the advisory tier is reported-only.
	var buf strings.Builder
	writeRunDiagnostics(&buf, run, defaultDiagnosticPolicy())
	if !strings.Contains(buf.String(), "advisories: 5") {
		t.Fatalf("the run diagnostics block must print the advisory count, got:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "advisories_fatal: false") {
		t.Fatalf("the run diagnostics block must state the policy's advisories_fatal (false by default), got:\n%s", buf.String())
	}
	// The discriminating assertion (block #3): a NON-default policy that stages the advisory tier
	// fatal MUST print `advisories_fatal: true`. A hardcoded literal would print `false` here and
	// fail — so this test fails if the code ever returns to a literal.
	var staged strings.Builder
	writeRunDiagnostics(&staged, run, diagnosticPolicy{ErrorsFatal: true, WarningsFatal: false, AdvisoriesFatal: true})
	if !strings.Contains(staged.String(), "advisories_fatal: true") {
		t.Fatalf("with AdvisoriesFatal staged ON the summary must print `advisories_fatal: true` (the printed value must follow the policy, not a literal), got:\n%s", staged.String())
	}
	// And the per-step console suffix must carry it: a PASS must not be able to hide an advisory.
	if n := diagNotice(stepDiagnostics{Advisories: 1, Allowlisted: 1}); !strings.Contains(n, "advisories=1") {
		t.Fatalf("diagNotice = %q, want it to carry advisories=1", n)
	}
}

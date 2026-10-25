package check

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The report text below is the ENGINE'S OWN output (spec/report's FormatStepResultsText), as
// measured on a live bed: the child's own per-check lines, then the rollup. It is the only
// place the per-check statuses and the reasons exist, so it is the document this file reads.
const liveSkippedCheckReport = `  SKIP  check a declared check that does not apply to this run  [not-applicable-here]  skip: true

1 step: 0 passed, 0 failed, 1 skipped
`

const liveMixedCheckReport = `  PASS  check the neovim editor binary is on PATH  [neovim-binary]
  SKIP  check file=/usr/bin/fastfetch  [fastfetch-binary]  excluded on distro "ubuntu"
  FAIL  check the sag binary is present  [sag-binary]  command not found

3 steps: 1 passed, 1 failed, 1 skipped
`

// TestParseCheckReportReadsPerCheckStatuses pins the READER: the acceptance predicate
// ("every declared check id resolved to PASS, and no declared check id resolved to SKIP") is
// only checkable if the record carries the id and the status of every declared check — and
// the id and the status exist ONLY in the child's own report (opencharly/plugin-check#132).
func TestParseCheckReportReadsPerCheckStatuses(t *testing.T) {
	rep := parseCheckReport(liveSkippedCheckReport)
	if !rep.Present {
		t.Fatalf("parseCheckReport() did not see the report in:\n%s", liveSkippedCheckReport)
	}
	if len(rep.Checks) != 1 {
		t.Fatalf("parseCheckReport() read %d checks, want 1: %+v", len(rep.Checks), rep.Checks)
	}
	got := rep.Checks[0]
	if got.Status != verdictSkip {
		t.Errorf("the check's status = %q, want %q", got.Status, verdictSkip)
	}
	if got.ID != "not-applicable-here" {
		t.Errorf("the check's id = %q, want %q", got.ID, "not-applicable-here")
	}
	if got.Reason != "skip: true" {
		t.Errorf("the check's reason = %q, want %q", got.Reason, "skip: true")
	}
}

// TestParseCheckReportReadsEveryDeclaredCheck pins that a report with several declared
// checks is read in full — the mixed case is where a skip hides next to a pass.
func TestParseCheckReportReadsEveryDeclaredCheck(t *testing.T) {
	rep := parseCheckReport(liveMixedCheckReport)
	if !rep.Present {
		t.Fatalf("parseCheckReport() did not see the report")
	}
	if len(rep.Checks) != 3 {
		t.Fatalf("parseCheckReport() read %d checks, want 3: %+v", len(rep.Checks), rep.Checks)
	}
	want := []struct{ id, status string }{
		{"neovim-binary", verdictPass},
		{"fastfetch-binary", verdictSkip},
		{"sag-binary", verdictFail},
	}
	for i, w := range want {
		if rep.Checks[i].ID != w.id || rep.Checks[i].Status != w.status {
			t.Errorf("check %d = %s/%s, want %s/%s", i, rep.Checks[i].ID, rep.Checks[i].Status, w.id, w.status)
		}
	}
	if rep.Checks[1].Reason != `excluded on distro "ubuntu"` {
		t.Errorf("the skipped check's reason = %q, want the report's own sentence", rep.Checks[1].Reason)
	}
}

// TestParseCheckReportIgnoresMutatingSteps pins that a plan step which does not ASSERT is
// not a declared check: `charly check live` runs in verify-only mode, which skips the
// mutating steps BY DESIGN, so counting them would report every bed whose plan carries a
// run: step as a skip.
func TestParseCheckReportIgnoresMutatingSteps(t *testing.T) {
	const report = `  SKIP  run install the toolchain  [toolchain-install]  skipped — verify-only mode (mutating step)
  PASS  check the toolchain is on PATH  [toolchain-path]

2 steps: 1 passed, 0 failed, 1 skipped
`
	rep := parseCheckReport(report)
	if len(rep.Checks) != 1 {
		t.Fatalf("parseCheckReport() read %d checks, want 1 (the mutating step is not a check): %+v", len(rep.Checks), rep.Checks)
	}
	if rep.Checks[0].ID != "toolchain-path" {
		t.Errorf("the read check = %q, want %q", rep.Checks[0].ID, "toolchain-path")
	}
}

// TestParseCheckReportWithoutAReport pins that a log with no report is not a report: its
// absence is not a claim that a check was skipped.
func TestParseCheckReportWithoutAReport(t *testing.T) {
	rep := parseCheckReport("some build output\nno rollup here\n")
	if rep.Present {
		t.Errorf("parseCheckReport() claimed a report in a log that carries none")
	}
	if len(rep.Checks) != 0 {
		t.Errorf("parseCheckReport() read %d checks from a log with no report", len(rep.Checks))
	}
}

// TestCheckReportVerdict pins the PHASE verdict.
//
// The defect (opencharly/charly#904): a phase whose only declared check was SKIPPED proved
// nothing and was banked as a pass. That phase is a SKIP.
//
// The counter-case pins the other half, and it is the reason the rule is not "any skipped
// check": a plan may declare a check that does not apply to the substrate — `skip: true`
// and `exclude_distro:` are documented authoring features (layer-charly-check/charly.yml:
// 1902-1903), and the org's own images carry them (layer-dev-tools/charly.yml:142-166
// declares three `exclude_distro:` checks). A phase that ran and PASSED other checks proved
// something, so it is a PASS — and the checks that did not run are recorded per check.
func TestCheckReportVerdict(t *testing.T) {
	cases := []struct {
		name       string
		report     string
		wantVerdic string
		wantReason bool
	}{
		{"a phase whose only declared check skipped proves nothing", liveSkippedCheckReport, verdictSkip, true},
		{"a phase with a passing check and an inapplicable one proves something", "  PASS  check the neovim editor binary is on PATH  [neovim-binary]\n  SKIP  check file=/usr/bin/fastfetch  [fastfetch-binary]  excluded on distro \"ubuntu\"\n\n2 steps: 1 passed, 0 failed, 1 skipped\n", verdictPass, false},
		{"a phase with no declared check at all proves nothing", "  SKIP  run install the toolchain  [toolchain-install]  skipped — verify-only mode (mutating step)\n\n1 step: 0 passed, 0 failed, 1 skipped\n", verdictSkip, true},
		{"a phase whose checks all passed proves something", "  PASS  check the neovim editor binary is on PATH  [neovim-binary]\n\n1 step: 1 passed, 0 failed, 0 skipped\n", verdictPass, false},
	}
	for _, tc := range cases {
		verdict, reason := checkReportVerdict(parseCheckReport(tc.report))
		if verdict != tc.wantVerdic {
			t.Errorf("%s: verdict = %q, want %q (reason %q)", tc.name, verdict, tc.wantVerdic, reason)
		}
		if tc.wantReason && reason == "" {
			t.Errorf("%s: a skip must carry its reason", tc.name)
		}
		if !tc.wantReason && reason != "" {
			t.Errorf("%s: a pass must carry no reason (got %q)", tc.name, reason)
		}
	}
}

// TestRunStatusIsThreeState pins the RUN verdict: PASS / SKIP / FAIL, with the FAIL
// winning over the SKIP.
func TestRunStatusIsThreeState(t *testing.T) {
	passing := &bedRunResult{OK: true, Step: []stepResult{
		{Name: "check-live", OK: true, Checks: []checkOutcome{{ID: "a", Status: verdictPass}}},
	}}
	if got := runStatus(passing); got != verdictPass {
		t.Errorf("a run whose phase proved something = %q, want %q", got, verdictPass)
	}

	skipped := &bedRunResult{OK: true, Step: []stepResult{
		{Name: "check-live", OK: true, SkipReason: "declared check not-applicable-here did not run: skip: true",
			Checks: []checkOutcome{{ID: "not-applicable-here", Status: verdictSkip, Reason: "skip: true"}}},
	}}
	if got := runStatus(skipped); got != verdictSkip {
		t.Errorf("a run whose phase proved nothing = %q, want %q", got, verdictSkip)
	}

	failed := &bedRunResult{OK: false, Step: []stepResult{
		{Name: "check-live", OK: false},
		{Name: "check-live-2", OK: true, SkipReason: "declared check x did not run: skip: true"},
	}}
	if got := runStatus(failed); got != verdictFail {
		t.Errorf("a failed run = %q, want %q — a FAIL wins over a SKIP", got, verdictFail)
	}

	prereq := &bedRunResult{OK: true, SkippedPrereq: true, SkipReason: "no GPU on this host"}
	if got := runStatus(prereq); got != verdictSkip {
		t.Errorf("a bed that never ran = %q, want %q", got, verdictSkip)
	}
}

// TestRunStatusReadsWhatTheRunProved pins the granularity of the verdict: the run reads the
// CHECK OUTCOMES, and a phase that asserted nothing — which declares NO check id — cannot
// unprove a check another phase proved.
//
// This is the shape of the #110 witness bed (check-empty-resolution-witness), measured live:
// its own declared check PASSES, and its MEMBER phase has no plan at all, so the member
// phase is a vacuous SKIP step beside it. Reading "any skipping step ⇒ the run skips" made
// that bed exit 3 — a false SKIP, exactly as wrong as the false PASS this change closes. The
// strict acceptance predicate ("every declared check id resolved to PASS, and no declared
// check id resolved to SKIP") is unaffected by a phase that declares no check id.
func TestRunStatusReadsWhatTheRunProved(t *testing.T) {
	vacuousMember := &bedRunResult{OK: true, Step: []stepResult{
		{Name: "check-live", OK: true, Checks: []checkOutcome{{ID: "empty-resolution-is-a-skip", Status: verdictPass}}},
		{Name: "check-live-empty-plan-child", OK: true,
			SkipReason: "no plan steps resolved for deployment check-empty-resolution-witness.empty-plan-child: this phase asserted nothing (opencharly/plugin-check#110)"},
	}}
	if got := runStatus(vacuousMember); got != verdictPass {
		t.Errorf("a run that proved something beside a vacuous phase = %q, want %q", got, verdictPass)
	}
	if got := runSkippedChecks(vacuousMember); got != 0 {
		t.Errorf("a vacuous phase declares no check id, so it skips %d checks, want 0", got)
	}
	// ...and the vacuous phase is still NAMED in the headline, so the run is not a bare PASS.
	detail := runSkipDetail(vacuousMember)
	if !strings.Contains(detail, "check-live-empty-plan-child") || !strings.Contains(detail, "asserted nothing") {
		t.Errorf("the headline detail = %q, want the vacuous phase named", detail)
	}

	// A run with NOTHING proved — every phase vacuous — is a SKIP: it asserted nothing.
	allVacuous := &bedRunResult{OK: true, Step: []stepResult{
		{Name: "check-live", OK: true, SkipReason: "no plan steps resolved for deployment bed.child (opencharly/plugin-check#110)"},
	}}
	if got := runStatus(allVacuous); got != verdictSkip {
		t.Errorf("a run that proved nothing = %q, want %q", got, verdictSkip)
	}
}

// TestBedVerdictLineCarriesWhatDidNotRun pins the HEADLINE.
//
// The defect was a headline that read as a bare `PASS (steps=N)` while a declared check
// skipped (opencharly/charly#904). So:
//   - a run that proved nothing reads SKIP and names the reason;
//   - a run that proved something but skipped a check names the count AND the reason, so it
//     can never be read as a bare PASS;
//   - a run that proved everything keeps the plain shape.
func TestBedVerdictLineCarriesWhatDidNotRun(t *testing.T) {
	provedNothing := bedVerdictLine("check-foo", &bedRunResult{OK: true, Step: []stepResult{
		{Name: "check-live", OK: true, SkipReason: "declared check not-applicable-here did not run: skip: true",
			Checks: []checkOutcome{{ID: "not-applicable-here", Status: verdictSkip, Reason: "skip: true"}}},
	}})
	if !strings.Contains(provedNothing, "charly check run check-foo: SKIP (steps=1, skipped_checks=1)") {
		t.Errorf("the headline = %q, want a SKIP carrying the skip count", provedNothing)
	}
	if !strings.Contains(provedNothing, "not-applicable-here") || !strings.Contains(provedNothing, "skip: true") {
		t.Errorf("the headline = %q, want the skipped check and its reason named", provedNothing)
	}

	provedSome := bedVerdictLine("check-foo", &bedRunResult{OK: true, Step: []stepResult{
		{Name: "check-live", OK: true, Checks: []checkOutcome{
			{ID: "neovim-binary", Status: verdictPass},
			{ID: "fastfetch-binary", Status: verdictSkip, Reason: `excluded on distro "ubuntu"`},
		}},
	}})
	if !strings.Contains(provedSome, "charly check run check-foo: PASS (steps=1, skipped_checks=1)") {
		t.Errorf("the headline = %q, want the PASS carrying the skip count", provedSome)
	}
	if !strings.Contains(provedSome, "fastfetch-binary") || !strings.Contains(provedSome, `excluded on distro "ubuntu"`) {
		t.Errorf("the headline = %q, want the skipped check and its reason named", provedSome)
	}

	provedEverything := bedVerdictLine("check-foo", &bedRunResult{OK: true, Step: []stepResult{
		{Name: "check-live", OK: true, Checks: []checkOutcome{{ID: "neovim-binary", Status: verdictPass}}},
	}})
	if provedEverything != "charly check run check-foo: PASS (steps=1)" {
		t.Errorf("the headline = %q, want the plain verdict shape", provedEverything)
	}
}

// TestBedRunOutcomeErrorMapsTheVerdictToTheExitCode pins the EXIT CODE — the signal every
// consumer reads (the org's own scripts/check-bed-watch.sh classifies 3 as "a SKIP, never a
// pass"). A run that proved nothing exits 3; a run that failed exits 2; a run that proved
// what it declared exits 0.
func TestBedRunOutcomeErrorMapsTheVerdictToTheExitCode(t *testing.T) {
	provedNothing := &bedRunResult{OK: true, Step: []stepResult{
		{Name: "check-live", OK: true, SkipReason: "declared check not-applicable-here did not run: skip: true"},
	}}
	err := bedRunOutcomeError("check-foo", provedNothing, nil)
	var skipErr *CheckSkippedError
	if !errors.As(err, &skipErr) {
		t.Fatalf("a run that proved nothing returned %v, want a CheckSkippedError (exit 3)", err)
	}
	if !strings.Contains(err.Error(), "not-applicable-here") {
		t.Errorf("the error = %q, want the skipped check named", err.Error())
	}

	failed := &bedRunResult{OK: false, FailExitCode: CheckFailExitCode, Step: []stepResult{{Name: "check-live", OK: false}}}
	err = bedRunOutcomeError("check-foo", failed, errors.New("boom"))
	var failErr *CheckFailedError
	if !errors.As(err, &failErr) {
		t.Fatalf("a failed run returned %v, want a CheckFailedError (exit 2)", err)
	}

	// A FAIL wins over a SKIP: a run that both failed and skipped is a FAIL.
	both := &bedRunResult{OK: false, FailExitCode: CheckFailExitCode, Step: []stepResult{
		{Name: "check-live", OK: false},
		{Name: "check-live-2", OK: true, SkipReason: "declared check x did not run"},
	}}
	err = bedRunOutcomeError("check-foo", both, errors.New("boom"))
	if !errors.As(err, &failErr) {
		t.Fatalf("a run that failed and skipped returned %v, want a CheckFailedError (exit 2)", err)
	}

	provedEverything := &bedRunResult{OK: true, Step: []stepResult{
		{Name: "check-live", OK: true, Checks: []checkOutcome{{ID: "neovim-binary", Status: verdictPass}}},
	}}
	if err := bedRunOutcomeError("check-foo", provedEverything, nil); err != nil {
		t.Fatalf("a run that proved what it declared returned %v, want nil", err)
	}
}

// TestWriteBedSummaryCarriesThePerCheckRecord pins the RECORD: the acceptance predicate
// must be checkable FROM summary.yml, which is the artifact every PR body and every
// attribution tier rests on (opencharly/charly#904).
func TestWriteBedSummaryCarriesThePerCheckRecord(t *testing.T) {
	dir := t.TempDir()
	res := &bedRunResult{
		Bed:    "witness-bed",
		CalVer: "2026.298.0000",
		Step: []stepResult{
			{Name: "check-live", OK: true, SkipReason: "declared check not-applicable-here did not run: skip: true",
				Checks: []checkOutcome{{
					ID:     "not-applicable-here",
					Status: verdictSkip,
					Text:   "a declared check that does not apply to this run",
					Reason: "skip: true",
				}}},
		},
		OK: true,
	}
	writeBedSummary(dir, res)

	raw, err := os.ReadFile(filepath.Join(dir, "summary.yml"))
	if err != nil {
		t.Fatalf("read summary.yml: %v", err)
	}
	out := string(raw)
	for _, want := range []string{
		"    status: SKIP\n",
		"    checks:\n",
		"      - id: not-applicable-here\n",
		"        status: SKIP\n",
		"        reason: \"skip: true\"\n",
		"skipped_checks: 1\n",
		"status: SKIP\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("summary.yml is missing %q:\n%s", want, out)
		}
	}
}

// TestAggregateRosterDoesNotFoldAnUnprovenBedIntoTheGreen pins the ROSTER: a bed that ran
// and proved nothing is not a pass, and the roster must not print a green verdict over it
// (opencharly/plugin-check#132). The prereq-skip contract stays as it was — a bed that never
// ran is counted separately, and a skip alongside a pass is still a PASS.
func TestAggregateRosterDoesNotFoldAnUnprovenBedIntoTheGreen(t *testing.T) {
	err := aggregateRoster("r", []rosterBedOutcome{
		{Bed: "check-passed", OK: true},
		{Bed: "check-unproven", Unproven: true, ExitCode: CheckSkippedExitCode, Reason: "declared check x did not run: skip: true"},
	})
	var skipErr *CheckSkippedError
	if !errors.As(err, &skipErr) {
		t.Fatalf("a roster with an unproven bed returned %v, want a CheckSkippedError (exit 3)", err)
	}
	if !strings.Contains(err.Error(), "check-unproven") {
		t.Errorf("the roster error = %q, want the unproven bed named", err.Error())
	}
}

package check

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
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

// TestParseCheckReportCreditsNothingWithoutARollup pins the rule that closes the
// false-PASS half of opencharly/plugin-check#134: a log that printed NO report carries no
// check outcome, whatever lines in it happen to be shaped like one.
//
// The reader used to collect every report-SHAPED line in the log and leave the counters to
// decide what to do with them, so a build step's output could credit a PASS to a run that
// proved NOTHING — the false green the whole verdict change exists to close.
func TestParseCheckReportCreditsNothingWithoutARollup(t *testing.T) {
	const log = "building the image\n  PASS  check a check that never ran  [ghost-check]\n"
	rep := parseCheckReport(log)
	if rep.Present {
		t.Errorf("parseCheckReport() claimed a report in a log that carries no rollup")
	}
	if rep.Readable {
		t.Errorf("parseCheckReport() called a log with no rollup readable")
	}
	if len(rep.Checks) != 0 {
		t.Errorf("parseCheckReport() credited %d check(s) from a log that printed no report, want 0", len(rep.Checks))
	}
}

// TestParseCheckReportReadsTheChildsOwnReport pins WHICH report the reader takes when a log
// carries more than one: the child's OWN, which is the LAST one it printed.
//
// The engine renders the report last — `live_cmd.go` renders it after any output forwarded
// from a nested venue, `feature_cmd.go` renders it to stdout — so the last rollup in the log
// is the report the child printed, and the per-check lines are the ones in that block. A
// log that carries an earlier report must not merge the two: the earlier block's checks
// belong to a different plan.
func TestParseCheckReportReadsTheChildsOwnReport(t *testing.T) {
	const log = `  PASS  check the forwarded venue's own check  [guest-check]

1 step: 1 passed, 0 failed, 0 skipped
  PASS  check the child's own check  [host-check]

1 step: 1 passed, 0 failed, 0 skipped
`
	rep := parseCheckReport(log)
	if !rep.Present {
		t.Fatalf("parseCheckReport() did not see the report in:\n%s", log)
	}
	if !rep.Readable {
		t.Fatalf("parseCheckReport() did not read the child's own report in:\n%s", log)
	}
	if len(rep.Checks) != 1 {
		t.Fatalf("parseCheckReport() read %d checks, want 1 (the child's own block only): %+v", len(rep.Checks), rep.Checks)
	}
	if rep.Checks[0].ID != "host-check" {
		t.Errorf("the read check = %q, want %q — the child's own report is the last one it printed", rep.Checks[0].ID, "host-check")
	}
}

// TestParseCheckReportRefusesABlockThatDisagreesWithItsOwnCount pins the report's own
// self-check: the rollup says "N steps" and the block must carry N step lines.
//
// A block that does not is carrying text the report did not print — a step's MESSAGE with a
// report-shaped line inside it, which the engine's truncate() permits because it bounds a
// message's length but does not flatten its newlines. Such a report credits NOTHING: a
// verdict must not rest on text the report did not print.
func TestParseCheckReportRefusesABlockThatDisagreesWithItsOwnCount(t *testing.T) {
	const log = `  SKIP  check a check that does not apply to this run  [not-applicable]  skip: true
  PASS  check a line out of a step's message  [ghost]

1 step: 0 passed, 0 failed, 1 skipped
`
	rep := parseCheckReport(log)
	if !rep.Present {
		t.Fatalf("parseCheckReport() did not see the report in:\n%s", log)
	}
	if rep.Readable {
		t.Errorf("parseCheckReport() read a block of %d step line(s) against a rollup of %d step(s)", rep.Printed, rep.Steps)
	}
	if len(rep.Checks) != 0 {
		t.Errorf("parseCheckReport() credited %d check(s) from a block that disagrees with the report's own count, want 0", len(rep.Checks))
	}
	verdict, reason := checkReportVerdict(rep)
	if verdict != verdictSkip {
		t.Errorf("an unreadable report = %q, want %q — nothing can be credited from it", verdict, verdictSkip)
	}
	if !strings.Contains(reason, "could not be read") {
		t.Errorf("the reason = %q, want the report's unreadability named", reason)
	}
}

// TestARunIsNotAPassOnTextThatIsNotAReport is the RUN-level statement of the same rule, and
// the one the false green would have taken: a run whose only report-shaped line came from a
// log that printed no report has proved NOTHING, so it is a SKIP and not a PASS.
func TestARunIsNotAPassOnTextThatIsNotAReport(t *testing.T) {
	const log = "building the image\n  PASS  check a check that never ran  [ghost-check]\n"
	rep := parseCheckReport(log)
	res := &bedRunResult{OK: true, Step: []stepResult{
		{Name: "image-build", OK: true, Checks: rep.Checks},
	}}
	if got := runStatus(res); got != verdictSkip {
		t.Errorf("a run that proved nothing = %q, want %q — text that is not a report cannot credit a PASS", got, verdictSkip)
	}
	if got := runPassedChecks(res); got != 0 {
		t.Errorf("a run credited %d passing check(s) from a log that printed no report, want 0", got)
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

// TestCheckReportVerdictTreatsAFailedCheckAsAFail pins the other half of the verdict: a
// report that says a declared check FAILED is a FAIL, not a PASS (the first non-blocking
// observation on opencharly/plugin-check#133, fixed in opencharly/plugin-check#134).
//
// The ordinary case is already covered by the exit code — the report and the exit code are
// derived from the same step results, so a child that honoured its contract exited 2 — but
// the gap is real when a child exits 0 and its own report says otherwise. Then the record
// would carry `status: FAIL` for a check while the run read PASS.
func TestCheckReportVerdictTreatsAFailedCheckAsAFail(t *testing.T) {
	verdict, reason := checkReportVerdict(parseCheckReport(liveMixedCheckReport))
	if verdict != verdictFail {
		t.Errorf("a report whose check FAILED = %q, want %q", verdict, verdictFail)
	}
	if !strings.Contains(reason, "sag-binary") {
		t.Errorf("the reason = %q, want the failed check named", reason)
	}
}

// TestStepVerdictFromReport pins how a report reaches its STEP: a SKIP sets the step's skip
// reason, a FAIL is returned as the failure sentence, and a PASS is no objection.
func TestStepVerdictFromReport(t *testing.T) {
	skipped := stepResult{OK: true}
	if fail := stepVerdictFromReport(parseCheckReport(liveSkippedCheckReport), &skipped); fail != "" {
		t.Errorf("a report that says a check skipped returned the failure %q, want none — a skip is not a failure", fail)
	}
	if skipped.SkipReason == "" {
		t.Errorf("a skipping report left the step with no reason")
	}
	if !strings.Contains(skipped.SkipReason, "not-applicable-here") {
		t.Errorf("the step's skip reason = %q, want the skipped check named", skipped.SkipReason)
	}

	failed := stepResult{OK: true}
	fail := stepVerdictFromReport(parseCheckReport(liveMixedCheckReport), &failed)
	if fail == "" {
		t.Errorf("a report that says a check FAILED returned no failure sentence")
	}
	if failed.SkipReason != "" {
		t.Errorf("a failing report set the step's skip reason %q — a failure is not a skip", failed.SkipReason)
	}
	if stepStatus(&failed) != verdictFail {
		t.Errorf("a step whose report says FAIL = %q, want %q", stepStatus(&failed), verdictFail)
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

// TestANonPlanChildCreditsNothingFromItsLog pins the rule that keeps a build's output out
// of the verdict: a REPORT is what a PLAN child prints, so a build step's log can never
// bank a check — not even when its output ends with a block that looks exactly like a
// report.
//
// MEASURED before the rule: such a block WAS credited, and the run read PASS while its
// plan step proved nothing (opencharly/plugin-check#135).
func TestANonPlanChildCreditsNothingFromItsLog(t *testing.T) {
	forged := "building layer...\n" +
		"  PASS  check the image builds  [some-build-step]  ok\n" +
		"1 step: 1 passed, 0 failed, 0 skipped\n"
	rep, skipped := childReport([]string{"box", "build", "some/image"}, forged)
	if rep.Present {
		t.Errorf("a build's log was read as a report: %+v", rep)
	}
	if len(rep.Checks) != 0 {
		t.Errorf("a build's log credited %d check(s), want 0", len(rep.Checks))
	}
	if skipped != 0 {
		t.Errorf("a build's log inflated the inner skip count to %d, want 0", skipped)
	}
	// The run-level consequence: the run must read as a SKIP, not a PASS.
	res := &bedRunResult{OK: true, Step: []stepResult{
		{Name: "image-build", OK: true, Checks: rep.Checks},
		{Name: "check-live", OK: true, SkipReason: "declared check x did not run: skip: true"},
	}}
	if got := runStatus(res); got != verdictSkip {
		t.Errorf("a run whose plan proved nothing = %q, want %q", got, verdictSkip)
	}
}

// TestAPlanChildReadsItsReport pins THAT a plan child's log is read for a report — the
// report the child printed, which is the last block in its log.
//
// The cli seam captures the child's two streams into ONE text (a whole line at a time), so
// the runner reads the merged log; the report is the last block in it because the engine
// renders the report last.
func TestAPlanChildReadsItsReport(t *testing.T) {
	for _, argv := range [][]string{
		{"check", "live", "bed"},
		{"check", "box", "some/image"},
		{"check", "feature", "run", "dep"},
	} {
		rep, _ := childReport(argv, liveSkippedCheckReport)
		if len(rep.Checks) != 1 {
			t.Errorf("%v read %d check(s) from the child's own report, want 1", argv, len(rep.Checks))
		}
	}
}

// renderersNotReachableAsAStepChild are the report renderers a bed run never launches as a
// step: the hidden build-scope feature-run engine, which the `charly box feature run`
// bridge invokes with its own args (CheckCmd.FeatureBox — `charly check __feature-box`).
// A new renderer that a bed run CAN launch belongs in planChildReporters instead —
// otherwise its report is ignored and a run that proved something reads as a SKIP.
var renderersNotReachableAsAStepChild = map[string]bool{
	"CheckFeatureBoxCmd": true,
}

// TestEveryReportRendererIsAPlanChild holds the reader's table against the SOURCE: every
// command in this package that renders a step report must be a plan child, with the stream
// it renders on, and every entry in the table must render one.
//
// The table is the only place that says which children print a report, so it must not be
// able to drift from the code that prints them.
func TestEveryReportRendererIsAPlanChild(t *testing.T) {
	renderers := reportRenderersInSource(t)
	for command := range renderers {
		if renderersNotReachableAsAStepChild[command] {
			continue
		}
		found := false
		for _, r := range planChildReporters {
			if r == command {
				found = true
			}
		}
		if !found {
			t.Errorf("%s renders a step report but is not a plan child — its report would be ignored", command)
		}
	}
	for key, command := range planChildReporters {
		if !renderers[command] {
			t.Errorf("the table claims %q (%s) renders a report, but %s renders none", key, command, command)
		}
	}
}

// reportRenderersInSource finds every command in this package that renders a step report,
// by reading the package's own source.
func reportRenderersInSource(t *testing.T) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	// parser.ParseDir is DEPRECATED (SA1019) and, worse, it ignores build tags when it
	// associates files with packages. The files are read one by one instead, so the scan
	// sees exactly the sources the compiler sees.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package's directory: %v", err)
	}
	renderers := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
				continue
			}
			command := receiverTypeName(fn.Recv.List[0].Type)
			if command == "" {
				continue
			}
			if rendersStepReport(fn.Body) {
				renderers[command] = true
			}
		}
	}
	return renderers
}

// receiverTypeName names a method's receiver type ("CheckLiveCmd" for (*CheckLiveCmd).Run).
func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return receiverTypeName(t.X)
	case *ast.IndexListExpr:
		return receiverTypeName(t.X)
	}
	return ""
}

// rendersStepReport reports whether a function body renders a step report.
func rendersStepReport(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok || ident.Name != "reportSteps" {
			return true
		}
		found = true
		return true
	})
	return found
}

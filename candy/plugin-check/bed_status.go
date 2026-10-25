package check

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// bed_status.go — the VERDICT vocabulary of a bed run, and the per-check record behind it.
//
// The defect this file closes (opencharly/charly#904, filed in this repo as
// opencharly/plugin-check#132): a bed run whose DECLARED CHECK skipped reported
// `PASS (steps=N)`, exited 0, and wrote a summary.yml whose steps all read `ok: true` — so
// the acceptance record could not say that the run proved nothing. The skip was captured ONE
// LAYER BELOW the verdict (the engine's own rollup count, already in the record as
// `skipped: 1` on the step) and never reached it, because the run-level verdict was derived
// from a BOOLEAN.
//
// The rule this file states: a bed run has THREE verdicts, not two.
//
//	PASS — the run proved what it declared: a declared check ran and passed.
//	SKIP — the run completed, but proved NOTHING: no declared check ran and passed.
//	FAIL — a declared check failed, or the run could not complete.
//
// A skip is not a failure (R8: an absent service or credential skips VISIBLY and is never
// mocked) and it is not a pass (R7: a check that cannot run is not a check that passed). The
// exit code that says it is CheckSkippedExitCode (3) — the code this plugin already reserves
// for "not a verdict" — which the org's own scripts/check-bed-watch.sh already classifies as
// "a SKIP, never a pass".
//
// THREE THINGS THE VERDICT DOES NOT SAY, and why:
//
//  1. A PASS is not "no check skipped". A plan may declare a check that does not apply to the
//     substrate it runs on: `skip: true` and `exclude_distro:` are documented authoring
//     features (layer-charly-check/charly.yml:1902-1903), and such a check resolves to SKIP BY
//     THE AUTHOR'S DESIGN. The dev-tools image alone declares three `exclude_distro:` checks
//     (fastfetch-binary, gopls-binary, golangci-lint-binary — layer-dev-tools/charly.yml:
//     142-166), so every bed that runs it on ubuntu, debian or fedora carries declared checks
//     that resolve to SKIP. Reading those as a skipping RUN would make every such bed a
//     non-verdict and would contradict the authoring contract ("reported but doesn't fail the
//     run").
//     So the verdict reads what the run PROVED, and the record carries what did not run: the
//     per-check statuses — so the STRICT acceptance predicate ("every declared check id
//     resolved to PASS, and no declared check id resolved to SKIP") is checkable FROM THE
//     RECORD — and the run's `skipped_checks:` count. A run that proved something and skipped
//     something says so in the headline, so it can never be read as a bare PASS.
//  2. A FAIL is not "a check did not run". A run that proved NOTHING is a SKIP, and only a
//     SKIP — the bed still completes and tears down as it would after a pass
//     (opencharly/plugin-check#132).
//  3. A PHASE that asserted nothing does not taint a run that proved something elsewhere. A
//     phase with no plan steps at all (opencharly/plugin-check#110) declares NO check id, so
//     the strict predicate is unaffected by it — it is recorded per step (its own
//     `status: SKIP` and its reason) and named in the headline, but the run's verdict reads
//     what the run PROVED. Measured on the #110 witness bed
//     (check-empty-resolution-witness): its own declared check PASSES, and its vacuous
//     member phase is recorded as a SKIP step beside it — the run is a PASS, exit 0, with the
//     vacuous phase named.

// The three verdict words, declared HERE so no other file grows its own spelling: the
// record, the headline and the roster all read these.
const (
	verdictPass = "PASS"
	verdictSkip = "SKIP"
	verdictFail = "FAIL"
)

// checkOutcome is ONE declared check's outcome, as the child's own report stated it.
type checkOutcome struct {
	// Status is the report's status word: PASS, FAIL, SKIP or INFRA.
	Status string
	// ID is the check's step id — the identity an acceptance predicate names ("every
	// declared check id resolved to PASS, and no declared check id resolved to SKIP"). It
	// may be empty: the report prints the id bracket whatever the id is.
	ID string
	// Text is the check's authored text, as the report printed it.
	Text string
	// Reason is the message the report carried — for a SKIP, why the check did not run.
	Reason string
}

// checkOutcomeRe matches ONE line of the engine's text report.
//
// The report's shape is spec/report's renderStep — "  %-5s %s %s  [%s]%s%s" — so a line is:
// two spaces, the status word padded to five, the step's keyword, its text, TWO spaces, the id
// in brackets, then the message. The bed runner already reads this same text for the rollup
// count (skippedFromRollup), so this is the same instrument on the same document.
//
// The status word is read BEFORE the id bracket is looked for, and the id bracket is matched
// after a NON-GREEDY text, so the FIRST "  [" wins. A check whose TEXT contains two spaces
// and a bracket is therefore split at that bracket: the id in the record is then wrong, while
// the STATUS — the word the verdict is built from — is still right. The bound is stated rather
// than papered over: a wrong id misnames one check, a missed status would mislabel the run.
var checkOutcomeRe = regexp.MustCompile(`^ {2}(PASS|FAIL|SKIP|INFRA) +([A-Za-z][A-Za-z0-9_-]*) +(.*?) {2}\[([^\[\]]*)\](.*)$`)

// reportRollupRe matches the report's own rollup line — "N step(s): X passed, Y failed, Z
// skipped" (spec/report's FormatStepResultsText). It is the ONE line that says a plan ran
// at all, and it is matched WHOLE (anchored, one line) so a log that merely mentions
// "skipped" cannot be mistaken for a report.
//
// The step COUNT is captured, because the rollup is the report's own statement of how many
// steps it printed — the one self-check the document offers. A report's step lines must
// agree with it, and a block that does not is carrying text the report did not print
// (opencharly/plugin-check#134).
var reportRollupRe = regexp.MustCompile(`(?m)^(\d+) steps?: \d+ passed, \d+ failed, \d+ skipped$`)

// isCheckKeyword reports whether a plan step ASSERTED.
//
// The keywords are spec's StepKeywords: check, run, agent-check, agent-run, include. The
// two that assert are check and agent-check; the others mutate (run, agent-run) or are
// structural (include). `charly check live` runs in VERIFY-ONLY mode, which skips the
// mutating steps BY DESIGN ("skipped — verify-only mode (mutating step)",
// sdk/kit/planrun.go) — so counting those as skipped checks would report every bed whose
// plan carries a run: step as a skip. The verdict reads the ASSERTING steps only.
func isCheckKeyword(keyword string) bool {
	return keyword == "check" || keyword == "agent-check"
}

// checkReport is what a child's own report said, read out of the step's log text.
type checkReport struct {
	// Present is true when the log carried a REPORT — the child's plan ran and said so.
	// A log without one (a build step's output, a deploy trace) carries no check signal
	// at all, and the absence is NOT a claim that a check was skipped.
	Present bool
	// Readable is true when the report's step lines agree with the rollup the report
	// printed beside them. A block that disagrees is carrying text the report did not
	// print, so its per-check lines are not the report's own and NOTHING is credited from
	// it (opencharly/plugin-check#134).
	Readable bool
	// Printed is how many report-shaped lines the block carried; Steps is the count the
	// rollup itself printed. They are equal in a readable report, and the difference is
	// what an unreadable one says out loud.
	Printed int
	Steps   int
	// Checks are the child's ASSERTING steps — its declared checks — with the status each
	// one resolved to. Populated only for a READABLE report.
	Checks []checkOutcome
}

// planChildReporters maps the child command a bed run launches to the command in this
// package that renders that child's report. It is the ONE place that says which children
// print a report.
//
// A REPORT is what a PLAN runner prints: the per-check outcomes of a resolved plan. Every
// other child in a bed run — a build, a deploy, an update, a teardown — prints none, so
// report-shaped text in its log is CONTENT, not a verdict.
//
// The runner knows which child it launched (the argv it passed), so the report is read only
// from a plan child's log. Without that rule a container build whose output ends with a
// self-consistent report-shaped block banks a PASS for a run whose plan proved NOTHING —
// the same false PASS this file exists to close (opencharly/plugin-check#134). MEASURED:
// such a block is credited, and the run reads PASS while its plan step proved nothing.
//
// This is the rule that can be stated over a TEXT log today. The root fix — a report that
// is not text at all, so the verdict reads the child's step RESULTS — is opencharly/plugin-check#135.
//
// The table is held against the source by TestEveryReportRendererIsAPlanChild: every
// command in this package that renders a report must be in it, and every entry in it must
// render one.
var planChildReporters = map[string]string{
	"check live":        "CheckLiveCmd",
	"check box":         "CheckBoxCmd",
	"check feature run": "CheckFeatureRunCmd",
}

// planChildReportKey names the entry of planChildReporters a step's child argv names, or ""
// when the child is not a plan runner.
//
// The key is built from the argv the runner launched, and the TABLE decides — the argv
// alone never grants a report. `charly check run` (the bed runner itself) and
// `charly check plan` are `check` verbs that print no report, and they are not in the
// table.
func planChildReportKey(argv []string) string {
	if len(argv) < 2 || argv[0] != "check" {
		return ""
	}
	if argv[1] == "feature" {
		if len(argv) < 3 {
			return ""
		}
		return "check feature " + argv[2]
	}
	return "check " + argv[1]
}

// childReport reads the child's own report out of its log, and the inner-step skip count
// the report carries.
//
// ONLY a plan child's report is read: a build's, a deploy's or a teardown's output is
// CONTENT, and content must never bank a verdict (opencharly/plugin-check#134; the
// remaining root fix, where the report is not text at all, is opencharly/plugin-check#135).
// A non-plan child therefore carries no report and no inner skip count — the same zero it
// carried before, because those logs never printed a report.
//
// The log is the child's MERGED output (the cli seam captures the two streams into one
// text, a whole line at a time), so the report is the LAST block in it: the engine renders
// the report last — live_cmd.go after any forwarded guest output, feature_cmd.go after the
// guest's own output — and the seam reunites the streams in the order they were written.
func childReport(argv []string, logText string) (checkReport, int) {
	// The argv names the child; the TABLE says whether that child prints a report. An argv
	// that names no entry — a build, a deploy, a teardown, `charly check run` itself — is
	// not a plan child, and its output carries no report.
	if _, ok := planChildReporters[planChildReportKey(argv)]; !ok {
		return checkReport{}, 0
	}
	return parseCheckReport(logText), skippedFromRollup(logText)
}

// parseCheckReport reads a child's OWN report out of a step's log text.
//
// THE CHILD'S OWN REPORT is the LAST report the child printed: the engine renders the
// report LAST (live_cmd.go renders it after any forwarded guest output; feature_cmd.go
// renders it to stdout), so the last rollup in the child's log is the report the child
// printed, and the per-check lines are the ones in the block before it.
//
// The log is the child's MERGED output: the cli seam captures the child's stdout and stderr
// into ONE text, reunited a whole line at a time, so a report cannot be told apart by the
// stream it was written to — and must not be, because the runner has no other view of the
// child. (childReport reads that merged log, and only for a plan child.)
//
// A log with NO rollup carries NO report and therefore NO check outcome. That is the
// difference between a verdict and a guess: a build step's output, a deploy trace, or any
// other non-plan log must not be able to credit a PASS to a run that proved NOTHING
// (opencharly/charly#904). Before this rule the reader collected every report-SHAPED line
// in the log, and the run-level counters counted them without asking whether a report had
// been printed at all.
//
// A report whose step lines do NOT agree with its own rollup is not read: the rollup says
// "N steps" and the block must carry N step lines. A block that does not is carrying text
// the report did not print — a step's MESSAGE with a report-shaped line inside it, which
// the engine's truncate() permits because it bounds a message's length but does not flatten
// its newlines. Such a report credits NOTHING, and the phase says so.
func parseCheckReport(logText string) checkReport {
	lines := strings.Split(logText, "\n")
	// The child's own report is the LAST one it printed.
	last := -1
	steps := 0
	for i, line := range lines {
		m := reportRollupRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		last = i
		steps, _ = strconv.Atoi(m[1])
	}
	if last < 0 {
		// No report: no check outcome. The absence is not a claim that a check was skipped.
		return checkReport{}
	}
	// The report's step lines are the ones between the PREVIOUS rollup and this one.
	start := 0
	for i := last - 1; i >= 0; i-- {
		if reportRollupRe.MatchString(lines[i]) {
			start = i + 1
			break
		}
	}
	rep := checkReport{Present: true, Steps: steps}
	for _, line := range lines[start:last] {
		m := checkOutcomeRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		rep.Printed++
		if !isCheckKeyword(m[2]) {
			continue
		}
		rep.Checks = append(rep.Checks, checkOutcome{
			Status: m[1],
			Text:   strings.TrimSpace(m[3]),
			ID:     m[4],
			Reason: strings.TrimSpace(m[5]),
		})
	}
	if rep.Printed != rep.Steps {
		// The block does not agree with the report's own count, so its per-check lines are
		// not the report's own. Nothing is credited from it.
		rep.Checks = nil
		return rep
	}
	rep.Readable = true
	return rep
}

// checkReportVerdict is the verdict a child's report implies, and the reason for it.
//
//	FAIL — the phase's report says a declared check FAILED.
//	SKIP — the phase asserted NOTHING: no declared check passed (or the report could not
//	       be read, so nothing can be credited from it).
//	PASS — the phase ASSERTED something: at least one declared check ran and passed.
//
// The skip is NOT "one declared check among many did not run". A declared check that does
// not apply to this substrate resolves to SKIP BY THE AUTHOR'S DESIGN (`skip: true`,
// `exclude_distro:` — layer-charly-check/charly.yml:1902-1903), and the org's own images
// carry such checks (layer-dev-tools/charly.yml:142-166 declares three `exclude_distro:`
// checks). Reading that as a skipping PHASE would make every bed that runs such an image a
// non-verdict — a false SKIP, exactly as wrong as the false PASS this change closes.
//
// So the phase's verdict reads what the phase PROVED, and the checks that did not run are
// recorded PER CHECK (the acceptance predicate reads the record: "every declared check id
// resolved to PASS, and no declared check id resolved to SKIP"). A phase whose report proves
// NOTHING — every declared check skipped, or the report carries no declared check at all —
// is a SKIP: a check that cannot run is not a check that passed (R7).
func checkReportVerdict(rep checkReport) (string, string) {
	if !rep.Present {
		// No report: the phase has nothing to say about itself. The RUN's verdict is
		// decided by what the run proved, which is what the reports say.
		return verdictPass, ""
	}
	if !rep.Readable {
		// A report that does not agree with itself cannot say what the run proved, so
		// nothing is credited from it (opencharly/plugin-check#134).
		return verdictSkip, fmt.Sprintf("the child's report could not be read: %d step line(s) against the %d step(s) its own rollup counted",
			rep.Printed, rep.Steps)
	}
	var skipped []checkOutcome
	var failed []checkOutcome
	passed := 0
	for _, c := range rep.Checks {
		switch c.Status {
		case verdictSkip:
			skipped = append(skipped, c)
		case verdictFail:
			failed = append(failed, c)
		case verdictPass:
			passed++
		}
	}
	if len(failed) > 0 {
		// A declared check FAILED. The report and the exit code are derived from the same
		// step results, so a child that honoured its contract already exited 2 and this
		// branch is unreachable for it. It closes the case where a child exits 0 and its
		// own report says otherwise — where the record would carry `status: FAIL` for a
		// check while the run read PASS (opencharly/plugin-check#134).
		return verdictFail, fmt.Sprintf("declared check %s failed: %s", checkID(failed[0]), checkReason(failed[0]))
	}
	if passed > 0 {
		return verdictPass, ""
	}
	if len(skipped) > 0 {
		return verdictSkip, fmt.Sprintf("declared check %s did not run: %s", checkID(skipped[0]), checkReason(skipped[0]))
	}
	return verdictSkip, "the child's report carries no passing check: it asserted nothing"
}

// checkID names a declared check: its id, or its text when the report printed no id.
func checkID(c checkOutcome) string {
	if c.ID != "" {
		return c.ID
	}
	return c.Text
}

// checkReason is the message the report carried for a check, or an honest stand-in.
func checkReason(c checkOutcome) string {
	if c.Reason != "" {
		return c.Reason
	}
	return "no reason given"
}

// stepVerdictFromReport applies a child's own report to its step, and returns the failure
// sentence when the report says a declared check FAILED.
//
// A SKIP sets the step's skip reason — the step completed but proved nothing, and the
// reason is what tells a reader that (opencharly/plugin-check#132). A FAIL is returned as
// the sentence, because the step FAILED: the report and the exit code come from the same
// step results, so a child that honoured its contract already exited 2, and this closes
// the case where a child exits 0 and its own report says otherwise
// (opencharly/plugin-check#134). A PASS is no objection: the step carries on.
func stepVerdictFromReport(rep checkReport, sk *stepResult) string {
	switch verdict, reason := checkReportVerdict(rep); verdict {
	case verdictSkip:
		sk.SkipReason = reason
	case verdictFail:
		sk.OK = false
		return reason
	}
	return ""
}

// stepStatus is the STEP's verdict.
//
//	FAIL — the step failed (the child failed, or its log carried a failure).
//	SKIP — the step completed but asserted nothing: the child's phase resolved NOTHING
//	        (opencharly/plugin-check#110), or it ran checks and NONE of them passed
//	        (opencharly/plugin-check#132).
//	PASS — the step asserted something and it held.
//
// The skip reason is carried by the step itself (stepResult.SkipReason), so the record, the
// headline and the roster all quote the SAME sentence.
func stepStatus(s *stepResult) string {
	if s == nil {
		return verdictFail
	}
	if !s.OK {
		return verdictFail
	}
	if s.SkipReason != "" {
		return verdictSkip
	}
	return verdictPass
}

// runStatus is the RUN's verdict.
//
//	FAIL — a step failed, or the run could not complete.
//	SKIP — the run completed, but PROVED NOTHING: no declared check ran and passed.
//	PASS — the run proved what it declared: a declared check ran and passed.
//
// The verdict reads the CHECK OUTCOMES, not the steps' own skip flags: a phase that asserted
// nothing declares NO check id (opencharly/plugin-check#110), so it cannot unprove a check
// another phase proved. It is still recorded — its step carries `status: SKIP` and its
// reason, and the headline names it — but the run's verdict reads what the run PROVED.
//
// A prereq-skipped bed is a SKIP: the bed never ran, so it proved nothing (its own
// SkipReason says why). It is NOT a failure — the host prerequisite is absent, which is a
// deliberate, documented non-run.
//
// A FAIL always wins over a SKIP: a run that failed is a FAIL whatever else it did.
func runStatus(res *bedRunResult) string {
	if res == nil {
		return verdictFail
	}
	if !res.OK {
		return verdictFail
	}
	if res.SkippedPrereq {
		return verdictSkip
	}
	if runPassedChecks(res) > 0 {
		return verdictPass
	}
	return verdictSkip
}

// runPassedChecks is how many declared checks across the run resolved to PASS — the question
// the run's verdict asks: did the run prove anything it declared?
//
// The counts read the steps' checks, and a step carries checks ONLY when its own report was
// READABLE (parseCheckReport credits nothing out of a log that printed no report, and
// nothing out of a report that disagrees with its own rollup). So a PASS can only ever be
// credited from a report the child actually printed and that agrees with itself.
func runPassedChecks(res *bedRunResult) int {
	if res == nil {
		return 0
	}
	n := 0
	for i := range res.Step {
		for _, c := range res.Step[i].Checks {
			if c.Status == verdictPass {
				n++
			}
		}
	}
	return n
}

// runSkipReason names WHY the run skipped: every skipping step's reason, prefixed with the
// step's name, so the headline and the error carry a sentence rather than a bare word.
func runSkipReason(res *bedRunResult) string {
	if res == nil {
		return ""
	}
	if res.SkippedPrereq {
		return res.SkipReason
	}
	var parts []string
	for i := range res.Step {
		s := &res.Step[i]
		if stepStatus(s) == verdictSkip && s.SkipReason != "" {
			parts = append(parts, fmt.Sprintf("%s: %s", s.Name, s.SkipReason))
		}
	}
	return strings.Join(parts, "; ")
}

// runSkippedChecks is how many declared checks across the run resolved to SKIP — the number
// the record carries and the headline quotes. It counts CHECKS, not steps: a run step that
// verify-only mode skipped by design is not a skipped check.
func runSkippedChecks(res *bedRunResult) int {
	if res == nil {
		return 0
	}
	n := 0
	for i := range res.Step {
		for _, c := range res.Step[i].Checks {
			if c.Status == verdictSkip {
				n++
			}
		}
	}
	return n
}

// runSkipDetail is the sentence the headline carries AFTER the verdict.
//
// For a SKIP run it is the reason the run proved nothing ("<step>: <reason>"). For a PASS
// run it NAMES what did not run — every declared check that resolved to SKIP, with the reason
// the child's report gave, and every phase that asserted nothing — so the headline can never
// be read as a bare PASS while something did not run (opencharly/charly#904). Empty when
// there is nothing to say.
func runSkipDetail(res *bedRunResult) string {
	if res == nil {
		return ""
	}
	if runStatus(res) == verdictSkip {
		if reason := runSkipReason(res); reason != "" {
			return reason
		}
		return "the run asserted nothing"
	}
	var parts []string
	for i := range res.Step {
		s := &res.Step[i]
		for _, c := range s.Checks {
			if c.Status != verdictSkip {
				continue
			}
			reason := c.Reason
			if reason == "" {
				reason = "no reason given"
			}
			id := c.ID
			if id == "" {
				id = c.Text
			}
			parts = append(parts, fmt.Sprintf("%s: declared check %s did not run: %s", s.Name, id, reason))
		}
		// A phase that asserted NOTHING declares no check id, so it is named here rather
		// than as a skipped check: it is a phase, not a check that did not run.
		if len(s.Checks) == 0 && s.SkipReason != "" {
			parts = append(parts, fmt.Sprintf("%s: asserted nothing: %s", s.Name, s.SkipReason))
		}
	}
	return strings.Join(parts, "; ")
}

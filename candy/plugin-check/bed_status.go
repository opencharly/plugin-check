package check

import (
	"fmt"
	"regexp"
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
var reportRollupRe = regexp.MustCompile(`(?m)^\d+ steps?: \d+ passed, \d+ failed, \d+ skipped$`)

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
	// Checks are the child's ASSERTING steps — its declared checks — with the status each
	// one resolved to.
	Checks []checkOutcome
}

// parseCheckReport reads a child's report out of a step's log text.
//
// It returns the report's presence and its declared checks. Lines that are not report
// lines are ignored: the log also carries the child's own noise (resolution logs, deploy
// traces), and none of that is a check outcome.
func parseCheckReport(logText string) checkReport {
	rep := checkReport{Present: reportRollupRe.MatchString(logText)}
	for _, line := range strings.Split(logText, "\n") {
		m := checkOutcomeRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
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
	return rep
}

// checkReportVerdict is the verdict a child's report implies, and the reason for it.
//
//	PASS — the phase ASSERTED something: at least one declared check ran and passed.
//	SKIP — the phase asserted NOTHING: no declared check passed.
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
		return verdictPass, ""
	}
	var skipped []checkOutcome
	passed := 0
	for _, c := range rep.Checks {
		switch c.Status {
		case verdictSkip:
			skipped = append(skipped, c)
		case verdictPass:
			passed++
		}
	}
	if passed > 0 {
		return verdictPass, ""
	}
	if len(skipped) > 0 {
		reason := skipped[0].Reason
		if reason == "" {
			reason = "no reason given"
		}
		id := skipped[0].ID
		if id == "" {
			id = skipped[0].Text
		}
		return verdictSkip, fmt.Sprintf("declared check %s did not run: %s", id, reason)
	}
	return verdictSkip, "the child's report carries no passing check: it asserted nothing"
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

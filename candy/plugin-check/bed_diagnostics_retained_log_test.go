package check

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// The retained-log counterpart of TestPacmanMirrorAbandonedTransactionAllowance. That test
// pins the entry's SEMANTICS over rows written for it; this one pins the CLASSIFICATION
// CHANGE over the real bytes of the run that produced the defect: over THIS excerpt the
// sentence is the single un-allowlisted finding before the entry (errors 0, warnings 1,
// allowlisted 7) and is claimed after it (errors 0, warnings 0, allowlisted 8) -- the
// 1 -> 0 warning change the retained image-build step reported, reproducible from this tree
// alone (`go test ./...`) rather than from a driver that was run once and never committed.
// The excerpt is TRIMMED, so the allowlisted totals asserted here (7 -> 8) are the excerpt's,
// not the whole image-build step log's 58 -> 59: those full-log numbers are the retained
// summary's provenance (quoted where retainedExcerptPath is described) and are deliberately
// not asserted over the excerpt.

const (
	// retainedExcerptPath is a trimmed excerpt of the retained bed log
	// .check/check-cachyos-immich-ml-pod/2026.255.0001/image-build.log, whose summary.yml
	// reported errors: 0, warnings: 1, allowlisted: 58 with the mirror-abandoned sentence as
	// the single un-allowlisted finding.
	retainedExcerptPath = "testdata/pacman-mirror-abandoned/image-build-excerpt.log"

	// mirrorAbandonedID is the allowance under test.
	mirrorAbandonedID = "pacman-mirror-abandoned-transaction-recovered"

	// retainedMirrorSentence is the verbatim line the retained run reported un-allowlisted at
	// line 1461.
	retainedMirrorSentence = "warning: too many errors from cdn77.cachyos.org, skipping for the remainder of this transaction"
)

// retainedRetrievalFailures are the seven `error: failed retrieving file ...` lines the same
// run emitted (three in transaction 1, two in transaction 2, two in transaction 3 -- the last
// pair one per mirror). They are already exempt through pacman-mirror-retrieval-recovered,
// which is why the excerpt must keep them: the point of the change is that the SENTENCE
// summarizing them stayed counted while they did not.
var retainedRetrievalFailures = []string{
	"error: failed retrieving file 'glibc-2.44+r24+g16be1518495f-1-x86_64_v3.pkg.tar.zst' from cdn77.cachyos.org : The requested URL returned error: 404",
	"error: failed retrieving file 'gcc-16.2.1+r23+gd564253eb6c8-1-x86_64_v3.pkg.tar.zst' from cdn77.cachyos.org : The requested URL returned error: 404",
	"error: failed retrieving file 'libgfortran-16.2.1+r23+gd564253eb6c8-1-x86_64_v3.pkg.tar.zst' from cdn77.cachyos.org : The requested URL returned error: 404",
	"error: failed retrieving file 'libpulse-17.0+r98+gb096704c0-1.1-x86_64_v3.pkg.tar.zst' from cdn77.cachyos.org : The requested URL returned error: 404",
	"error: failed retrieving file 'libasyncns-1:0.8+r3+g68cd5af-3.2-x86_64_v3.pkg.tar.zst' from cdn77.cachyos.org : The requested URL returned error: 404",
	"error: failed retrieving file 'libyuv-r2921+644251f25-1.1-x86_64_v3.pkg.tar.zst' from cdn77.cachyos.org : The requested URL returned error: 404",
	"error: failed retrieving file 'libyuv-r2921+644251f25-1.1-x86_64_v3.pkg.tar.zst' from us.cachyos.org : The requested URL returned error: 404",
}

func TestPacmanMirrorAbandonedTransactionOnRetainedLog(t *testing.T) {
	raw, err := os.ReadFile(retainedExcerptPath)
	if err != nil {
		t.Fatalf("the committed retained-log excerpt must be readable: %v", err)
	}
	log := string(raw)

	// Provenance guards first: the excerpt must still carry the run's OWN lines. A future
	// edit that swapped them for a paraphrase which happens to pass would be a different,
	// unreviewed claim.
	for _, want := range append([]string{retainedMirrorSentence, "checking keyring..."}, retainedRetrievalFailures...) {
		if !strings.Contains(log, want) {
			t.Errorf("the committed excerpt no longer carries a line of the retained run: %q", want)
		}
	}

	// AFTER: the entry claims the sentence, so the retained step scans clean -- the same
	// accounting the fresh bed run must report.
	after := scanStepDiagnostics(log)
	if after.Errors != 0 || after.Warnings != 0 {
		t.Errorf("the retained step must scan clean with the entry: errors=%d warnings=%d",
			after.Errors, after.Warnings)
	}
	if after.Allowlisted != len(retainedRetrievalFailures)+1 {
		t.Errorf("allowlisted = %d, want %d (the seven retrieval failures plus the sentence)",
			after.Allowlisted, len(retainedRetrievalFailures)+1)
	}
	var claimed []string
	for _, f := range after.Findings {
		if f.AllowID == mirrorAbandonedID {
			claimed = append(claimed, f.Text)
		}
	}
	if !slices.Equal(claimed, []string{retainedMirrorSentence}) {
		t.Errorf("the entry must claim the sentence and nothing else; claimed %q", claimed)
	}

	// BEFORE: the same bytes against the allowlist without the entry -- the pre-change list.
	// This is where the two numbers come from, and they must match the retained summary.yml:
	// warnings 1, errors 0, allowlisted 58 (of which these seven are the retrieval failures).
	before := scanStepDiagnosticsWithoutEntry(mirrorAbandonedID, log)
	if before.Errors != 0 || before.Warnings != 1 {
		t.Errorf("without the entry the retained step must report exactly the one warning it "+
			"did: errors=%d warnings=%d", before.Errors, before.Warnings)
	}
	if before.Allowlisted != len(retainedRetrievalFailures) {
		t.Errorf("without the entry allowlisted = %d, want %d", before.Allowlisted, len(retainedRetrievalFailures))
	}
	var unclaimed []string
	for _, f := range before.Findings {
		if f.AllowID == "" {
			unclaimed = append(unclaimed, f.Text)
		}
	}
	if !slices.Equal(unclaimed, []string{retainedMirrorSentence}) {
		t.Errorf("the one un-allowlisted line must be the sentence; got %q", unclaimed)
	}
}

// scanStepDiagnosticsWithoutEntry runs the SAME scan with one allowlist entry removed, so the
// "before" half of a classification change is measured over the same bytes rather than
// asserted in prose. The list is restored before returning.
func scanStepDiagnosticsWithoutEntry(id, log string) stepDiagnostics {
	saved := diagnosticAllowlist
	rest := make([]diagnosticAllowance, 0, len(saved))
	for _, a := range saved {
		if a.ID != id {
			rest = append(rest, a)
		}
	}
	diagnosticAllowlist = rest
	defer func() { diagnosticAllowlist = saved }()
	return scanStepDiagnostics(log)
}

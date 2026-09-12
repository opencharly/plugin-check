package check

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// TestPacmanMirrorAbandonedTransactionOnReproducerLog re-derives the pacman-mirror-abandoned
// classification from a log a reviewer can REGENERATE, which is what the earlier proof could not
// do.
//
// The log it scans is the COMPLETE stdout+stderr of the documented invocation of
// testdata/pacman-mirror-abandoned/reproducer/charly.yml — a self-contained project committed in
// this tree (its README.md carries the command, the calvers and the log sha256). Nothing about
// the run lives in /tmp, so the sentence is no longer "reproduced once by a driver nobody kept":
// re-running the command re-derives these bytes, and this test re-derives the classification
// from them.
func TestPacmanMirrorAbandonedTransactionOnReproducerLog(t *testing.T) {
	const logPath = "testdata/pacman-mirror-abandoned/reproducer/build.log"
	const id = "pacman-mirror-abandoned-transaction-recovered"

	// The two sentences pacman prints in the committed log — one per server it abandons.
	sentences := []string{
		"warning: too many errors from 127.0.0.1:1, skipping for the remainder of this transaction",
		"warning: too many errors from cdn77.cachyos.org, skipping for the remainder of this transaction",
	}

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("the committed reproducer log must be readable: %v", err)
	}
	log := string(raw)

	// Provenance guards first. These are the run's OWN lines: the retrieval failure the dead
	// server causes for a NAMED package, the sentence pacman prints when it gives up on that
	// server, the stage it reaches only after every payload arrived, the progress line proving
	// the packages were installed, and — because the log is committed COMPLETE rather than as a
	// hand-picked excerpt — the build's own completion line.
	for _, want := range append([]string{
		"error: failed retrieving file 'glibc-2.44+r24+g16be1518495f-1-x86_64_v3.pkg.tar.zst' from 127.0.0.1:1 : Failed to connect to 127.0.0.1:1 after 0 ms: Could not connect to server",
		"checking keyring...",
		"upgrading glibc...",
		"Successfully tagged localhost/mirror-repro:",
	}, sentences...) {
		if !strings.Contains(log, want) {
			t.Errorf("the committed reproducer log no longer carries a line of the run it documents: %q", want)
		}
	}

	// AFTER: the entry claims both sentences, so the reproducer's build scans clean — the
	// classification change measured over the bytes rather than asserted in prose.
	after := scanStepDiagnostics(log)
	if after.Errors != 0 || after.Warnings != 0 {
		t.Errorf("the reproducer's build must scan clean with the entry: errors=%d warnings=%d",
			after.Errors, after.Warnings)
	}
	var claimed []string
	for _, f := range after.Findings {
		if f.AllowID == id {
			claimed = append(claimed, f.Text)
		}
	}
	if !slices.Equal(claimed, sentences) {
		t.Errorf("the entry must claim exactly the two abandonment sentences; claimed %q", claimed)
	}
	// 1 dev-build notice + 20 conditional retrieval errors + the two sentences.
	if after.Allowlisted != 23 {
		t.Errorf("allowlisted = %d, want 23", after.Allowlisted)
	}

	// BEFORE: the same bytes against the allowlist without the entry — the pre-change list.
	// This is where the 2 -> 0 warning change comes from, and where the retained summary's
	// single un-allowlisted warning came from on the original bed run.
	before := scanStepDiagnosticsWithoutEntry(id, log)
	if before.Errors != 0 || before.Warnings != len(sentences) {
		t.Errorf("without the entry the reproducer's build must report exactly the two abandonment "+
			"warnings: errors=%d warnings=%d", before.Errors, before.Warnings)
	}
	if before.Allowlisted != 21 {
		t.Errorf("without the entry allowlisted = %d, want 21", before.Allowlisted)
	}
	var unclaimed []string
	for _, f := range before.Findings {
		if f.AllowID == "" {
			unclaimed = append(unclaimed, f.Text)
		}
	}
	if !slices.Equal(unclaimed, sentences) {
		t.Errorf("the un-allowlisted lines must be exactly the two sentences; got %q", unclaimed)
	}
}

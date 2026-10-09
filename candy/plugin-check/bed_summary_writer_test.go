package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestWriteBedSummaryEmitsSkipped pins the WRITER, not only the parser (opencharly/plugin-check#99
// review). TestSkippedFromRollup covers skippedFromRollup; nothing covered the line that puts the count
// into the record, so deleting the emission would have failed no test. This one drives writeBedSummary
// itself and reads the file it wrote.
func TestWriteBedSummaryEmitsSkipped(t *testing.T) {
	dir := t.TempDir()
	res := &bedRunResult{
		Bed:    "witness-bed",
		CalVer: "2026.282.0000",
		Step: []stepResult{
			{Name: "check-live", Duration: time.Second, OK: true, Skipped: 2},
			{Name: "cleanup", Duration: time.Second, OK: true, Skipped: 0},
		},
		OK: true,
	}
	writeBedSummary(dir, res)

	b, err := os.ReadFile(filepath.Join(dir, "summary.yml"))
	if err != nil {
		t.Fatalf("writeBedSummary did not write summary.yml: %v", err)
	}
	out := string(b)

	// The count must be there for the step that skipped, and it must be distinguishable from the step
	// that skipped nothing — otherwise "nothing skipped" and "this record predates the field" read alike.
	if !strings.Contains(out, "name: check-live\n    duration_seconds: 1\n    ok: true\n    skipped: 2\n") {
		t.Errorf("the skipping step's record is missing its skipped count:\n%s", out)
	}
	if !strings.Contains(out, "name: cleanup\n    duration_seconds: 1\n    ok: true\n    skipped: 0\n") {
		t.Errorf("a step that skipped nothing must still say so:\n%s", out)
	}
}

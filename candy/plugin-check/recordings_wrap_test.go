package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteRecordingsIncludesWrapLog proves the whole-run wrap stop output (the
// wrap's own record: stop log) lands in recordings.yml next to summary.yml —
// G5 discoverability for the whole-run .cast.
func TestWriteRecordingsIncludesWrapLog(t *testing.T) {
	dir := t.TempDir()
	wrapStop := "Recording stopped (mode: terminal); saved 422 bytes to " + filepath.Join(dir, "whole-run.cast") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "record-wrap-stop.log"), []byte(wrapStop), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "whole-run.cast"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeRecordingsManifest(dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "recordings.yml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "whole-run.cast") {
		t.Fatalf("manifest missing whole-run entry:\n%s", s)
	}
	if !strings.Contains(s, "survived_teardown: true") {
		t.Fatalf("whole-run survival not asserted:\n%s", s)
	}
}

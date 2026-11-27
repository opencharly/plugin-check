package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteFileAtomic_CompletesOrRefuses — the summary must be all-or-nothing
// (opencharly/charly#1308). A successful write is complete and leaves no temp file; a write
// into an unwritable directory returns an error and leaves no partial artifact behind.
func TestWriteFileAtomic_CompletesOrRefuses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "summary.yml")
	data := []byte("bed: x\ncalver: 2026.331.0000\n")

	if err := writeFileAtomic(path, data); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(data) {
		t.Fatalf("the written document is not the one requested: %q (%v)", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "summary.yml" && strings.Contains(e.Name(), ".tmp.") {
			t.Errorf("a temp file survived a successful write: %s", e.Name())
		}
	}

	// An unwritable directory: the error must surface and the target must not appear.
	// A root CI container bypasses the mode bits, so the arm would succeed there and this
	// test would fail for the environment rather than for the code — skip visibly instead
	// (the invariant is still pinned by the "complete content + no temp residue" arm above).
	if os.Geteuid() == 0 {
		t.Log("running as root: mode bits do not refuse a write, skipping the refusal arm")
		return
	}
	ro := filepath.Join(dir, "ro")
	if err := os.Mkdir(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(ro, "summary.yml")
	if err := writeFileAtomic(target, data); err == nil {
		t.Fatal("a write into an unwritable directory must return its error, not swallow it")
	}
	if _, err := os.Stat(target); err == nil {
		t.Fatal("a refused write must not leave a partial document at the target path")
	}
}

package check

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestWriteFileAtomic_SurvivesAShortWrite reproduces the PRODUCTION failure — a write that
// fails AFTER the file is open (ENOSPC in the field; EFBIG here via RLIMIT_FSIZE) — and
// asserts the property that makes the fix real: the previous document is still intact.
//
// The child process is used because RLIMIT_FSIZE is per-process (and the Go test runner
// writes to stdout). This test FAILS on the old code path: `os.WriteFile` opens the target
// with O_TRUNC, so the first oversized write leaves a truncated file, and the parent's
// "previous document is intact" assertion fails.
func TestWriteFileAtomic_SurvivesAShortWrite(t *testing.T) {
	if os.Getenv("PLUGIN_CHECK_ATOMIC_CHILD") == "1" {
		signal.Ignore(syscall.SIGXFSZ) // EFBIG, not a killed process
		lim := syscall.Rlimit{Cur: 512, Max: 512}
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &lim); err != nil {
			os.Exit(4)
		}
		target := os.Getenv("PLUGIN_CHECK_ATOMIC_TARGET")
		var err error
		if os.Getenv("PLUGIN_CHECK_ATOMIC_OLD") == "1" {
			err = os.WriteFile(target, make([]byte, 8192), 0o644) // the OLD call, for the red arm
		} else {
			err = writeFileAtomic(target, make([]byte, 8192))
		}
		if err == nil {
			os.Exit(3) // the short write was NOT reported
		}
		os.Exit(0)
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "summary.yml")
	const oldDoc = "bed: old\nstatus: PASS\n"
	if err := os.WriteFile(target, []byte(oldDoc), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PLUGIN_CHECK_ATOMIC_CHILD", "1")
	t.Setenv("PLUGIN_CHECK_ATOMIC_TARGET", target)
	// Re-exec this test binary as the size-limited child and WAIT for it.
	cmd := exec.Command(os.Args[0], "-test.run", "TestWriteFileAtomic_SurvivesAShortWrite", "-test.v")
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		t.Fatalf("the child did not complete (a silent short write?): %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != oldDoc {
		t.Fatalf("the previous document was destroyed by a short write:\n got %q\nwant %q", got, oldDoc)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") {
			t.Errorf("a temp file survived a failed write: %s", e.Name())
		}
	}
}

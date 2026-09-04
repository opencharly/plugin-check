package check

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestAutoProvisionBaseGolden_ScansStepLog proves the runner's step error is the
// subcommand SUMMARY and the missing-golden detail is recovered from the log file.
func TestAutoProvisionBaseGolden_ScansStepLog(t *testing.T) {
	orig := provisionBaseGoldenRun
	provisionBaseGoldenRun = func(base string) error { return nil }
	defer func() { provisionBaseGoldenRun = orig }()

	log := filepath.Join(t.TempDir(), "vm-build.log")
	body := fmt.Sprintf("error: vm %q: snapshot \"golden\" does not exist", "check-snap-probe")
	if err := os.WriteFile(log, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	err := errors.New("vm-build exited 1: charly subcommand exited 1; log: " + log)
	retry, perr := autoProvisionBaseGolden(err, map[string]bool{}, nil)
	if perr != nil {
		t.Fatalf("unexpected: %v", perr)
	}
	if !retry {
		t.Fatal("the log-scan must recover the missing-golden signature and request a retry")
	}
}

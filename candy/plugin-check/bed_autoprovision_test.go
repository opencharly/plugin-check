package check

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// TestAutoProvisionBaseGolden_ParsesMissingGolden proves the sdk clone-enforcement
// error is recognized and the BASE BED name extracted (the R4 auto-provision seam).
func TestAutoProvisionBaseGolden_ParsesMissingGolden(t *testing.T) {
	err := errors.New("vm \"check-charly-omarchy-vm\": snapshot \"golden\" does not exist; create with: charly vm snapshot create check-charly-omarchy-vm golden")
	visited := map[string]bool{}
	base, retry, perr := autoProvisionBaseGolden(err, visited, nil)
	if perr != nil {
		t.Fatalf("unexpected provision error: %v", perr)
	}
	if !retry {
		t.Fatal("missing-golden error must request a retry (auto-provision)")
	}
	if base != "check-charly-omarchy-vm" {
		t.Fatalf("the BASE BED (from_vm, check-* prefixed) must be extracted, got %q", base)
	}
	if !visited[base] {
		t.Fatal("the base bed must be marked visited")
	}
}

// TestAutoProvisionBaseGolden_NonCloneErrorPropagates: unrelated vm-build errors
// must NOT trigger auto-provisioning.
func TestAutoProvisionBaseGolden_NonCloneErrorPropagates(t *testing.T) {
	err := errors.New("no container engine found (install podman or docker)")
	_, retry, perr := autoProvisionBaseGolden(err, map[string]bool{}, nil)
	if retry || perr != nil {
		t.Fatalf("unrelated errors must not auto-provision (retry=%v perr=%v)", retry, perr)
	}
}

// TestAutoProvisionBaseGolden_CycleGuard: a clone whose base is itself a clone
// must not recurse forever.
func TestAutoProvisionBaseGolden_CycleGuard(t *testing.T) {
	err := errors.New("vm \"check-base-vm\": snapshot \"golden\" does not exist")
	visited := map[string]bool{"check-base-vm": true}
	if _, _, perr := autoProvisionBaseGolden(err, visited, nil); perr == nil || !strings.Contains(perr.Error(), "cycle") {
		t.Fatalf("expected a cycle error, got %v", perr)
	}
}

// TestAutoProvisionBaseGolden_RefusesNonDisposable: a base that is not disposable
// must be refused (R10 discipline).
func TestAutoProvisionBaseGolden_RefusesNonDisposable(t *testing.T) {
	err := errors.New("vm \"check-base-vm\": snapshot \"golden\" does not exist")
	_, _, perr := autoProvisionBaseGolden(err, map[string]bool{}, func(s string) bool { return false })
	if perr == nil || !strings.Contains(perr.Error(), "not disposable") {
		t.Fatalf("expected a disposable refusal, got %v", perr)
	}
}

// TestAutoProvisionBaseGolden_ScansStepLog proves the runner's step error is the
// subcommand SUMMARY and the missing-golden detail is recovered from the log file.
func TestAutoProvisionBaseGolden_ScansStepLog(t *testing.T) {
	log := t.TempDir() + "/vm-build.log"
	os.WriteFile(log, []byte("error: vm \"check-snap-probe\": snapshot \"golden\" does not exist"), 0o644)
	err := errors.New("vm-build exited 1: charly subcommand exited 1; log: " + log)
	base, retry, perr := autoProvisionBaseGolden(err, map[string]bool{}, nil)
	if perr != nil {
		t.Fatalf("unexpected: %v", perr)
	}
	if !retry || base != "check-snap-probe" {
		t.Fatalf("the log-scan must recover the missing-golden signature (base=%q retry=%v)", base, retry)
	}
}

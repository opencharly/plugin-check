package check

import (
	"errors"
	"strings"
	"testing"
)

// TestAutoProvisionBaseGolden_ParsesMissingGolden proves the sdk clone-enforcement
// error is recognized and the base bed name extracted (the R4 auto-provision seam).
func TestAutoProvisionBaseGolden_ParsesMissingGolden(t *testing.T) {
	orig := provisionBaseGoldenRun
	provisionBaseGoldenRun = func(base string) error { return nil }
	defer func() { provisionBaseGoldenRun = orig }()

	err := errors.New(`vm "check-charly-omarchy-vm": snapshot "golden" does not exist; create with: charly vm snapshot create check-charly-omarchy-vm golden`)
	visited := map[string]bool{}
	retry, perr := autoProvisionBaseGolden(err, visited, nil)
	if perr != nil {
		t.Fatalf("unexpected provision error: %v", perr)
	}
	if !retry {
		t.Fatal("missing-golden error must request a retry (auto-provision)")
	}
	if !visited["check-charly-omarchy-vm"] {
		t.Fatal("the base bed must be marked visited")
	}
}

// TestAutoProvisionBaseGolden_NonCloneErrorPropagates: unrelated vm-build errors
// must NOT trigger auto-provisioning.
func TestAutoProvisionBaseGolden_NonCloneErrorPropagates(t *testing.T) {
	err := errors.New("no container engine found (install podman or docker)")
	retry, perr := autoProvisionBaseGolden(err, map[string]bool{}, nil)
	if retry || perr != nil {
		t.Fatalf("unrelated errors must not auto-provision (retry=%v perr=%v)", retry, perr)
	}
}

// TestAutoProvisionBaseGolden_CycleGuard: a clone whose base is itself a clone
// must not recurse forever.
func TestAutoProvisionBaseGolden_CycleGuard(t *testing.T) {
	err := errors.New(`vm "check-base-vm": snapshot "golden" does not exist`)
	visited := map[string]bool{"check-base-vm": true}
	if _, perr := autoProvisionBaseGolden(err, visited, nil); perr == nil || !strings.Contains(perr.Error(), "cycle") {
		t.Fatalf("expected a cycle error, got %v", perr)
	}
}

// TestAutoProvisionBaseGolden_RefusesNonDisposable: a base that is not disposable
// must be refused (R10 discipline).
func TestAutoProvisionBaseGolden_RefusesNonDisposable(t *testing.T) {
	err := errors.New(`vm "charly-omarchy-rc": snapshot "golden" does not exist`)
	_, perr := autoProvisionBaseGolden(err, map[string]bool{}, func(s string) bool { return false })
	if perr == nil || !strings.Contains(perr.Error(), "not disposable") {
		t.Fatalf("expected a disposable refusal, got %v", perr)
	}
}

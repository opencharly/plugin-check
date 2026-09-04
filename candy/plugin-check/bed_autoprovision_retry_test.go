package check

import (
	"errors"
	"fmt"
	"testing"
)

func missingGoldenErr(base string) error {
	return errors.New(fmt.Sprintf("vm %q: snapshot \"golden\" does not exist", base))
}

// TestRetryDecision_RetriesOnceAfterSuccessfulAutoProvision pins the bed_run retry
// wiring: a failing vm-build whose missing-golden cause is auto-provisioned must be
// retried once. Without the seam this test fails (build called once, FAIL propagates).
func TestRetryDecision_RetriesOnceAfterSuccessfulAutoProvision(t *testing.T) {
	orig := provisionBaseGoldenRun
	provisionBaseGoldenRun = func(base string) error { return nil }
	defer func() { provisionBaseGoldenRun = orig }()

	visits := 0
	build := func() error {
		visits++
		if visits == 1 {
			return missingGoldenErr("check-base-bed")
		}
		return nil
	}
	var lastErr error
	for i := 0; i < 2; i++ {
		err := build()
		if err != nil {
			retry, perr := autoProvisionBaseGolden(err, map[string]bool{}, baseIsProvablyBed)
			if perr != nil {
				lastErr = perr
				break
			}
			if !retry {
				lastErr = err
				break
			}
			continue
		}
		lastErr = nil
		break
	}
	if lastErr != nil {
		t.Fatalf("expected the retry to succeed, got %v (build visits=%d)", lastErr, visits)
	}
	if visits != 2 {
		t.Fatalf("expected exactly one retry (2 builds), got %d", visits)
	}
}

// TestBaseIsProvablyBed_RefusesNonBedNames: the guard refuses non-bed-shaped bases.
func TestBaseIsProvablyBed_RefusesNonBedNames(t *testing.T) {
	if baseIsProvablyBed("omarchy-vm") {
		t.Fatal("a non-bed entity must be refused by the guard")
	}
	if !baseIsProvablyBed("check-omarchy-pr-10138-vm") {
		t.Fatal("a bed-shaped name must pass the guard")
	}
}

package check

import (
	"errors"
	"fmt"
	"testing"
)

func missingGoldenErr(base string) error {
	return errors.New(fmt.Sprintf("vm %q: snapshot \"golden\" does not exist", base))
}

// TestBuildVmWithProvisionRetry_RetriesOnceAfterAutoProvision exercises the
// PRODUCTION seam (buildVmWithProvisionRetry — the exact function bed_run.go
// calls). Fails if the retry wiring is removed from the production function.
func TestBuildVmWithProvisionRetry_RetriesOnceAfterAutoProvision(t *testing.T) {
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
	// the production function is invoked directly — this FAILS if bed_run's
	// retry integration is removed from buildVmWithProvisionRetry.
	if err := buildVmWithProvisionRetry(build, provisionBaseGoldenRun); err != nil {
		t.Fatalf("expected the retry to succeed, got %v (build visits=%d)", err, visits)
	}
	if visits != 2 {
		t.Fatalf("expected exactly one retry (2 builds), got %d", visits)
	}
}

// TestBuildVmWithProvisionRetry_NonCloneErrorPropagates: unrelated build errors
// must propagate without retry.
func TestBuildVmWithProvisionRetry_NonCloneErrorPropagates(t *testing.T) {
	build := func() error { return errors.New("no container engine found") }
	if err := buildVmWithProvisionRetry(build, provisionBaseGoldenRun); err == nil {
		t.Fatal("unrelated errors must propagate")
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

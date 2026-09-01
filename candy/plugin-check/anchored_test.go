package check

// anchored_test.go — the §5.3 snapshot-anchored mode's pure decision table
// (anchored.go). Every test FAILS without the new code: the helpers under test
// did not exist before this change, so a revert of the feature removes the
// functions the tests call (compile error = fail), and the assertions pin the
// new behavior (revert issuance, Step-5 skip, keep-venue→Keep, variant
// validation) rather than any pre-existing path.

import (
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// vmBedReply is the minimal VM-bed descriptor fixture anchored-mode helpers
// need (IsVM + the per-deploy domain/entity names the revert + create argv
// carry).
func vmBedReply() spec.CheckBedReply {
	return spec.CheckBedReply{IsVM: true, VMTemplate: "omarchy-vm", BedDomain: "check-omarchy-vm"}
}

func TestCheckRunBedOpts_KeepVenueForcesKeep(t *testing.T) {
	opts, err := checkRunBedOpts(&CheckRunCmd{KeepVenue: true})
	if err != nil {
		t.Fatalf("checkRunBedOpts: %v", err)
	}
	if !opts.Keep {
		t.Fatalf("checkRunBedOpts(KeepVenue=true).Keep = false, want true — keep-venue must force --keep (no teardown between batch runs)")
	}
	if !opts.KeepVenue {
		t.Errorf("KeepVenue not passed through to bedRunOpts")
	}
}

func TestCheckRunBedOpts_AnchoredSkipsFreshUpdateGate(t *testing.T) {
	opts, err := checkRunBedOpts(&CheckRunCmd{Anchor: "golden"})
	if err != nil {
		t.Fatalf("checkRunBedOpts: %v", err)
	}
	// The Step-5 fresh-update re-verify gate is the `!opts.NoRebuild` block in
	// runCheckBed; anchored mode must skip it (revert IS the freshness mechanism).
	if !opts.NoRebuild {
		t.Fatalf("checkRunBedOpts(Anchor=golden).NoRebuild = false, want true — anchored mode must skip the fresh-update re-verify gate")
	}
	if opts.Anchor != "golden" {
		t.Errorf("Anchor not passed through: %q", opts.Anchor)
	}
	// No --anchor leaves the gate untouched (no regression).
	plain, err := checkRunBedOpts(&CheckRunCmd{})
	if err != nil {
		t.Fatalf("checkRunBedOpts(plain): %v", err)
	}
	if plain.NoRebuild {
		t.Errorf("plain run got NoRebuild=true — anchored-only behavior leaked")
	}
}

func TestCheckRunBedOpts_ParsesVars(t *testing.T) {
	opts, err := checkRunBedOpts(&CheckRunCmd{Vars: []string{"pr=9345", "c=1"}})
	if err != nil {
		t.Fatalf("checkRunBedOpts: %v", err)
	}
	if opts.Vars["pr"] != "9345" || opts.Vars["c"] != "1" {
		t.Errorf("opts.Vars = %v, want {pr:9345 c:1}", opts.Vars)
	}
	if _, err := checkRunBedOpts(&CheckRunCmd{Vars: []string{"broken"}}); err == nil {
		t.Fatalf("malformed --var accepted, want a usage error")
	}
}

func TestParseRunVars_RejectsMalformed(t *testing.T) {
	for _, bad := range []string{"no-equals", "=value", ""} {
		if _, err := parseRunVars([]string{bad}); err == nil {
			t.Errorf("parseRunVars(%q) accepted, want error", bad)
		}
	}
	if vars, err := parseRunVars(nil); err != nil || vars != nil {
		t.Errorf("parseRunVars(nil) = %v, %v; want nil, nil", vars, err)
	}
}

func TestValidateAnchoredRun_VariantNotDeclaredRejected(t *testing.T) {
	opts := bedRunOpts{Variant: "big-mem"}
	node := spec.FleetNode{Variants: map[string]*spec.VmVariant{
		"small": {Cpus: 2},
	}}
	err := validateAnchoredRun(opts, vmBedReply(), node, "check-vm")
	if err == nil {
		t.Fatalf("undecided variant accepted, want rejection")
	}
	if !strings.Contains(err.Error(), "big-mem") || !strings.Contains(err.Error(), "variants: map") {
		t.Errorf("error %q should name the variant and the variants: map", err)
	}
	// Declared variant passes.
	if err := validateAnchoredRun(bedRunOpts{Variant: "small"}, vmBedReply(), node, "check-vm"); err != nil {
		t.Errorf("declared variant rejected: %v", err)
	}
}

func TestValidateAnchoredRun_AnchorRequiresVmBed(t *testing.T) {
	err := validateAnchoredRun(bedRunOpts{Anchor: "golden"}, spec.CheckBedReply{IsVM: false}, spec.FleetNode{}, "check-pod")
	if err == nil {
		t.Fatalf("--anchor on a non-VM bed accepted, want rejection")
	}
	if !strings.Contains(err.Error(), "VM-only") {
		t.Errorf("error %q should say snapshot-anchored mode is VM-only", err)
	}
	err = validateAnchoredRun(bedRunOpts{Variant: "big"}, spec.CheckBedReply{IsVM: false}, spec.FleetNode{}, "check-pod")
	if err == nil {
		t.Fatalf("--variant on a non-VM bed accepted, want rejection")
	}
	// A plain anchored VM run validates clean.
	if err := validateAnchoredRun(bedRunOpts{Anchor: "golden"}, vmBedReply(), spec.FleetNode{}, "check-vm"); err != nil {
		t.Errorf("valid anchored VM run rejected: %v", err)
	}
}

func TestAnchoredPreCheckStep_IssuesSnapshotRevert(t *testing.T) {
	got := anchoredPreCheckStep(vmBedReply(), bedRunOpts{Anchor: "golden"})
	want := []string{"vm", "snapshot", "revert", "omarchy-vm", "golden"}
	if !equalArgs(got, want) {
		t.Fatalf("anchoredPreCheckStep = %v, want %v (the revert must fire before the checks)", got, want)
	}
	// Non-anchored: no revert step at all.
	if argv := anchoredPreCheckStep(vmBedReply(), bedRunOpts{}); argv != nil {
		t.Errorf("non-anchored run got revert argv %v, want nil", argv)
	}
	// Anchored non-VM bed: no revert step (validation rejects earlier anyway).
	if argv := anchoredPreCheckStep(spec.CheckBedReply{IsVM: false}, bedRunOpts{Anchor: "golden"}); argv != nil {
		t.Errorf("anchored non-VM bed got revert argv %v, want nil", argv)
	}
}

func TestVmCreateArgs_VariantPassThrough(t *testing.T) {
	base := []string{"vm", "create", "omarchy-vm", "--domain", "check-omarchy-vm"}
	if got := vmCreateArgs(vmBedReply(), ""); !equalArgs(got, base) {
		t.Errorf("vmCreateArgs(no variant) = %v, want %v", got, base)
	}
	want := append(append([]string{}, base...), "--variant", "big-mem")
	if got := vmCreateArgs(vmBedReply(), "big-mem"); !equalArgs(got, want) {
		t.Errorf("vmCreateArgs(big-mem) = %v, want %v (variant must ride the vm create argv)", got, want)
	}
}

func TestRunVarsArgv_SortedDeterministic(t *testing.T) {
	got := runVarsArgv(map[string]string{"z": "1", "pr": "9345", "a": "b"})
	want := []string{"--var", "a=b", "--var", "pr=9345", "--var", "z=1"}
	if !equalArgs(got, want) {
		t.Errorf("runVarsArgv = %v, want %v (sorted keys)", got, want)
	}
	if argv := runVarsArgv(nil); argv != nil {
		t.Errorf("runVarsArgv(nil) = %v, want nil", argv)
	}
}

func TestWithRunVars_MergesIntoCheckRunEnv(t *testing.T) {
	env := map[string]string{"DEPLOY_NAME": "check-vm", "IMAGE": "x"}
	merged := withRunVars(env, map[string]string{"pr": "9345", "DEPLOY_NAME": "overridden"})
	if merged["pr"] != "9345" {
		t.Errorf("withRunVars did not merge pr: %v", merged)
	}
	if merged["DEPLOY_NAME"] != "overridden" {
		t.Errorf("withRunVars did not let operator passthrough win: %v", merged)
	}
	if env["pr"] != "" {
		t.Errorf("withRunVars mutated the caller's env: %v", env)
	}
}

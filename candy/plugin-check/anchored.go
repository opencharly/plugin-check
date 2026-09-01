package check

// anchored.go — §5.3 snapshot-anchored check-run mode. An anchored run
// (`charly check run <bed> --anchor <name>`) SKIPS the pre-run VM destroy (keeps
// the venue), reverts the golden-disk snapshot BEFORE the checks run, and skips
// the Step-5 fresh-update re-verify gate (revert IS the freshness mechanism).
// The golden disk comes from the operator's FRESH lane — `charly check run <bed>`
// with no --anchor builds the disk and captures the snapshot on_finalize (the
// bed deploy's snapshot: policy); an anchored run reverting a missing snapshot
// fails with guidance to run the fresh lane first.
//
// The pure helpers below are anchored mode's ONE decision table — runCheckBed
// (bed_run.go) consumes them and anchored_test.go exercises them without an
// executor, mirroring the codebase's test seam pattern for bed decisions.

import (
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/opencharly/spec/spec"
)

// parseRunVars parses the repeatable `--var key=value` pairs into a map. A
// malformed pair (no '=' or an empty key) is a usage error.
func parseRunVars(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	vars := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		k, v, ok := strings.Cut(pair, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("--var %q: want key=value", pair)
		}
		vars[k] = v
	}
	return vars, nil
}

// runVarsArgv renders the per-run vars as SORTED `--var key=value` argv for a
// `charly check live` cli-reentry step (sorted keys → deterministic step argv).
func runVarsArgv(vars map[string]string) []string {
	if len(vars) == 0 {
		return nil
	}
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	argv := make([]string, 0, len(vars)*2)
	for _, k := range keys {
		argv = append(argv, "--var", k+"="+vars[k])
	}
	return argv
}

// validateAnchoredRun rejects anchored-mode misuse BEFORE any step runs. The
// snapshot chain + variants are VM-only (the spec's substrate-word checks reject
// them on other substrates), and a selected variant MUST be declared in the
// bed's own variants: map — a variant boots the shared golden disk with a shape
// override, never an invented shape.
func validateAnchoredRun(opts bedRunOpts, d spec.CheckBedReply, node spec.FleetNode, bed string) error {
	if opts.Anchor != "" && !d.IsVM {
		return fmt.Errorf("charly check run %s: --anchor %q requires a VM bed (snapshot-anchored mode is VM-only)", bed, opts.Anchor)
	}
	if opts.Variant == "" {
		return nil
	}
	if !d.IsVM {
		return fmt.Errorf("charly check run %s: --variant %q requires a VM bed", bed, opts.Variant)
	}
	if _, declared := node.Variants[opts.Variant]; !declared {
		return fmt.Errorf("charly check run %s: --variant %q is not declared in the bed's variants: map (declared: %s)",
			bed, opts.Variant, variantNames(node.Variants))
	}
	return nil
}

// variantNames returns the sorted declared variant names for an error message.
func variantNames(variants map[string]*spec.VmVariant) string {
	if len(variants) == 0 {
		return "(none)"
	}
	names := make([]string, 0, len(variants))
	for name := range variants {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// anchoredPreCheckStep returns the snapshot-revert step argv an anchored run
// issues BEFORE the checks run, or nil when the run is not anchored (or the bed
// is not a VM). The revert resets the venue's disk to the golden snapshot
// captured by the operator's fresh lane — revert ≈ seconds vs a fresh install ≈
// 20-30 min.
func anchoredPreCheckStep(d spec.CheckBedReply, opts bedRunOpts) []string {
	if opts.Anchor == "" || !d.IsVM {
		return nil
	}
	return []string{"vm", "snapshot", "revert", d.VMTemplate, opts.Anchor}
}

// vmCreateArgs builds the `charly vm create` argv for a bed's VM arm. A selected
// variant passes through as --variant <name> so plugin-vm can apply the bed's
// variants: shape override to a boot over the shared golden disk.
//
// NOTE (follow-up plugin-vm PR): plugin-vm's VmCreateCmd does not accept
// --variant yet, so an opted-in --variant fails loudly at vm-create until that
// lands. That is the honest behavior for an opt-in flag — no existing bed
// regresses (the flag is only passed when the operator selects a variant).
func vmCreateArgs(d spec.CheckBedReply, variant string) []string {
	args := []string{"vm", "create", d.VMTemplate, "--domain", d.BedDomain}
	if variant != "" {
		args = append(args, "--variant", variant)
	}
	return args
}

// withRunVars folds the request's per-run vars into the check-run env so plan
// steps can ${VAR} them (the CheckRunRequest.Vars consumer — the field's spec
// contract is "per-run variable passthrough"). Operator passthrough wins over
// resolved vars.
func withRunVars(env map[string]string, vars map[string]string) map[string]string {
	if len(vars) == 0 {
		return env
	}
	merged := maps.Clone(env)
	maps.Copy(merged, vars)
	return merged
}

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
// snapshot chain is VM-only (the spec's substrate-word checks reject it on other
// substrates).
func validateAnchoredRun(opts bedRunOpts, d spec.CheckBedReply, bed string) error {
	if opts.Anchor != "" && !d.IsVM {
		return fmt.Errorf("charly check run %s: --anchor %q requires a VM bed (snapshot-anchored mode is VM-only)", bed, opts.Anchor)
	}
	return nil
}

// anchoredPreCheckStep returns the snapshot-revert step argv an anchored run
// issues BEFORE the checks run, or nil when the run is not anchored (or the bed
// is not a VM). The revert resets the venue's disk to the golden snapshot
// captured by the operator's fresh lane — revert ≈ seconds vs a fresh install ≈
// 20-30 min. The revert targets the bed's per-deploy domain (--domain
// <BedDomain>, #33/P33): the snapshot surface is keyed on the entity, and the
// bed's live domain is named after the DEPLOY, not the entity.
//
// The step is `vm snapshot revert-and-start` (the plugin-vm composite): the
// anchored lane's venue is kept from the fresh lane (shut off after the update
// rebuild), and the composite encapsulates stop (no-op if already off) →
// offline revert → start, so the domain is booted and ready for the checks.
func anchoredPreCheckStep(d spec.CheckBedReply, opts bedRunOpts) []string {
	if opts.Anchor == "" || !d.IsVM {
		return nil
	}
	return []string{"vm", "snapshot", "revert-and-start", d.VMTemplate, opts.Anchor, "--domain", d.BedDomain}
}

// vmCreateArgs builds the `charly vm create` argv for a bed's VM arm.
func vmCreateArgs(d spec.CheckBedReply) []string {
	return []string{"vm", "create", d.VMTemplate, "--domain", d.BedDomain}
}

// update_gate change-class values — the bed plan's declarative R10 fresh-update
// gate (the spec #Deploy update_gate: field). full (the authored-absent default)
// is the canonical destroy+recreate gate; restart-only replaces the destroy/
// recreate with a plain venue RESTART (a VM boots its existing clone again) and
// re-checks the restarted venue — the change class for runtime-PR-injection
// software evals; skip is the declarative twin of --no-rebuild.
const (
	updateGateFull        = "full"         // `charly update` — destroy + recreate the venue, then re-check
	updateGateRestartOnly = "restart-only" // reboot the existing clone/container, then re-check
	updateGateSkip        = "skip"         // no update phase and no post-update pass
)

// updateGateFor resolves the bed's Step-5 gate class: the operator's
// --no-rebuild flag wins (forces skip — the pre-existing invariant), else the
// plan's authored update_gate: field, else the full gate (back-compat default;
// an unknown authored value ALSO falls back to full rather than silently
// skipping the acceptance gate). Anchored mode is handled at the call site
// (revert IS the freshness mechanism — Step 5 is structurally suppressed).
func updateGateFor(opts bedRunOpts, bedNode *spec.FleetNode) string {
	if opts.NoRebuild {
		return updateGateSkip
	}
	if bedNode == nil || bedNode.UpdateGate == "" {
		return updateGateFull
	}
	switch bedNode.UpdateGate {
	case updateGateFull, updateGateRestartOnly, updateGateSkip:
		return bedNode.UpdateGate
	default:
		return updateGateFull
	}
}

// gateStep is ONE Step-5 gate step: its summary.yml/step-log name + argv.
// The VM restart-only pair is two steps because every `charly vm …` subcommand
// is one recorded step; the runCheckBed caller stamps both exactly like full's
// `update` step (diag-scanned logs included).
type gateStep struct {
	name string
	argv []string
}

// updateGateSteps returns the Step-5 GATE steps (non-group arm) by change class:
//   - full — the canonical `charly update` destroy+recreate (tag-pinned to the
//     per-run build, exactly as before).
//   - restart-only, VM — reboot the EXISTING per-deploy domain: `vm stop --force`
//     then `vm start` on the same clone disk. No destroy, no recreate, no
//     reinstall. --force makes the power-cycle deterministic on ANY guest (a
//     golden without acpid ignores the ACPI shutdown and the graceful stop would
//     burn its 3m grace cap — measured on the scratch restart-only run);
//     `vm start` is idempotent (an already-running domain is a clean success).
//   - restart-only, pod/container venue — `charly restart` (same image).
//   - restart-only, in-place (local/external) — no restartable venue: the gate
//     step is the in-place re-apply itself, unchanged (full's own step).
//   - skip — never called (the caller skips the whole Step-5 block); returns nil.
func updateGateSteps(gate string, d spec.CheckBedReply, name, imageTag string, isInPlace bool) []gateStep {
	switch {
	case gate == updateGateRestartOnly && d.IsVM:
		return []gateStep{
			{"gate-restart-stop", []string{"vm", "stop", "--force", d.VMTemplate, "--domain", d.BedDomain}},
			{"gate-restart-start", []string{"vm", "start", d.VMTemplate, "--domain", d.BedDomain}},
		}
	case gate == updateGateRestartOnly && !isInPlace:
		return []gateStep{{"gate-restart", []string{"restart", name}}}
	default:
		return []gateStep{{"update", withRunTag([]string{"update", name}, imageTag)}}
	}
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

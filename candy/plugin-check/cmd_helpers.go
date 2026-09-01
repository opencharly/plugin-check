package check

// cmd_helpers.go — K1-unblock W3 Unit A: the remaining pure, no-runner-dependency helpers off
// charly/check_cmd.go. resolveNestedNode/guestNestedCheckCmd are plain tree/string helpers with no
// core-only dependency; candyAddSteps/candyDirsFromEnvelope replace collectAddCandySteps/
// candyDirsFromScan's core-only filesystem re-scan (ScanAllCandyWithConfig) with a read off the
// already-fetched resolved-project envelope — rp.CandyModels is filled from the SAME
// ScanAllCandyWithConfigOpts scan (confirmed by tracing validate_project_host.go:98's
// loadProjectForResolve, the resolved-project envelope's own loader), so it already carries every
// filesystem-discovered candy the original core-only re-scan would find, including its
// SourceDir (spec.CandyModel.SourceDir) — no candyDirsFromScan map-building step needed at all.
//
// K1-unblock wave arm 1: vmHostdevCount + the checkLive* family (pod/vm/local/group) + their
// dispatcher landed plugin-side too — see live_gather.go (pluginVmHostdevCount,
// pluginCheckLivePod/VM/Local/Group, pluginCheckRunLive). STILL core-only (feeds the
// "check-load-plugins" seam, not part of any check-run Mode this package dispatches):
// resolveCheckRunnerContext (charly/check_cmd.go). deployNodePluginContext relocated to
// charly/plugin_loader.go (#55 W3 B3, beside its M-mechanism consumer loadDeployPlugins) — still
// core-only, different file. checkLocalDeployScope/runLocalDeployScopePlan (the external
// `target: local` deploy's own --verify path) relocated to candy/plugin-fleet (#55 W3 B3,
// verify_local.go) — no longer core at all.

import (
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/shellquote"
	"github.com/opencharly/spec/spec"
)

// resolveNestedNode walks a dotted path through roots[root].Children, returning the leaf node (or
// nil if any segment is absent). Ported unchanged from charly/check_cmd.go.
func resolveNestedNode(roots map[string]spec.FleetNode, path string) *spec.FleetNode {
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return nil
	}
	entry, ok := roots[parts[0]]
	if !ok {
		return nil
	}
	current := &entry
	for _, p := range parts[1:] {
		if current.Children == nil {
			return nil
		}
		next, ok := current.Children[p]
		if !ok || next == nil {
			return nil
		}
		current = next
	}
	return current
}

// guestNestedCheckCmd builds the `charly check live <pod>` command that a nested-in-VM pod
// check dispatches INTO the guest over SSH. Ported unchanged from
// charly/check_cmd.go (pure string builder, no core dependency) plus the per-run
// --var passthrough: vars ride along as sorted --var key=value args so the
// guest's check-run env sees the same vars the host's direct live path sees.
func guestNestedCheckCmd(guestPod, format, section string, filter []string, instance string, vars map[string]string) string {
	if format == "" {
		format = "text"
	}
	var cmd strings.Builder
	cmd.WriteString("charly check live " + shellquote.ShellQuote(guestPod) + " --format " + shellquote.ShellQuote(format))
	if section != "" {
		cmd.WriteString(" --section " + shellquote.ShellQuote(section))
	}
	for _, f := range filter {
		cmd.WriteString(" --filter " + shellquote.ShellQuote(f))
	}
	if instance != "" {
		cmd.WriteString(" -i " + shellquote.ShellQuote(instance))
	}
	// runVarsArgv is the sorted ["--var", "k=v", ...] interleave; quote only the
	// values (flags stay bare, matching the --format/--filter pattern above).
	vargs := runVarsArgv(vars)
	for i := 0; i+1 < len(vargs); i += 2 {
		cmd.WriteString(" " + vargs[i] + " " + shellquote.ShellQuote(vargs[i+1]))
	}
	return cmd.String()
}

// candyAddSteps collects the deploy-scope check/provision steps from each locally-scanned candy a
// deploy applies via add_candy — the envelope-sourced replacement for charly/check_cmd.go's
// collectAddCandySteps. Remote (@github) candies are skipped, matching the original: they carry
// their own context and a re-scan can resolve a different cached version than what was deployed.
func candyAddSteps(rp *spec.ResolvedProject, addCandies []string) []spec.Step {
	if rp == nil || len(addCandies) == 0 {
		return nil
	}
	var out []spec.Step
	for _, ref := range addCandies {
		// A remote @github ref is looked up exactly like a local one: BareRef is the
		// map-key form (no @, no :version), and rp.CandyModels keys a FETCHED candy by
		// precisely that. Skipping remote refs here silently dropped every baked check:
		// step of a candy applied by ref — the normal way a bed pins one — so the bed ran
		// only its own plan and passed while asserting almost nothing.
		m, ok := rp.CandyModels[deploykit.BareRef(ref)]
		if !ok {
			continue
		}
		out = append(out, deploykit.BakeableSteps(m.Plan)...)
	}
	return out
}

// candyDirsFromEnvelope projects the envelope's rp.CandyModels into a name -> SourceDir map — the
// committed-APK anchoring resolveCheckRunnerContext (charly/check_cmd.go, still host-resident
// pending Unit B) folds into a live baked-plan runner's RunnerConfig.CandyDirs. Keyed identically
// to the original candyDirsFromScan: the candy MAP KEY (a bare local name, or the bare @github ref
// for a fetched one) — rp.CandyModels is keyed the same way (both ultimately come off the same
// ScanAllCandyWithConfigOpts scan).
func candyDirsFromEnvelope(rp *spec.ResolvedProject) map[string]string {
	if rp == nil || len(rp.CandyModels) == 0 {
		return nil
	}
	out := make(map[string]string, len(rp.CandyModels))
	for key, m := range rp.CandyModels {
		if m.SourceDir != "" {
			out[key] = m.SourceDir
		}
	}
	return out
}

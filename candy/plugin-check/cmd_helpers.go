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
// K1-unblock wave arm 1: vmHostdevCount + the checkLive* family (pod/vm/local) + their
// dispatcher landed plugin-side too — see live_gather.go (pluginVmHostdevCount,
// pluginCheckLivePod/VM/Local, pluginCheckRunLive). STILL core-only (feeds the
// "check-load-plugins" seam, not part of any check-run Mode this package dispatches):
// resolveCheckRunnerContext (charly/check_cmd.go). deployNodePluginContext relocated to
// charly/plugin_loader.go (#55 W3 B3, beside its M-mechanism consumer loadDeployPlugins) — still
// core-only, different file. checkLocalDeployScope/runLocalDeployScopePlan (the external
// `target: local` deploy's own --verify path) relocated to candy/plugin-fleet (#55 W3 B3,
// verify_local.go) — no longer core at all.

import (
	"context"
	"fmt"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/shellquote"
	"github.com/opencharly/spec/spec"
)

// resolveNestedNode walks a dotted path through roots[root]'s ordered member tree — the
// IN-SUBSTRATE positions, the successor of the former Children map — returning the leaf node (or
// nil if any segment is absent). Ported from charly/check_cmd.go; the position-derived lookup
// (MemberByName + InSubstrate) replaces the former dual-map index.
func resolveNestedNode(roots map[string]spec.DeployNode, path string) *spec.DeployNode {
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
		m := current.MemberByName(p)
		if m == nil || !m.InSubstrate() || m.Node == nil {
			return nil
		}
		current = m.Node
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

// addCandyRefsForEntity returns the deploy's OWN `add_candy:` refs — scoped to its box — ready to
// ride a ResolvedProjectRequest as ExtraCandyRefs.
//
// THE ROOT FIX (opencharly/plugin-check#112). The envelope's candy scan is REACHABILITY-scoped: it
// collects the refs the project's own image closure walks (sdk/loaderkit.CollectRemoteRefsOpts), and
// a deploy's `add_candy:` refs are by construction OUTSIDE that closure. candy/plugin-build's own
// resolved-project cache key states the consequence in its own words: ExtraCandyRefs "is the ONLY
// way a host-side plugin candy that no image closure reaches enters rp.Candies at all". So a resolve
// that does not carry the deploy's refs back NEVER MATERIALIZES the coordinate the deploy pinned:
// rp.CandyModels then holds whatever OTHER coordinate of that repo the closure did reach — an older
// tag transitively required by a composed layer — and the child `check live` phase asserted that
// version's plan (and, since #113, refuses instead). Passing the refs in is what makes the pinned
// coordinate a CANDIDATE, so the arbiter resolves it and candyAddSteps reads the pinned model.
//
// It is the per-node ref collection the host's own deployNodePluginContext (charly/plugin_loader.go)
// performs for plugin-loading, over the SAME plugin-side merged-tree resolver, with the SAME scope
// constructor: `spec.ScopedExtraCandyRefs(spec.BoxScope(name), …)`, because the deploy IS its box
// (a constant label here is the #739 false-conflict bug). Nested in-substrate children are visited
// too — over-wide is safe, since an unused ref only widens a fetch, and a single-process walk of a
// root with substrate members needs every member's refs.
//
// A resolve failure is not an error: an unwidened envelope degrades to exactly today's behaviour
// (the version guard names the mismatch loudly), and the venue/plan paths report their own failures.
func addCandyRefsForEntity(ctx context.Context, ex *sdk.Executor, dir, name string) []spec.ExtraCandyRef {
	if ex == nil || name == "" || name == "." {
		return nil
	}
	tree, err := loaderkit.ResolveMergedTreeViaExecutor(ctx, ex, dir)
	if err != nil || len(tree) == 0 {
		return nil
	}
	// The node lookup follows the SAME contract the host's resolveDeployNodeByPath states, one layer
	// each: the `vm:` CLI address prefix is stripped (splitVmAddress), then the EXACT full key wins —
	// a NAMESPACE-qualified deploy (`charly.check-agentteams-vm`) carries dots that are namespace
	// separators, not member-path separators — and only then does the dotted MEMBER walk apply.
	plain, _ := spec.SplitVmAddress(name)
	var node *spec.DeployNode
	if n, ok := tree[plain]; ok {
		node = &n
	} else {
		node = resolveNestedNode(tree, plain)
	}
	if node == nil {
		// A NAMESPACE-qualified bed that lives in an import (the umbrella root importing `charly/`)
		// is not a key in the merged ROOT-scope tree. Reach it through the loader's ONE
		// namespace-aware bed resolver — the same one the classification path uses
		// (checkproject.go's classifyCheckProjection) — rather than a second dotted-name walk.
		if uf, ok, uerr := loaderkit.LoadUnifiedViaExecutor(ctx, ex, dir); uerr == nil && ok && uf != nil {
			if n, bok := uf.ResolveBed(plain); bok {
				node = &n
			}
		}
	}
	if node == nil {
		return nil
	}
	return scopedAddCandyRefs(spec.BoxScope(name), node)
}

// scopedAddCandyRefs is the PURE half of addCandyRefsForEntity: a node's own `add_candy:` refs plus
// its in-substrate descendants', each tagged with the deploy's box scope. It mirrors the host's
// visitation exactly (charly/plugin_loader.go's deployNodePluginContext recurses the in-substrate
// members and nothing else: a deploy-level sibling is its own composition, addressed by its own
// name), so the plugin and the host state the same scope for the same refs.
func scopedAddCandyRefs(scope string, node *spec.DeployNode) []spec.ExtraCandyRef {
	var out []spec.ExtraCandyRef
	var visit func(n *spec.DeployNode)
	visit = func(n *spec.DeployNode) {
		if n == nil {
			return
		}
		out = append(out, spec.ScopedExtraCandyRefs(scope, n.AddCandy...)...)
		for _, m := range n.InSubstrateMembers() {
			visit(m.Node)
		}
	}
	visit(node)
	return out
}

// candyAddSteps collects the deploy-scope check/provision steps from each locally-scanned candy a
// deploy applies via add_candy — the envelope-sourced replacement for charly/check_cmd.go's
// collectAddCandySteps. Remote (@github) candies are looked up exactly like local ones (see the
// map-key note inside).
//
// THE VERSION GUARD (opencharly/plugin-check#112). `rp.CandyModels` is keyed by candy IDENTITY
// (BareRef: no @, no :version), which is right for the graph but cannot distinguish two
// coordinates of one repo: when two scopes in the same project pin the same repo at different
// tags, the scan FETCHES both and its per-entity arbiter keeps ONE winner
// (sdk/loaderkit/scan_orchestrate.go — the losers are dropped at PickCandyVersion), so this bare
// lookup returns that winner whatever the deploy pinned. Measured: `distro-arch`'s bed pins
// `layer-direnv:v2026.287.2155` while `layer-agent-forwarding` transitively pins
// `layer-direnv:v2026.272.0057`, and the bed's child `check live` ran the OLD plan (9 steps, no
// `context:`) while the fetched artifact carried the pinned one.
//
// The FIX for the defect itself is upstream of this lookup: the resolve that FILLS this envelope now
// carries the deploy's own add_candy: refs as ExtraCandyRefs (addCandyRefsForEntity, above), so the
// pinned coordinate is materialized at all and the arbiter can pick it. This lookup stays
// identity-keyed — that is what the envelope offers, and it is correct once the pin is a candidate.
//
// The guard that remains is the narrow fail-closed arm for the case the arbiter CANNOT satisfy: an
// authored ref that NAMES a version and receives a model stamped with a DIFFERENT one is an
// unresolvable input, not a plan to run — a pin OLDER than another coordinate the same closure
// reaches loses the newest-tag arbiter, and honouring it needs per-coordinate models on the
// envelope (spec → sdk → charly; opencharly/plugin-check#146). Failing loudly naming both versions
// beats silently asserting a version the deploy never named. The guard is deliberately narrow:
//   - a version-less ref (no pin) keeps the identity lookup — that is the documented behaviour;
//   - an UNSTAMPED model (Version == "") is not gated, because that is the documented local-shadow
//     case (sdk/loaderkit's scan gives a local candy of the same name precedence, as INFO);
//   - a local bare-name ref has no version to honour.
func candyAddSteps(rp *spec.ResolvedProject, addCandies []string) ([]spec.Step, error) {
	if rp == nil || len(addCandies) == 0 {
		return nil, nil
	}
	var out []spec.Step
	for _, ref := range addCandies {
		// BareRef is the map-key form (no @, no :version), and rp.CandyModels keys a FETCHED
		// candy by precisely that. Skipping remote refs here silently dropped every baked check:
		// step of a candy applied by ref — the normal way a bed pins one — so the bed ran only
		// its own plan and passed while asserting almost nothing.
		bare := deploykit.BareRef(ref)
		m, ok := rp.CandyModels[bare]
		if !ok {
			continue
		}
		if _, want := spec.StripCandyRefVersion(ref); want != "" && m.Version != "" && m.Version != want {
			return nil, fmt.Errorf(
				"add_candy %q names version %q but the resolved model for %q is version %q: a bare-keyed lookup cannot honour a pin when the same repo is pinned at two coordinates in one project, and this resolution carries the arbiter's winner, not the pinned version. Running it would assert a version this deploy never named (opencharly/plugin-check#112). Honour the pin (or drop the version and pin the project consistently) and retry",
				ref, want, bare, m.Version)
		}
		out = append(out, deploykit.BakeableSteps(m.Plan)...)
	}
	return out, nil
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

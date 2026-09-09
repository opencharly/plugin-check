package check

// plugin_runner.go — K1-unblock W3 Unit B: newPluginCheckRunner, the plugin-side counterpart of
// charly/checkrun.go's newCheckRunner. Wires the SAME three seams every check runner needs
// (Verbs/Grammar/ProbeTimeout) using this file's own portable implementations instead of core's
// hostVerbResolver/hostPlanGrammar/loadedReadiness — confirmed byte-identical behavior for
// Grammar (plan_grammar.go, ported verbatim) and ProbeTimeout (poll.ReadinessProvider() is the
// SAME resolver loadedReadiness() wraps, already threaded to this plugin process via
// CHARLY_READINESS_* env at Connect — see venue.go's header). Verbs is the new mechanism
// (verb_resolver.go's pluginVerbResolver), proven by the W3 Unit B spike.

import (
	"context"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/checkkit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/poll"
	"github.com/opencharly/spec/spec"
)

// newPluginCheckRunner builds a kit.Runner for a check pass driven from this plugin, given the
// env snapshot every out-of-process verb dispatch carries. Mirrors charly/checkrun.go's
// newCheckRunner: the caller fills cfg with the per-site fields (Exec/Mode/Env/Box/... and, for a
// live cross-deployment pass, TargetResolver/HostVars); Verbs/Grammar/ProbeTimeout are always set
// here, never by the caller.
//
// The VerbResolver holds a BACK-REFERENCE to the constructed *kit.Runner (pvr.kr = kr, mirroring
// charly/checkrun.go's hvr.kr = kr) so an out-of-process verb dispatch (InvokeProvider's S1
// VenueDescriptor seam) always threads the runner's CURRENT executor — including one SwapVenue
// retargeted mid-plan for a cross-deployment (`on:`/${HOST:member}) or member step, never a
// venue frozen at construction time. RCA'd live (check-k3s-vm SIGSEGV'd on a nil cc.Exec() inside
// a `command:` step on a VM target): the former static-venueDesc-at-construction design left
// InvokeProvider's fallback ("thread the caller's incoming s.exec") in play whenever the runner's
// OWN default venue never round-tripped — which was ALWAYS true for a plan's first/default step
// dispatched from a top-level `charly check ...` command Invoke (no ambient deploy-context s.exec
// of its own). Deriving the descriptor fresh from r.kr.Exec() on every RunVerb call fixes both
// that default-venue gap AND the SwapVenue-tracking gap in one generic mechanism.
// kitVerbs adapts the checkkit resolver to the kit.VerbResolver seam: the
// embedded checkkit.VerbResolver now carries the real RunProvisionAct leg
// (the sdk v0.2026252.834 cutover) - the do:act state-provision verbs
// dispatch through the SDK's one home, no plugin-local copy (R3).
type kitVerbs struct {
	*checkkit.VerbResolver
	kr *kit.Runner
}

func (v *kitVerbs) SetRunner(kr *kit.Runner) {
	v.kr = kr
	v.VerbResolver.SetRunner(kr)
}

func newPluginCheckRunner(ex *sdk.Executor, ctx context.Context, env spec.CheckEnv, cfg kit.RunnerConfig) *kit.Runner {
	// env.MCPProvide is the deployment's mcp_provide declarations (the live-VM gathers seed it
	// from the resolved vm template) — captured onto the resolver so RunVerb's FRESH snapshot
	// (pluginSnapshotCheckEnv, built from runner state on every call) carries it too, not just
	// the defensive construction-time fallback env.
	// the runner config carries the deployment's mcp_provide declarations so the
	// FRESH per-call snapshot (SnapshotCheckEnv -> kr.MCPProvide()) rides them
	// too - the VM venue has no OCI label to resolve them from. A container
	// venue resolves mcp_provide from its OCI image label instead, so the env
	// stays unpopulated there (the label path owns it).
	if de, ok := cfg.Exec.(spec.DeployExecutor); ok {
		if d := kit.DescriptorFromExecutor(de); d.Kind != "container" {
			// a NestedExecutor over a container jump (podman/docker exec) is a
			// container venue too - its descriptor Kind is empty, the jump kind
			// is the tell.
			if ne, ok := cfg.Exec.(*kit.NestedExecutor); !ok || ne.Jump.Kind != kit.JumpPodmanExec {
				cfg.MCPProvide = env.MCPProvide
			}
		}
	}
	pvr := &kitVerbs{VerbResolver: &checkkit.VerbResolver{Ex: ex, Env: env}}
	cfg.Verbs = pvr
	cfg.Grammar = checkkit.PlanGrammar{}
	if cfg.ProbeTimeout == 0 {
		cfg.ProbeTimeout = poll.ReadinessProvider().PerAttemptFor(vmshared.PollLocal)
	}
	kr := kit.NewRunner(cfg)
	pvr.SetRunner(kr)
	return kr
}

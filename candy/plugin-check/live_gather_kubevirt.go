package check

import (
	"context"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// pluginCheckLiveKubeVirt gathers a `kubevirt:` deploy's deploy-scope check over the guest
// SSH — the kubevirt sibling of pluginCheckLiveVM. A kubevirt guest is an ssh venue exactly
// like a vm: candy/plugin-kubevirt's PrepareVenue opens the managed `virtctl port-forward`
// and publishes the Host stanza under `kit.VmSshAlias(VmDomainIdentity(deploy))`, so the
// executor + readiness gate are the SAME managed-alias SSHExecutor the vm arm uses. Unlike
// the vm arm there is no libvirt domain, no `port_forwards` allocation table, and no
// `VirtualMachineSnapshot` — the plan comes straight off the kubevirt deploy node.
func pluginCheckLiveKubeVirt(ex *sdk.Executor, ctx context.Context, rp *spec.ResolvedProject, tree map[string]spec.DeployNode, dir string, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	entry, ok := tree[req.Name]
	if !ok {
		return kit.CheckRunReply{}, fmt.Errorf("check live: kubevirt deploy %q not found", req.Name)
	}
	domainID := spec.VmDomainIdentity(req.Name)

	// Plan: the kubevirt deploy node's own plan + its candy add steps (the in-guest plan
	// walk). A dotted nested target chains through the shared deploy-chain resolver.
	plan := append([]spec.Step(nil), entry.Plan...)
	if expanded, eErr := expandPlanIncludes(rp, plan); eErr == nil {
		plan = expanded
	}
	if len(plan) == 0 && len(req.Plan) == 0 {
		return kit.CheckRunReply{NoSteps: true}, nil
	}
	// The guest ssh executor: the managed alias carries User/Port/IdentityFile, so no
	// VmSpec/lvm-state lookup is needed (that is the whole point of the managed stanza).
	var executor deploykit.DeployExecutor = &kit.SSHExecutor{Host: kit.VmSshAlias(domainID), ConnectTimeout: 10}
	if _, chain, chainErr := deploykit.ResolveDeployChain(tree, req.Name, kit.ShellExecutor{}); chainErr == nil && chain != nil {
		executor = chain
	}
	gate := &kit.SSHExecutor{Host: kit.VmSshAlias(domainID), ConnectTimeout: 5}
	if gerr := gate.WaitForSSH(ctx); gerr != nil {
		return kit.CheckRunReply{}, fmt.Errorf("kubevirt %q is not up / SSH-reachable — is the VirtualMachine running? %w", domainID, gerr)
	}
	if gerr := gate.WaitForCloudInit(ctx); gerr != nil {
		return kit.CheckRunReply{}, fmt.Errorf("kubevirt %q cloud-init did not settle: %w", domainID, gerr)
	}

	env := map[string]string{
		"IMAGE":          req.Name,
		"INSTANCE":       req.Instance,
		"CONTAINER_IP":   "127.0.0.1",
		"CONTAINER_NAME": "charly-" + domainID,
		"DEPLOY_NAME":    kit.SanitizeDeployName("kubevirt:" + domainID),
	}
	resolver := newPluginRuntimeCheckVarResolver(env)

	set := &kit.LabelDescriptionSet{Deploy: []kit.LabeledDescription{{Origin: "kubevirt:" + domainID, Plan: plan}}}
	set = wrapStepsFileSet(set, req.Plan, "kubevirt:"+domainID)

	envVars, hasRuntime := pluginResolverEnv(resolver)
	envVars = withRunVars(envVars, req.Vars)
	hostVars, hostCleanups := resolveHostVarsForSteps(ex, ctx, dir, plan, req.Instance)
	defer kit.CloseHostCleanups(hostCleanups)
	runner := newPluginCheckRunner(ex, ctx, spec.CheckEnv{
		Mode:      "live",
		Box:       req.Name,
		Instance:  req.Instance,
		Venue:     domainID,
		VenueKind: "kubevirt",
	}, kit.RunnerConfig{
		Exec:           executor,
		Mode:           kit.ModeLive,
		Env:            envVars,
		HasRuntime:     hasRuntime,
		Box:            req.Name,
		Instance:       req.Instance,
		VmName:         domainID,
		VerifyOnly:     true,
		CandyDirs:      candyDirsFromEnvelope(rp),
		HostVars:       hostVars,
		TargetResolver: pluginVenueResolver(ex, ctx, dir, req.Instance),
	})
	results := kit.RunPlan(ctx, runner, set, false)
	return kit.CheckRunReply{Steps: results, Header: fmt.Sprintf("KubeVirt: charly-%s (managed ssh alias)", req.Name)}, nil
}

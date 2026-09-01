package check

// feature_run_gather.go — K1-unblock wave, arm 2 (the "feature-live" check-run mode):
// pluginCheckRunFeatureLive, the plugin-resident port of the former core
// (deleted) hostFeatureLive, mirroring live_gather.go's pluginCheckLivePod's pod-venue
// construction. Reached from candy/plugin-check's OWN `charly check feature run` CLI leaf
// (feature_cmd.go) via command.go's Mode:"feature-live" short-circuit.
//
// The BUILD-scope sibling "feature-box" mode is now feature_box_gather.go's pluginCheckRunFeatureBox
// (cone-C #31 relocated the former core hostFeatureBox here): `charly box feature run <image>` is
// candy/plugin-box's command:feature, which InvokeProvider's command:check's hidden `__feature-box`
// leaf → Mode:"feature-box" → that engine. So both feature modes (deploy-scope feature-live here,
// build-scope feature-box in the sibling file) live plugin-side; the former in-core BoxFeatureRunCmd
// + hostFeatureBox are DELETED.
//
// The agent-grader resolve reuses agent.go's EXISTING resolveAgentSpec (already the
// synccreds.go/runlocal.go call pattern for this exact catalog→exec-spec resolve, over
// Executor.InvokeProvider — R3, no duplicate second copy) fed rp.AgentBodies, the resolved-project
// envelope's projection of the catalog the deleted core-side grader-catalog resolver used to read
// via uf.PluginKinds["agent"].

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"
)

// pluginCheckRunFeatureLive is the "feature-live" mode: deploy-scope ADE acceptance against the
// running deployment req.Name, wiring the host-side agent grader (agent.go's resolveAgentSpec)
// unless req.NoAgent. The port of the former core hostFeatureLive, mirroring
// live_gather.go's pluginCheckLivePod's pod-venue construction (this mode is always a pod/
// container deployment — the core original never classified vm/local/group here either).
// featureLiveArm classifies a feature-live target: "vm" routes to the VM arm
// (pluginCheckRunFeatureLiveVM), anything else to the pod arm. Extracted from
// pluginCheckRunFeatureLive so the dispatch is testable — before the fix, the
// feature-live path was container-only (deploykit.ResolveContainer directly) and
// a VM target failed with "container ... is not running".
func featureLiveArm(tree map[string]spec.FleetNode, name string) string {
	if _, isVM := checkVmTarget(tree, name); isVM {
		return "vm"
	}
	return "pod"
}

func pluginCheckRunFeatureLive(ex *sdk.Executor, ctx context.Context, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	dir, _ := os.Getwd()
	rp, err := resolvedProject(ex, ctx, dir)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	tree := derefDeployTree(rp.Deploy)
	// Connect the out-of-process check-verb plugins (mcp/cdp/vnc/dbus/spice/…) the live plan
	// references — ONCE, at command scope, before the per-kind dispatch (task #62).
	checkLoadPlugins(ex, ctx, req.Name, dir)
	if featureLiveArm(tree, req.Name) == "vm" {
		return pluginCheckRunFeatureLiveVM(ex, ctx, rp, tree, dir, req)
	}
	return pluginCheckRunFeatureLivePod(ex, ctx, rp, tree, dir, req)
}

// pluginCheckRunFeatureLivePod is the pod (running-container) arm of the feature-live mode:
// deploy-scope ADE acceptance against the running container req.Name, wiring the host-side
// agent grader (agent.go's resolveAgentSpec) unless req.NoAgent. The port of the former core
// hostFeatureLive, mirroring live_gather.go's pluginCheckLivePod's pod-venue construction.
func pluginCheckRunFeatureLivePod(ex *sdk.Executor, ctx context.Context, rp *spec.ResolvedProject, tree map[string]spec.FleetNode, dir string, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	engine, containerName, err := deploykit.ResolveContainer(req.Name, req.Instance)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	// Deploy-scope ADE grades the plan baked into the image this container is RUNNING
	// (live_image.go) — the same single image identity live_gather.go's plan gather uses.
	meta, err := liveDeployMetadata(engine, containerName)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	if meta == nil || meta.Description == nil || meta.Description.IsEmpty() {
		return kit.CheckRunReply{NoSteps: true}, nil
	}
	var deployOverlay *spec.FleetNode
	if dc, derr := loaderkit.LoadHostFleetConfigViaExecutor(ctx, ex); derr == nil && dc != nil {
		if entry, ok := dc.Fleet[spec.DeployKey(req.Name, req.Instance)]; ok {
			deployOverlay = &entry
		} else if entry, ok := dc.Fleet[req.Name]; ok {
			deployOverlay = &entry
		}
	}
	resolver, _ := kit.ResolveCheckVarsRuntime(meta, deployOverlay, engine, req.Name, containerName, req.Instance)
	resolver = stampCharlyBin(resolver)
	// validateTagExpr still VALIDATES --tag's syntax; applying the parsed filter to the plan walk
	// is a known, tracked gap — preserved verbatim from the core original.
	if err := kit.ValidateTagExpr(req.Tag); err != nil {
		return kit.CheckRunReply{}, fmt.Errorf("parsing --tag: %w", err)
	}

	env, hasRuntime := pluginResolverEnv(resolver)
	env = withRunVars(env, req.Vars)
	var grader kit.StepGrader
	if !req.NoAgent {
		ai, aerr := resolveAgentSpec(ex, ctx, rp.AgentBodies, req.Agent)
		if aerr != nil {
			return kit.CheckRunReply{}, aerr
		}
		grader = &kit.AgentGrader{Agent: ai, Target: req.Name, Instance: req.Instance, Timeout: req.Timeout}
	}
	execChain := deploykit.ContainerChain(engine, containerName)
	runner := newPluginCheckRunner(ex, ctx, spec.CheckEnv{
		Mode:      "feature-live",
		Box:       req.Name,
		Instance:  req.Instance,
		Distros:   meta.Distro,
		VenueKind: execChain.Kind(),
	}, kit.RunnerConfig{
		Exec:                 execChain,
		Mode:                 kit.ModeLive,
		Env:                  env,
		HasRuntime:           hasRuntime,
		Distros:              meta.Distro,
		Box:                  req.Name,
		Instance:             req.Instance,
		SkipDeterministicRun: true,
		CandyDirs:            candyDirsFromEnvelope(rp),
		Grader:               grader,
	})
	results := kit.RunPlan(ctx, runner, meta.Description, req.Strict)
	grading := "agent-graded prose"
	if req.NoAgent {
		grading = "deterministic-only"
	}
	header := fmt.Sprintf("Feature run (deploy scope, %s): %s (container: %s)", grading, meta.Box, containerName)
	return kit.CheckRunReply{Image: meta.Box, Steps: results, Header: header}, nil
}

// pluginCheckRunFeatureLiveVM is the VM arm of the feature-live mode: deploy-scope ADE
// acceptance against the running VM req.Name, wiring the host-side agent grader unless
// req.NoAgent. Mirrors live_gather.go's pluginCheckLiveVM's SSH-venue construction — the
// feature-live path was container-only (the core original never classified vm/local/group
// here either); this closes that gap so ADE can grade a VM deployment's plan.
func pluginCheckRunFeatureLiveVM(ex *sdk.Executor, ctx context.Context, rp *spec.ResolvedProject, tree map[string]spec.FleetNode, dir string, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	vmName, domainID, nestedLeaf := pluginResolveVmTarget(tree, req.Name)
	sp := pluginResolveVmSpec(rp, vmName)

	user := vmshared.ResolveCloudInitSSHUser(sp)
	port, err := deploykit.ResolveVmSshPort(sp, domainID)
	if err != nil {
		return kit.CheckRunReply{}, err
	}

	plan, user, port, err := pluginLoadVmCheckPlans(ctx, ex, rp, tree, req.Name, vmName, nestedLeaf, user, port)
	if err != nil {
		return kit.CheckRunReply{}, err
	}

	host := "127.0.0.1"
	var executor deploykit.DeployExecutor = &kit.SSHExecutor{Host: kit.VmSshAlias(domainID), ConnectTimeout: 10}
	if strings.Contains(req.Name, ".") {
		if _, chain, chainErr := deploykit.ResolveDeployChain(tree, req.Name, kit.ShellExecutor{}); chainErr == nil && chain != nil {
			executor = chain
		}
	}

	gate := &kit.SSHExecutor{Host: kit.VmSshAlias(domainID), ConnectTimeout: 5}
	if gerr := gate.WaitForSSH(ctx); gerr != nil {
		return kit.CheckRunReply{}, fmt.Errorf("vm %q is not up / SSH-reachable — is the domain running? %w", domainID, gerr)
	}
	if gerr := gate.WaitForCloudInit(ctx); gerr != nil {
		return kit.CheckRunReply{}, fmt.Errorf("vm %q cloud-init did not settle (still running or restarting?): %w", domainID, gerr)
	}

	env := map[string]string{
		"IMAGE":            req.Name,
		"INSTANCE":         req.Instance,
		"HOST_PORT:22":     strconv.Itoa(port),
		"CONTAINER_IP":     host,
		"CONTAINER_NAME":   "charly-" + domainID,
		"USER":             user,
		"HOME":             "/home/" + user,
		"VM_HOSTDEV_COUNT": strconv.Itoa(pluginVmHostdevCount(sp)),
		"DEPLOY_NAME":      kit.SanitizeDeployName("vm:" + vmName),
	}
	resolver := newPluginRuntimeCheckVarResolver(env)

	if len(plan) == 0 {
		return kit.CheckRunReply{NoSteps: true}, nil
	}
	set := &kit.LabelDescriptionSet{Deploy: []kit.LabeledDescription{{Origin: "vm:" + vmName, Plan: plan}}}

	envVars, hasRuntime := pluginResolverEnv(resolver)
	envVars = withRunVars(envVars, req.Vars)
	hostVars, hostCleanups := resolveHostVarsForSteps(ex, ctx, dir, plan, req.Instance)
	defer kit.CloseHostCleanups(hostCleanups)
	var grader kit.StepGrader
	if !req.NoAgent {
		ai, aerr := resolveAgentSpec(ex, ctx, rp.AgentBodies, req.Agent)
		if aerr != nil {
			return kit.CheckRunReply{}, aerr
		}
		grader = &kit.AgentGrader{Agent: ai, Target: req.Name, Instance: req.Instance, Timeout: req.Timeout}
	}
	runner := newPluginCheckRunner(ex, ctx, spec.CheckEnv{
		Mode:      "feature-live",
		Box:       req.Name,
		Instance:  req.Instance,
		Venue:     domainID,
		VenueKind: "vm",
	}, kit.RunnerConfig{
		Exec:                 executor,
		Mode:                 kit.ModeLive,
		Env:                  envVars,
		HasRuntime:           hasRuntime,
		Box:                  req.Name,
		Instance:             req.Instance,
		VmName:               domainID,
		SkipDeterministicRun: true,
		CandyDirs:            candyDirsFromEnvelope(rp),
		HostVars:             hostVars,
		TargetResolver:       pluginVenueResolver(ex, ctx, dir, req.Instance),
		Grader:               grader,
	})
	results := kit.RunPlan(ctx, runner, set, req.Strict)
	grading := "agent-graded prose"
	if req.NoAgent {
		grading = "deterministic-only"
	}
	header := fmt.Sprintf("Feature run (deploy scope, %s): %s (vm: %s)", grading, req.Name, domainID)
	return kit.CheckRunReply{Image: req.Name, Steps: results, Header: header}, nil
}

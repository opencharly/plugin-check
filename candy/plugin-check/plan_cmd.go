package check

// plan_cmd.go — `charly check plan <image>`: resolve a box's check plan and print it
// WITHOUT running it. The check runner resolves plans internally (ExtractMetadata →
// ResolveCheckVarsBuild) and only then executes; this leaf reuses the SAME resolve
// phase and emits the resolved steps (+ resolved env) as JSON. It exists because the
// failing-check diagnosis repeatedly needed the RESOLVED plan (which file paths a
// check asserts, which vars were substituted) and no command exposed it — the plan
// was only recoverable by digging through build logs.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// CheckPlanCmd resolves and prints a box's check plan.
type CheckPlanCmd struct {
	Image string `arg:"" help:"Image reference: a full ref, '<box>:<calver>' to pin one build, or a bare short name resolved against local container storage (refused when a newer local build of that box exists)"`
}

func (c *CheckPlanCmd) Run() error {
	_, err := hostCheckRun(spec.CheckRunRequest{Mode: "plan", Image: c.Image})
	return err
}

// planReply carries the resolved plan for JSON emission.
type planReply struct {
	Image string            `json:"image"`
	Env   map[string]string `json:"env"`
	Steps json.RawMessage   `json:"steps"`
}

// pluginCheckRunPlan mirrors pluginCheckRunBox's resolve phase and stops before
// RunPlan: it prints the RESOLVED plan steps as JSON so a caller can see exactly
// what a `charly check box` would assert (paths, contains, commands, vars).
func pluginCheckRunPlan(ex *sdk.Executor, ctx context.Context, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	imageRef, err := kit.ResolveBuiltImageRef(rt.RunEngine, req.Image)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	meta, err := deploykit.ExtractMetadata(rt.RunEngine, imageRef)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	if meta == nil || meta.Description == nil {
		return kit.CheckRunReply{Image: imageRef, NoSteps: true}, nil
	}
	resolver := kit.ResolveCheckVarsBuild(meta)
	env, _ := pluginResolverEnv(resolver)
	stepsJSON, err := json.Marshal(meta.Description)
	if err != nil {
		return kit.CheckRunReply{}, fmt.Errorf("check plan marshal steps: %w", err)
	}
	if env == nil {
		env = map[string]string{}
	}
	reply := planReply{Image: imageRef, Env: env, Steps: stepsJSON}
	out, err := json.Marshal(reply)
	if err != nil {
		return kit.CheckRunReply{}, fmt.Errorf("check plan marshal reply: %w", err)
	}
	fmt.Fprintln(os.Stdout, string(out))
	return kit.CheckRunReply{Image: imageRef, Steps: nil}, nil
}

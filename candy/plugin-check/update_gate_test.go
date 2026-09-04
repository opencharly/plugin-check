package check

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestUpdateGateFor pins the Step-5 gate-class resolution: the operator's
// --no-rebuild flag wins (forces skip — the pre-existing invariant), else the
// plan's authored update_gate: field, else the full gate. An unknown authored
// value ALSO falls back to full: a malformed plan must never silently skip the
// R10 acceptance gate.
func TestUpdateGateFor(t *testing.T) {
	for _, tc := range []struct {
		name      string
		noRebuild bool
		node      *spec.FleetNode
		want      string
	}{
		{"authored absent defaults to full", false, &spec.FleetNode{}, updateGateFull},
		{"nil node defaults to full", false, nil, updateGateFull},
		{"explicit full", false, &spec.FleetNode{UpdateGate: updateGateFull}, updateGateFull},
		{"restart-only", false, &spec.FleetNode{UpdateGate: updateGateRestartOnly}, updateGateRestartOnly},
		{"skip", false, &spec.FleetNode{UpdateGate: updateGateSkip}, updateGateSkip},
		{"--no-rebuild forces skip over full", true, &spec.FleetNode{UpdateGate: updateGateFull}, updateGateSkip},
		{"--no-rebuild forces skip over restart-only", true, &spec.FleetNode{UpdateGate: updateGateRestartOnly}, updateGateSkip},
		{"unknown authored value falls back to full", false, &spec.FleetNode{UpdateGate: "rebuild"}, updateGateFull},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := updateGateFor(bedRunOpts{NoRebuild: tc.noRebuild}, tc.node)
			if got != tc.want {
				t.Fatalf("updateGateFor() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestUpdateGateSteps pins the Step-5 GATE step argv by change class: full stays
// the tagged destroy+recreate `charly update` (zero regression), restart-only on
// a VM STOPS then STARTS the same per-deploy domain (the existing clone boots
// again — no destroy/recreate/reinstall), restart-only on a pod restarts the
// container, and restart-only on an in-place (local/external) bed falls back to
// full's own step (no restartable venue).
func TestUpdateGateSteps(t *testing.T) {
	const name, tag = "check-omarchy-eval", "check-omarchy-eval-2026.247.1900"
	vm := spec.CheckBedReply{IsVM: true, VMTemplate: "omarchy-vm", BedDomain: "check-omarchy-eval"}
	pod := spec.CheckBedReply{}
	local := spec.CheckBedReply{IsLocal: true}
	external := spec.CheckBedReply{IsExternal: true}

	for _, tc := range []struct {
		name      string
		gate      string
		d         spec.CheckBedReply
		isInPlace bool
		want      []gateStep
	}{
		{
			"full VM runs the tagged destroy+recreate update (unchanged)",
			updateGateFull, vm, false,
			[]gateStep{{"update", []string{"update", name, "--tag", tag}}},
		},
		{
			"restart-only VM stops then starts the SAME domain",
			updateGateRestartOnly, vm, false,
			[]gateStep{
				{"gate-restart-stop", []string{"vm", "stop", "omarchy-vm", "--domain", "check-omarchy-eval"}},
				{"gate-restart-start", []string{"vm", "start", "omarchy-vm", "--domain", "check-omarchy-eval"}},
			},
		},
		{
			"restart-only pod restarts the container (same image)",
			updateGateRestartOnly, pod, false,
			[]gateStep{{"gate-restart", []string{"restart", name}}},
		},
		{
			"restart-only local falls back to the in-place update",
			updateGateRestartOnly, local, true,
			[]gateStep{{"update", []string{"update", name, "--tag", tag}}},
		},
		{
			"restart-only external falls back to the in-place update",
			updateGateRestartOnly, external, true,
			[]gateStep{{"update", []string{"update", name, "--tag", tag}}},
		},
		{
			"full pod keeps the tagged update",
			updateGateFull, pod, false,
			[]gateStep{{"update", []string{"update", name, "--tag", tag}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := updateGateSteps(tc.gate, tc.d, name, tag, tc.isInPlace)
			if !equalGateSteps(got, tc.want) {
				t.Fatalf("updateGateSteps() = %v, want %v", got, tc.want)
			}
		})
	}
}

func equalGateSteps(got, want []gateStep) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i].name != want[i].name || !equalArgs(got[i].argv, want[i].argv) {
			return false
		}
	}
	return true
}

package check

// bed_run_vars_test.go — G-2 unit gate for the bed runner's CHECK_RUN_DIR /
// CALVER / BED var threading (the checkLiveTree seam in bed_run.go): the
// run-derived vars ride the check-live --var argv (→ CheckRunRequest.Vars →
// withRunVars → the runner env → ExpandOpVars), so an authored plan step can
// write its FINAL media path directly (e.g. an artifact string of
// "media/${CALVER}/${BED}/${BED}.cast") without shell derivation or a later
// move. Mirrors the checkvars_expand pattern (kit.ExpandTestVars) over the
// exact var map the runner hands plan steps.

import (
	"maps"
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// bedReply is the minimal run descriptor fixture the var-threading helpers need:
// CALVER (the run calver = the run dir's basename) + the run dir itself,
// single-sourced the same way bedSetup builds them (logDir = .check/<bed>/<calver>).
func bedReply(bed, calver string) spec.CheckBedReply {
	return spec.CheckBedReply{Calver: calver, LogDir: filepath.Join(".check", bed, calver)}
}

// TestBedShortName pins the media-stage short-name derivation for the check
// entities it must name: the omarchy acceptance beds (multi-word suites
// included), generic beds of the same shape, and the empty-string edge.
func TestBedShortName(t *testing.T) {
	tests := []struct {
		bed  string
		want string
	}{
		{"check-omarchy-accept-agents", "agents"},
		{"check-omarchy-accept-session", "session"},
		{"check-omarchy-accept-menu-bar", "menu-bar"},
		{"check-omarchy-accept-shell-surfaces", "shell-surfaces"},
		{"check-omarchy-accept-theme-media", "theme-media"},
		{"check-omarchy-accept-dev-migrations", "dev-migrations"},
		{"check-omarchy-accept-power-audio", "power-audio"},
		// Generic beds of the same shape: product segment stripped, short tail kept.
		{"check-k3s-vm", "vm"},
		{"check-sidecar-pod", "pod"},
		{"check-omarchy-eval-edge-inst", "eval-edge-inst"},
		// Fallbacks: a prefix-less name and a single-segment name are their own
		// short names; empty stays empty.
		{"plain", "plain"},
		{"check-foo", "foo"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := bedShortName(tc.bed); got != tc.want {
			t.Errorf("bedShortName(%q) = %q, want %q", tc.bed, got, tc.want)
		}
	}
}

// TestCheckRunVars_ThreadsRunVars asserts the run-derived vars land in the
// check-live per-run var map exactly once, that the operator's own --var
// passthrough survives, and that nil/empty inputs are safe (no panic, non-nil
// map — an empty CALVER expands to "" rather than resolving to a stale value).
func TestCheckRunVars_ThreadsRunVars(t *testing.T) {
	bed := "check-omarchy-accept-agents"
	d := bedReply(bed, "2026.254.0048")
	want := map[string]string{
		"CHECK_RUN_DIR": d.LogDir,
		"CALVER":        "2026.254.0048",
		"BED":           "agents",
	}
	got := checkRunVars(nil, d, bed)
	if !maps.Equal(got, want) {
		t.Errorf("checkRunVars(nil, …) = %v, want %v", got, want)
	}

	// Operator passthrough preserved alongside the authoritative run vars.
	got2 := checkRunVars(map[string]string{"pr": "9345"}, d, bed)
	want2 := maps.Clone(want)
	want2["pr"] = "9345"
	if !maps.Equal(got2, want2) {
		t.Errorf("checkRunVars(pr=9345, …) = %v, want %v", got2, want2)
	}

	// Empty-string safe: a nil --var map yields a non-nil map; an empty calver
	// yields an empty env value (the ref expands to "" — never a resolution
	// error, never a panic) and the call never panics on an empty name.
	vars := checkRunVars(nil, spec.CheckBedReply{}, "")
	if vars == nil {
		t.Fatalf("checkRunVars on an empty descriptor returned a nil map")
	}
	if vars["CALVER"] != "" || vars["BED"] != "" || vars["CHECK_RUN_DIR"] != "" {
		t.Errorf("checkRunVars(empty descriptor) = %v, want all-empty values", vars)
	}
}

// TestCheckRunVars_AuthoredStepExpands is the money gate: an authored step's
// final-media-path string (the G-2 shape) resolves through the exact var chain
// a bed run applies — checkRunVars → withRunVars over the venue's base env
// (the VM AND the pod arm both merge req.Vars this way) → ExpandTestVars —
// with no unresolved refs.
func TestCheckRunVars_AuthoredStepExpands(t *testing.T) {
	bed := "check-omarchy-accept-agents"
	calver := "2026.254.0245"
	d := bedReply(bed, calver)
	authored := "artifact: media/${CALVER}/${BED}/${BED}.cast"
	want := "artifact: media/" + calver + "/agents/agents.cast"

	runVars := checkRunVars(nil, d, bed)
	for name, base := range map[string]map[string]string{
		// The pod arm's base env (kit.ResolveCheckVarsRuntime output shape).
		"pod": {"IMAGE": bed},
		// The VM arm's base env (pluginCheckLiveVM's env map shape).
		"vm": {"IMAGE": bed, "INSTANCE": "", "USER": "omarchy", "HOME": "/home/omarchy"},
	} {
		env := withRunVars(base, runVars)
		expanded, missing := kit.ExpandTestVars(authored, env)
		if expanded != want {
			t.Errorf("[%s venue] ExpandTestVars(%q) = %q, want %q", name, authored, expanded, want)
		}
		if len(missing) != 0 {
			t.Errorf("[%s venue] unresolved refs %v in %q", name, missing, authored)
		}
	}
}

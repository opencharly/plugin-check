package check

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestPlanReplyJSON pins the `charly check plan` output shape: image + resolved
// env + the resolved steps as raw JSON. This is the contract the diagnosis flow
// needs (see plan_cmd.go doc) — a stable, greppable plan dump.
func TestPlanReplyJSON(t *testing.T) {
	steps := json.RawMessage(`[{"origin":"candy:github.com/opencharly/layer-direnv","check":"drop-in","plugin_input":{"exists":true,"file":"/etc/profile.d/charly-direnv-bash.sh"}}]`)
	r := planReply{Image: "ghcr.io/opencharly/sway-browser-vnc:test", Env: map[string]string{"HOME": "/home/user"}, Steps: steps}
	out, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(out)
	for _, want := range []string{"sway-browser-vnc", "HOME", "layer-direnv", "charly-direnv-bash.sh"} {
		if !strings.Contains(s, want) {
			t.Errorf("plan JSON missing %q: %s", want, s)
		}
	}
}

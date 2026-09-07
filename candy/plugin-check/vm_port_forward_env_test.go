package check

import (
	"reflect"
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestMergeVmForwardedHostPortVars pins the VM venue's forwarding runtime vars: every
// auto allocation becomes a HOST_PORT:<guest> var (the exact grammar the bed's addr:/http:
// checks use), allocations <= 0 are dropped, and a nil forwards map is a no-op.
func TestMergeVmForwardedHostPortVars(t *testing.T) {
	env := map[string]string{"HOST_PORT:22": "39389"}
	mergeVmForwardedHostPortVars(env, map[string]int{"18765": 41307, "8080": 0})
	want := map[string]string{"HOST_PORT:22": "39389", "HOST_PORT:18765": "41307"}
	if !reflect.DeepEqual(env, want) {
		t.Errorf("mergeVmForwardedHostPortVars = %#v, want %#v", env, want)
	}

	nilEnv := map[string]string{}
	mergeVmForwardedHostPortVars(nilEnv, nil)
	if len(nilEnv) != 0 {
		t.Errorf("mergeVmForwardedHostPortVars(nil forwards) mutated the env: %#v", nilEnv)
	}
}

// TestRewriteLoopbackPort pins the mcp_provide URL rewrite: a loopback URL whose port has a
// persisted auto-forward is rewritten to the allocated host port; unmapped ports, fixed
// ports, non-loopback URLs and malformed URLs are untouched.
func TestRewriteLoopbackPort(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		forwards map[string]int
		want    string
		wantOK  bool
	}{
		{
			name: "auto-forwarded guest port rewrites to the allocated host port",
			raw:  "http://127.0.0.1:18765/mcp",
			forwards: map[string]int{"18765": 41307},
			want:   "http://127.0.0.1:41307/mcp",
			wantOK: true,
		},
		{
			name: "localhost host rewrites too",
			raw:  "http://localhost:18765/mcp",
			forwards: map[string]int{"18765": 41307},
			want:   "http://localhost:41307/mcp",
			wantOK: true,
		},
		{
			name: "unmapped port left untouched",
			raw:  "http://127.0.0.1:9999/mcp",
			forwards: map[string]int{"18765": 41307},
			want:   "",
			wantOK: false,
		},
		{
			name: "non-loopback host left untouched",
			raw:  "http://vm.example:18765/mcp",
			forwards: map[string]int{"18765": 41307},
			want:   "",
			wantOK: false,
		},
		{
			name: "portless URL left untouched",
			raw:  "http://127.0.0.1/mcp",
			forwards: map[string]int{"18765": 41307},
			want:   "",
			wantOK: false,
		},
		{
			name: "malformed URL left untouched",
			raw:  "::not-a-url",
			forwards: map[string]int{},
			want:   "",
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := rewriteLoopbackPort(tc.raw, tc.forwards)
			if ok != tc.wantOK || got != tc.want {
				t.Errorf("rewriteLoopbackPort(%q) = (%q, %v), want (%q, %v)", tc.raw, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestHostRoutableMcpProvide pins the env-seed rewrite end-to-end off the template body: the
// bed's declared charly server (loopback, auto-forwarded) is rewritten; a nil forwards map
// (no deployed state) degrades to the unrewritten declarations.
func TestHostRoutableMcpProvide(t *testing.T) {
	rp := &spec.ResolvedProject{Templates: &spec.ProjectTemplates{VM: map[string]spec.RawBody{
		"cachyos-vm": spec.RawBody(`{"mcp_provide":[{"name":"charly","url":"http://127.0.0.1:18765/mcp","transport":"http"}]}`),
	}}}
	forwards := map[string]int{"18765": 41307}
	got := hostRoutableMcpProvide(rp, "cachyos-vm", forwards)
	want := []spec.CandyMCPProvide{{
		Name:      "charly",
		URL:       "http://127.0.0.1:41307/mcp", // the host-reachable address of the guest's MCP server
		Transport: "http",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hostRoutableMcpProvide = %#v, want %#v", got, want)
	}

	if got := hostRoutableMcpProvide(rp, "cachyos-vm", nil); got[0].URL != "http://127.0.0.1:18765/mcp" {
		t.Errorf("hostRoutableMcpProvide(nil forwards) = %#v, want the unrewritten template URL", got)
	}
}

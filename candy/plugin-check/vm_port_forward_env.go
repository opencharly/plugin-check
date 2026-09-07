package check

// vm_port_forward_env.go — the VM venue's substrate-neutral runtime vars + MCP endpoint
// threading (the plugin-side twin of the container-venue container-inspection leg, R3):
//
// mergeVmForwardedHostPortVars: a VM's declared network.port_forwards entries do NOT exist
// as HOST_PORT:<guest> runtime vars (only container venues have a container-inspection
// leg; sdk/kit checkvars.go's mergeRuntimeVars). The auto-allocated host ports ARE
// persisted (VmDeployState.PortForwards, written at vm-create by plugin-vm's
// resolveVmPortForwards), so the VM live-gather threads them into the check env vars
// itself — the same "${HOST_PORT:<guest>}" grammar a pod bed's addr:/http:/port checks
// use, now resolved for VM beds too.
//
// hostRoutableMcpProvide: the env's mcp_provide declaration (spec.CheckEnv.MCPProvide,
// seeded by pluginResolveVmMcpProvide from the vm template's raw body) carries the GUEST-
// side URL ("http://127.0.0.1:18765/mcp": the server inside the guest). The out-of-process
// mcp: verb dials from the HOST, where the guest loopback is only reachable through the
// vm's forwarded port — and plugin-mcp's fromEnv-loopback rule deliberately skips the
// endpoint rewrite (a host-local URL is dialed as-is; a VM with a fixed "18765:18765"
// forward IS reachable at 127.0.0.1:18765). For an auto: allocation the host port is NOT
// the guest port, so the seed rewrites the URL to the allocated host port — the same
// "guest address to host-routable address" translation the container label path performs
// via ResolveEndpoint, done at the only place that knows both the template URL and the
// persisted allocation.
import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// vmForwardedPortAllocations reads a VM domain's persisted auto-forward allocations
// (guest port to allocated host port) from the per-host deploy overlay, plugin-side
// (loaderkit.ResolveVmStateViaExecutor — the same reverse-channel read the deploy-vm /
// vm plugins use). domainID is the per-deploy DOMAIN identity (vm-create persists the
// state under the "vm:"+domainID deploy key — the same key ResolveVmSshPort reads).
// A nil overlay / missing entry degrades to nil: the same swallow-and-continue contract
// as the deployed-state reads in pluginLoadVmCheckPlans.
func vmForwardedPortAllocations(ex *sdk.Executor, ctx context.Context, domainID string) map[string]int {
	vs, err := loaderkit.ResolveVmStateViaExecutor(ctx, ex, domainID)
	if err != nil || vs == nil {
		return nil
	}
	return vs.PortForwards
}

// mergeVmForwardedHostPortVars augments env with the VM's forwarded-port runtime vars:
// "HOST_PORT:<guest>" = "<allocated host port>" for every auto: allocation — the VM
// analogue of the container-inspection leg's HOST_PORT:<container-port> entries. A fixed
// forward needs no var (the guest port IS the host port). A nil forwards map is a no-op.
func mergeVmForwardedHostPortVars(env map[string]string, forwards map[string]int) {
	for guest, host := range forwards {
		if host <= 0 {
			continue
		}
		env["HOST_PORT:"+guest] = strconv.Itoa(host)
	}
}

// hostRoutableMcpProvide returns the VM template's mcp_provide declarations with every
// loopback URL whose port has a persisted auto-forward rewritten to the allocated host
// port (the host-reachable address of the guest's MCP server). A declaration whose port is
// not forwarded (or forwarded fixed) is returned unchanged — the verb's loopback rule
// already dials it as-is. A nil forwards map (no state) returns the declarations unchanged.
func hostRoutableMcpProvide(rp *spec.ResolvedProject, vmName string, forwards map[string]int) []spec.CandyMCPProvide {
	provides := pluginResolveVmMcpProvide(rp, vmName)
	if len(provides) == 0 || len(forwards) == 0 {
		return provides
	}
	out := make([]spec.CandyMCPProvide, 0, len(provides))
	for _, p := range provides {
		if p.URL != "" && isLoopbackURL(p.URL) {
			if rewritten, ok := rewriteLoopbackPort(p.URL, forwards); ok {
				p.URL = rewritten
			}
		}
		out = append(out, p)
	}
	return out
}

// rewriteLoopbackPort rewrites a loopback URL's port to the forwarded host port when a
// persisted allocation maps that guest port. Only loopback hosts are rewritten (a
// non-loopback URL is already host-routable as authored); a URL with no port or an
// unmapped port is left untouched.
func rewriteLoopbackPort(raw string, forwards map[string]int) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Port() == "" {
		return "", false
	}
	host := u.Hostname()
	if host != "127.0.0.1" && host != "localhost" {
		return "", false
	}
	guest := u.Port()
	n, err := strconv.Atoi(guest)
	if err != nil {
		return "", false
	}
	hostPort, ok := forwards[strconv.Itoa(n)]
	if !ok || hostPort <= 0 {
		return "", false
	}
	u.Host = fmt.Sprintf("%s:%d", host, hostPort)
	return u.String(), true
}

// isLoopbackURL reports whether raw's host is loopback (127.0.0.1 / localhost) — mirrors
// the mcp: verb's own skip rule (candy/plugin-mcp resolve.go), so the rewrite targets
// exactly the URLs the verb would dial unrewritten.
func isLoopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost":
		return true
	}
	return false
}

package check

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// repairStrandedContainerDNS makes container-name resolution work before members come up,
// repairing it when a previous teardown has broken it.
//
// THE BUG THIS FIXES IS OURS. podman destroys the rootless network namespace when the
// container count reaches zero and builds a fresh one on the next start, but it does not
// restart aardvark-dns: the daemon keeps serving the namespace that no longer exists for
// anybody. A disposable bed's own cleanup-members phase is the thing that most reliably
// empties an idle host, so running a bed to completion is what breaks DNS for the NEXT bed —
// including other people's, since the damage is host-wide.
//
// Nothing looks wrong afterwards. The aardvark process is alive, the bridge is up in the new
// namespace with the right gateway, the zone file is correct and even lists the new
// containers, and `podman network inspect` still reports dns_enabled. Only the listener is in
// the wrong namespace, so every `getent hosts <peer>` inside every container returns empty and
// surfaces much later as "cannot resolve <name>" in a plan step.
//
// Repairing it at bring-up rather than documenting a manual runbook is the point: a bed that
// silently poisons the next run is not a gate, and asking operators to notice and hand-fix it
// is the workaround, not the fix. This is idempotent and a no-op on a healthy host.
func repairStrandedContainerDNS(logf func(string, ...any)) {
	pid := aardvarkPID()
	if pid == "" {
		return // not running: podman starts it with the first container, in the right namespace
	}
	got, err := os.Readlink("/proc/" + pid + "/ns/net")
	if err != nil {
		return // cannot read it; do not guess
	}
	want, err := podmanRootlessNetns()
	if err != nil {
		want = ""
	}
	if !containerDNSNeedsRepair(pid, got, want) {
		return
	}

	logf("container DNS: aardvark-dns (pid %s) is serving %s but podman uses %s — "+
		"repairing, container-name resolution is dead until it is", pid, got, want)

	// SIGKILL, not SIGTERM: a stranded aardvark ignores TERM (observed).
	_ = exec.Command("kill", "-9", pid).Run()

	// The pidfile must go too. podman consults it and, finding a pid it believes is a healthy
	// daemon, starts no replacement — so killing alone leaves DNS dead.
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		_ = os.Remove(rt + "/containers/networks/aardvark-dns/aardvark.pid")
	}

	if out, err := exec.Command("podman", "network", "reload", "--all").CombinedOutput(); err != nil {
		logf("container DNS: reload failed: %v: %s", err, strings.TrimSpace(string(out)))
		return
	}
	if np := aardvarkPID(); np != "" {
		if ns, err := os.Readlink("/proc/" + np + "/ns/net"); err == nil {
			logf("container DNS: repaired — aardvark-dns pid %s now in %s", np, ns)
			return
		}
	}
	logf("container DNS: reload issued; aardvark-dns restarts with the next container")
}

// aardvarkPID returns a running aardvark-dns pid, or "".
//
// It scans /proc rather than shelling out to `pgrep -f`, whose pattern form also matches the
// caller's own command line — which turns a lookup into a self-kill when the result is passed
// to kill.
func aardvarkPID() string {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return ""
	}
	for _, e := range entries {
		name := e.Name()
		if name == "" || name[0] < '0' || name[0] > '9' {
			continue
		}
		b, err := os.ReadFile("/proc/" + name + "/cmdline")
		if err != nil || len(b) == 0 {
			continue
		}
		if strings.Contains(strings.ReplaceAll(string(b), "\x00", " "), "aardvark-dns") {
			return name
		}
	}
	return ""
}

// podmanRootlessNetns reports the network namespace podman currently uses for rootless
// networking — the one a container's DNS queries actually reach.
func podmanRootlessNetns() (string, error) {
	out, err := exec.Command("podman", "unshare", "--rootless-netns",
		"readlink", "/proc/self/ns/net").Output()
	if err != nil {
		return "", fmt.Errorf("read rootless netns: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// containerDNSNeedsRepair is the decision, split from the /proc and podman reads so the
// stranded case can be exercised in a test. Reproducing it for real means breaking DNS for
// every container on the host, including other sessions' work.
//
// Repair ONLY on a definite mismatch: an unknown namespace, an absent daemon, or an
// unavailable comparison must never trigger a kill, or a healthy host gets its DNS restarted
// by a bed that had no evidence anything was wrong.
func containerDNSNeedsRepair(pid, got, want string) bool {
	if pid == "" || got == "" || want == "" {
		return false
	}
	return got != want
}

// currentPID is used by the guard proving aardvarkPID never returns the calling process —
// the value is handed to kill -9, so a self-match would kill the bed runner mid-run.
func currentPID() string { return strconv.Itoa(os.Getpid()) }

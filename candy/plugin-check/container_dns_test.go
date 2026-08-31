package check

import "testing"

// A bed's own cleanup empties an idle host, which makes podman rebuild the rootless network
// namespace and strands aardvark-dns in the old one. The next bed then finds container-name
// resolution dead. Repairing at bring-up is what stops a green run from poisoning the next
// one; this pins the condition that triggers it.
func TestStrandedNamespaceTriggersRepair(t *testing.T) {
	if !containerDNSNeedsRepair("1906277", "net:[4026534654]", "net:[4026533620]") {
		t.Fatal("a namespace mismatch must trigger repair — this is the whole failure")
	}
}

// The healthy case is overwhelmingly the common one. Repairing here would restart DNS for
// every container on the host on every bed run, which is worse than the bug.
func TestMatchingNamespaceLeavesDNSAlone(t *testing.T) {
	if containerDNSNeedsRepair("2451338", "net:[4026533620]", "net:[4026533620]") {
		t.Fatal("matching namespaces must not be touched")
	}
}

// Repair kills a process and reloads networking for the whole host. Doing that on incomplete
// evidence — no daemon, an unreadable namespace, or podman not answering — would let a bed
// break a host that was fine.
func TestIncompleteEvidenceNeverRepairs(t *testing.T) {
	cases := []struct {
		name, pid, got, want string
	}{
		{"no daemon running", "", "", "net:[4026533620]"},
		{"namespace unreadable", "1906277", "", "net:[4026533620]"},
		{"podman did not answer", "1906277", "net:[4026534654]", ""},
		{"nothing known at all", "", "", ""},
	}
	for _, c := range cases {
		if containerDNSNeedsRepair(c.pid, c.got, c.want) {
			t.Errorf("%s: must not repair on incomplete evidence", c.name)
		}
	}
}

// aardvarkPID scans /proc instead of shelling out to `pgrep -f aardvark-dns`, whose pattern
// form also matches the caller's own command line. That is not hypothetical: the same mistake
// made with pkill during this investigation killed the caller (exit 144). Here the result is
// passed to kill -9, so a self-match would kill the bed runner mid-run.
func TestAardvarkPIDDoesNotMatchItself(t *testing.T) {
	// The test binary's own command line contains "container_dns" and the package name, but
	// never the daemon's argv0 — so a correct scan cannot return this process.
	if pid := aardvarkPID(); pid != "" {
		if pid == currentPID() {
			t.Fatalf("aardvarkPID returned the calling process (%s) — kill -9 would kill the bed runner", pid)
		}
	}
}

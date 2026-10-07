package check

import (
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// venue_test.go — the venue-CLASSIFIER unit coverage (checkVmTarget / checkLocalTarget /
// resolveLeafVenue + the "." fast-path), ported from the deleted core charly/check_venue_test.go in
// the #118 check broker-envelope-out cutover: the classifier relocated here (venue.go), so its
// regression coverage — the RCA #12 leaf-under-pod cases + the pod-not-host-routed guard — lives
// here too. The plugin classifier reads the STAMPED venue trait (node.Descent.Venue) off the
// resolved-project deploy tree, so the fixtures set Descent.Venue directly (ssh=vm, shell=local,
// container=pod) rather than the core original's Target:/uf.VM() shape.

// desc builds a Descent for a venue WORD through the REAL derivation
// (spec.DescentFromTraits) with the SAME #DeployTraits the substrate provider declares — so a
// fixture's stamped transport/venue/exclusive_venue match what the loader produces live, and a
// predicate that reads any of them is exercised against real data (not a hand-built stub).
//
//	"ssh"       → the vm row (ExclusiveVenue: the host libvirt domain).
//	"kubevirt"  → the kubevirt row (ssh transport, NO ExclusiveVenue — the plugin-owned CR).
//	"shell"     → the local row.
//	"container" → the pod row.
func desc(venue string) *spec.DescentDescriptor {
	switch venue {
	case "ssh":
		return spec.DescentFromTraits(&spec.DeployTraits{Venue: "ssh", MachineVenue: true, ExclusiveVenue: true, BedTarget: true})
	case "kubevirt":
		return spec.DescentFromTraits(&spec.DeployTraits{Venue: "kubevirt", ImageBacked: true, BedTarget: true})
	case "shell":
		return spec.DescentFromTraits(&spec.DeployTraits{Venue: "shell", MachineVenue: true, BedTarget: true})
	default: // container / pod
		return spec.DescentFromTraits(&spec.DeployTraits{Venue: venue, ImageBacked: true, BedTarget: true})
	}
}

// newVenueTestTree covers every venue class the classifier must distinguish, off the stamped
// Descent.Venue trait every loader-stamped node carries.
func newVenueTestTree() map[string]spec.DeployNode {
	return map[string]spec.DeployNode{
		"cachyos-gpu": {Descent: desc("ssh")}, // vm entity (ssh venue): its own name IS the domain identity
		"web-pod": {Descent: desc("container"), Member: []spec.Member{
			// RCA #12: a vm CHILD (ssh) nested under a non-vm (container) parent — the leaf, not
			// the root, is the vm. And a local (shell) leaf under the same pod root.
			{Name: "web-pod-vm", Position: spec.PositionInSubstrate, Node: &spec.DeployNode{Descent: desc("ssh")}},
			{Name: "web-pod-local", Position: spec.PositionInSubstrate, Node: &spec.DeployNode{Descent: desc("shell")}},
		}},
		"k3s-vm": {Descent: desc("ssh"), Member: []spec.Member{
			// Delegate-into-guest: the ROOT is the vm, the leaf is something nested INSIDE its
			// guest — the leaf check falls through to the root fallback, not a second vm.
			{Name: "inner-app", Position: spec.PositionInSubstrate, Node: &spec.DeployNode{Descent: desc("shell")}},
		}},
		"bare-vm-dep": {Descent: desc("ssh")},
		// A NAMESPACE-QUALIFIED top-level deploy key (the umbrella-root form): the dots are
		// namespace separators, NOT member-path separators. tree["charly"] does NOT exist, so the
		// dotted-path walk missed it and checkVmTarget fell through to a container lookup.
		"charly.check-charly-vm": {Descent: desc("ssh")},
		// The LOCAL sibling of the above: a namespace-qualified `kind: local` bed. No `charly`
		// root exists, so the in-substrate member walk cannot reach it — the opencharly/plugin-check#78
		// regression (the local arm resolved via the member walk and reported "not found").
		"charly.check-task": {Descent: desc("shell")},
		"my-local": {Descent: desc("shell"), Member: []spec.Member{
			// A NON-HOST leaf under a HOST root — the branch checkLocalTarget's dispatch decides
			// (a resolved non-host leaf must NOT be local-routed via its root). Before the fix it
			// fell through to the root fallback and matched (opencharly/plugin-check#86).
			{Name: "pod-child", Position: spec.PositionInSubstrate, Node: &spec.DeployNode{Descent: desc("container")}},
		}},
		"remote-host": {Descent: desc("shell"), Host: "user@box"},
	}
}

func TestCheckVmTarget(t *testing.T) {
	tree := newVenueTestTree()
	cases := []struct {
		name       string
		wantDomain string // the per-deploy DOMAIN IDENTITY (deploy key, dots→dashes) — P33
		wantOK     bool
	}{
		{"cachyos-gpu", "cachyos-gpu", true}, // vm (ssh venue): its own name IS the domain identity
		{"k3s-vm", "k3s-vm", true},           // vm deploy → the DEPLOY key
		{"bare-vm-dep", "bare-vm-dep", true},
		{"k3s-vm.inner", "k3s-vm", true}, // dotted root is the vm deploy, leaf unresolvable → root fallback
		// A namespace-qualified top-level key: the EXACT full key must resolve FIRST (its dots are
		// namespace separators, not a member path). Without the exact-key-first fix this is
		// ("", false) — the regression that made a qualified VM bed's check-live look for a
		// container instead of the SSH venue.
		{"charly.check-charly-vm", "charly-check-charly-vm", true},
		// RCA #12: leaf-vm-under-pod — the pod ROOT is not a vm, but the LEAF is. domainID keys
		// off the FULL dotted path, sanitized by VmDomainIdentity ("." → "-").
		{"web-pod.web-pod-vm", "web-pod-web-pod-vm", true},
		// RCA #12 preserved precedent: root-vm-with-guest-suffix — the leaf resolves but is NOT
		// a vm (shell, nested inside k3s-vm's guest) → root fallback, keyed off the VM ROOT.
		{"k3s-vm.inner-app", "k3s-vm", true},
		{"web-pod", "", false},               // pod is not a vm
		{"web-pod.web-pod-local", "", false}, // leaf under a pod root that is itself local, not vm
		{"my-local", "", false},              // local is not a vm
		{"nonexistent", "", false},           // unknown
	}
	for _, tc := range cases {
		gotDomain, gotOK := checkVmTarget(tree, tc.name)
		if gotOK != tc.wantOK || gotDomain != tc.wantDomain {
			t.Errorf("checkVmTarget(%q) = (%q, %v), want (%q, %v)",
				tc.name, gotDomain, gotOK, tc.wantDomain, tc.wantOK)
		}
	}
}

func TestCheckVmTargetEmptyTree(t *testing.T) {
	if vm, ok := checkVmTarget(nil, "anything"); ok || vm != "" {
		t.Errorf("checkVmTarget(nil, …) = (%q, %v), want (\"\", false)", vm, ok)
	}
}

// TestMergeBedsIntoTree pins the classification-only bed fold: namespace-qualified
// beds (uf.Beds()) are added so a qualified bed can be venue-classified from the
// umbrella root, while an existing ROOT-level entry is never clobbered. This is the
// narrow seam that replaces folding into the build-validated rp.Deploy (which
// surfaced cross-namespace validation errors).
func TestMergeBedsIntoTree(t *testing.T) {
	root := spec.DeployNode{Descent: desc("shell")}
	bed := spec.DeployNode{Descent: desc("ssh")}

	// A nil (empty-envelope) tree plus the beds fold yields the qualified bed.
	if got := mergeBedsIntoTree(nil, map[string]spec.DeployNode{"charly.check-x-vm": bed}); func() bool {
		_, ok := got["charly.check-x-vm"]
		return !ok
	}() {
		t.Fatalf("namespaced bed absent after fold")
	}

	// A root-level entry wins a key collision; the bed still lands.
	got := mergeBedsIntoTree(
		map[string]spec.DeployNode{"root-pod": root},
		map[string]spec.DeployNode{"root-pod": bed, "charly.check-x-vm": bed},
	)
	if n, ok := got["root-pod"]; !ok || n.Descent == nil || n.Descent.Venue != "shell" {
		t.Errorf("root entry clobbered by the fold: %+v", n)
	}
	if _, ok := got["charly.check-x-vm"]; !ok {
		t.Errorf("namespaced bed absent after fold: %v", got)
	}
}

func TestCheckLocalTarget(t *testing.T) {
	tree := newVenueTestTree()
	cases := []struct {
		name   string
		wantOK bool
		host   string
	}{
		{"my-local", true, ""},            // shell venue (host:local default)
		{"remote-host", true, "user@box"}, // shell venue carrying host:<remote>
		{"my-local.child", true, ""},      // dotted root is shell, leaf unresolvable → root fallback
		// opencharly/plugin-check#86: a RESOLVED NON-HOST leaf under a HOST root must NOT be
		// local-routed via its root. This case FAILS on the pre-fix code, which fell through to the
		// root fallback and returned true.
		{"my-local.pod-child", false, ""},
		// opencharly/plugin-check#78: a NAMESPACE-QUALIFIED local bed (its dots are namespace
		// separators). checkLocalTarget already resolved it via resolveLeafVenue; the local
		// ARM's divergent in-substrate resolver is what failed.
		{"charly.check-task", true, ""},
		// RCA #12: local-leaf-under-pod — the pod ROOT is not host-venue, but the LEAF (shell) is.
		{"web-pod.web-pod-local", true, ""},
		{"web-pod.web-pod-vm", false, ""}, // leaf under a pod root that is itself a vm, not local
		// A pod (container venue) is NEVER host-routed — its check venue is the running container
		// (published ports), not the host. Regression guard from commit 7a38cc3a: masked while pod
		// beds used fixed H:C==9222:9222 ports; surfaced with auto-allocated host ports.
		{"web-pod", false, ""},
		{"cachyos-gpu", false, ""}, // vm (ssh) is not a local deploy
		{"k3s-vm", false, ""},      // vm is not local
	}
	for _, tc := range cases {
		node, gotOK := checkLocalTarget(tree, tc.name)
		if gotOK != tc.wantOK {
			t.Errorf("checkLocalTarget(%q) ok = %v, want %v", tc.name, gotOK, tc.wantOK)
			continue
		}
		if gotOK && node.Host != tc.host {
			t.Errorf("checkLocalTarget(%q) node.Host = %q, want %q", tc.name, node.Host, tc.host)
		}
	}
}

func TestCheckLocalTargetEmptyTree(t *testing.T) {
	if _, ok := checkLocalTarget(nil, "anything"); ok {
		t.Errorf("checkLocalTarget(nil, …) ok = true, want false")
	}
}

// TestResolveLocalDeployNode pins the opencharly/plugin-check#78 fix: the LOCAL arm must resolve a
// dotted bed the SAME way the dispatcher (checkLocalTarget) does — exact-key-first via
// resolveDeployNodeByPath — not via the in-substrate-only member walk, which cannot reach a
// deploy-level entity.
func TestResolveLocalDeployNode(t *testing.T) {
	tree := newVenueTestTree()

	// A NAMESPACE-QUALIFIED local bed: dots are namespace separators, not a member path.
	node, root, ok := resolveLocalDeployNode(tree, "charly.check-task")
	if !ok || node == nil || root == nil {
		t.Fatalf("resolveLocalDeployNode(charly.check-task) ok=%v node=%v root=%v, want resolved", ok, node, root)
	}
	// PREMISE GUARD: the in-substrate-only walk cannot reach it — the exact regression this fixes.
	if n := resolveNestedNode(tree, "charly.check-task"); n != nil {
		t.Fatalf("premise broken: resolveNestedNode reached a namespace-qualified key")
	}

	// A GENUINE member path: the leaf resolves and the ROOT is its first segment.
	node, root, ok = resolveLocalDeployNode(tree, "web-pod.web-pod-local")
	if !ok || node == nil {
		t.Fatalf("resolveLocalDeployNode(web-pod.web-pod-local) not resolved")
	}
	if root == nil || root.Descent == nil || root.Descent.Venue != "container" {
		t.Fatalf("member-path root = %+v, want the `web-pod` (container) root", root)
	}

	// Unknown stays unknown.
	if _, _, ok := resolveLocalDeployNode(tree, "nope.not-here"); ok {
		t.Fatalf("resolveLocalDeployNode(unknown) ok = true, want false")
	}
}

// TestResolveCheckVenueLocalDot verifies the "." fast-path returns a host venue without touching
// the resolved-project envelope (the in-guest delegation target) — so it needs no executor.
func TestResolveCheckVenueLocalDot(t *testing.T) {
	v, err := resolveCheckVenue(nil, nil, "", ".", "")
	if err != nil {
		t.Fatalf("resolveCheckVenue(\".\") error: %v", err)
	}
	if v.Kind != "host" {
		t.Errorf("resolveCheckVenue(\".\").Kind = %q, want host", v.Kind)
	}
	if _, ok := v.Exec.(kit.ShellExecutor); !ok {
		t.Errorf("resolveCheckVenue(\".\").Exec = %T, want ShellExecutor", v.Exec)
	}
	if v.IsContainer() {
		t.Errorf("resolveCheckVenue(\".\").IsContainer() = true, want false")
	}
}

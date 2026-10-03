package check

import (
	"os"
	"strings"
	"testing"
)

// bed_kubectl_deprecation_allowance_test.go pins the kubectl API-deprecation exemption added
// for opencharly/plugin-check#71. The stock k3s addon manifests (Traefik / local-path) still
// select the legacy `node-role.kubernetes.io/master` label, so kubectl prints this deprecation
// warning on every bed whose deploy applies them — the single un-allowlisted `warnings=1` on
// the deploy-add step of the check-kubevirt-* / check-k3s-vm beds. The entry MUST claim exactly
// this sentence and MUST NOT claim any other `Warning:` line.
func TestKubectlNodeRoleMasterDeprecationAllowance(t *testing.T) {
	line := `Warning: spec.template.spec.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[1].matchExpressions[0].key: node-role.kubernetes.io/master is use "node-role.kubernetes.io/control-plane" instead`
	sev, _, ok := classifyDiagnosticLine(line)
	if !ok {
		t.Fatalf("%q was not recognised as a diagnostic at all", line)
	}
	if sev != severityWarning {
		t.Fatalf("%q: want the warning tier, got %v", line, sev)
	}
	a := allowanceFor(sev, line)
	if a == nil || a.ID != "kubectl-node-role-master-deprecation" {
		t.Fatalf("%q: want the kubectl-node-role-master-deprecation allowance, got %v", line, a)
	}

	// A DIFFERENT kubectl Warning must not be swallowed.
	other := `Warning: spec.template.spec.containers[0].resources: some other deprecation`
	if sev, _, ok := classifyDiagnosticLine(other); ok {
		if got := allowanceFor(sev, other); got != nil && got.ID == "kubectl-node-role-master-deprecation" {
			t.Errorf("%q must NOT be claimed by the node-role-master entry", other)
		}
	}
}

// TestKubectlDeprecationWarningGoesToZero proves the 1 -> 0 change over the retained deploy-add
// line: scanning it alone yields 0 un-allowlisted warnings once the entry exists.
func TestKubectlDeprecationWarningGoesToZero(t *testing.T) {
	log := strings.Join([]string{
		`Warning: spec.template.spec.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[1].matchExpressions[0].key: node-role.kubernetes.io/master is use "node-role.kubernetes.io/control-plane" instead`,
		"",
	}, "\n")
	d := scanStepDiagnostics(log)
	if d.Warnings != 0 {
		t.Errorf("the kubectl deprecation warning must be allowlisted; un-allowlisted warnings = %d (want 0)", d.Warnings)
	}
}

// TestKubectlDeprecationRetainedExcerpt proves the anchored regex matches the BYTE-FOR-BYTE
// line a real k3s bed emitted — not a string typed into the test. The excerpt is the verbatim
// line from .check/check-kubevirt-operator/2026.276.0239/deploy-add.log:1199, whose summary.yml
// reported deploy-add `warnings=1` (this line the single un-allowlisted finding). With the entry
// the same bytes classify as allowlisted; without it they are the counted warning.
func TestKubectlDeprecationRetainedExcerpt(t *testing.T) {
	b, err := os.ReadFile("testdata/kubectl-deprecation/deploy-add-excerpt.log")
	if err != nil {
		t.Fatal(err)
	}
	d := scanStepDiagnostics(string(b))
	if d.Warnings != 0 {
		t.Errorf("the retained deploy-add excerpt must scan to 0 un-allowlisted warnings (the entry must match the real bytes); got warnings=%d", d.Warnings)
	}
	if d.Errors != 0 {
		t.Errorf("retained excerpt: want 0 errors, got %d", d.Errors)
	}
}

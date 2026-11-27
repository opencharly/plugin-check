package check

import (
	"slices"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// A bed run's live stage asks for bed actions (#153): without the flag, the bed's own run:
// steps -- its scenario actions -- were skipped as verify-only, so a bed could only express
// an action as a check: step, a verb changing state (against D-VERB-1).
func TestCheckLiveArgvAsksForBedActions(t *testing.T) {
	argv := checkLiveArgv("check-kde-cstream-pod", map[string]string{"BED": "pod"})
	if !slices.Contains(argv, "--bed-actions") {
		t.Fatalf("check-live argv %v must carry --bed-actions", argv)
	}
	if strings.Join(argv[:3], " ") != "check live check-kde-cstream-pod" {
		t.Errorf("argv = %v, want it to start with: check live check-kde-cstream-pod", argv)
	}
}

// Bed actions are refused on anything that is not disposable (D-DEP-3), and allowed on a
// disposable deployment and an ephemeral one (ephemeral implies disposable).
func TestBedActionsAuthorizedOnlyOnADisposableDeployment(t *testing.T) {
	yes := true
	tree := map[string]spec.DeployNode{
		"bed":       {Disposable: &yes},
		"ephemeral": {Ephemeral: &spec.EphemeralLifetime{}},
		"prod":      {},
	}
	for name, wantOK := range map[string]bool{"bed": true, "ephemeral": true, "prod": false, "absent": false} {
		err := bedActionsAuthorized(tree, name)
		if (err == nil) != wantOK {
			t.Errorf("bedActionsAuthorized(%q) = %v, want ok=%v", name, err, wantOK)
		}
		if err != nil && !strings.Contains(err.Error(), "disposable") {
			t.Errorf("the refusal must name the rule, got: %v", err)
		}
	}
}

// A NESTED (in-substrate) member of a `disposable: true` parent runs its bed actions INSIDE
// the parent's venue, so the PARENT's disposability authorizes it (D-DEP-3) — the nested
// member need not repeat `disposable: true`. This reproduces the charly `check-group` failure
// (opencharly/charly#1299): `check-group-member` under a disposable `eval-vm` root was refused
// because only the resolved LEAF node was checked, and the leaf does not carry the parent's
// flag.
func TestBedActionsAuthorizedNestedMemberOfDisposableParent(t *testing.T) {
	yes := true
	mkNode := func(disposable bool) *spec.Deploy {
		n := &spec.Deploy{Target: "local"}
		if disposable {
			n.Disposable = &yes
		}
		return n
	}
	tree := map[string]spec.DeployNode{
		// Disposable parent, non-disposable nested member → authorized via the parent.
		"bed": {
			Disposable: &yes,
			Member:     []spec.Member{{Name: "child", Position: spec.PositionInSubstrate, Node: mkNode(false)}},
		},
		// Non-disposable parent, non-disposable nested member → refused.
		"prod": {
			Member: []spec.Member{{Name: "child", Position: spec.PositionInSubstrate, Node: mkNode(false)}},
		},
		// Non-disposable parent, nested member that IS itself disposable → authorized (leaf).
		"half": {
			Member: []spec.Member{{Name: "child", Position: spec.PositionInSubstrate, Node: mkNode(true)}},
		},
	}
	for name, wantOK := range map[string]bool{
		"bed.child":  true,
		"prod.child": false,
		"half.child": true,
		"bed.absent": false,
	} {
		err := bedActionsAuthorized(tree, name)
		if (err == nil) != wantOK {
			t.Errorf("bedActionsAuthorized(%q) = %v, want ok=%v", name, err, wantOK)
		}
		if err != nil && !strings.Contains(err.Error(), "disposable") {
			t.Errorf("the refusal must name the rule, got: %v", err)
		}
	}
}

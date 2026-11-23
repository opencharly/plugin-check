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

package check

import (
	"os"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// The debug-retention notice is the ONE thing an operator reads at the moment a bed fails,
// and every command it prints must be runnable as printed. It regressed by naming the shared
// kind:vm ENTITY (VMTemplate) where the per-deploy DOMAIN IDENTITY (BedDomain) is required:
// `charly vm ssh omarchy-vm` resolves the alias charly-omarchy-vm, which does not exist, so
// the hint died with NXDOMAIN while the real domain charly-check-omarchy-desktop-vm sat
// running. The entity and the deploy differ for EVERY bed (a bed derives from a shared
// template), so this is not an edge case — the hint was wrong every time it printed.
func TestDebugRetentionNotice_VMHintsUseDeployDomain(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	printDebugRetentionNotice(w, "check-omarchy-desktop-vm", spec.CheckBedReply{
		IsVM:       true,
		VMTemplate: "omarchy-vm",
		BedDomain:  "check-omarchy-desktop-vm",
	})
	w.Close()
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	got := string(buf[:n])

	// The ssh target is the domain identity, never the entity.
	if !strings.Contains(got, "charly vm ssh check-omarchy-desktop-vm") {
		t.Errorf("ssh hint does not target the deploy domain:\n%s", got)
	}
	if strings.Contains(got, "charly vm ssh omarchy-vm") {
		t.Errorf("ssh hint still targets the kind:vm ENTITY (unresolvable alias):\n%s", got)
	}
	// destroy is entity-scoped BUT must carry --domain, matching the cleanup path in
	// bed_run.go, whose `vm destroy <entity> --domain <BedDomain>` is the working form.
	if !strings.Contains(got, "charly vm destroy omarchy-vm --domain check-omarchy-desktop-vm") {
		t.Errorf("destroy hint is missing the --domain the cleanup path uses:\n%s", got)
	}
}

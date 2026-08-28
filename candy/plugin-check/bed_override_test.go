package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBedVerdictLineNamesRepoOverride pins issue #340: a bed verdict must name the
// CHARLY_REPO_OVERRIDE pair when one was active, so a reader knows the verdict is
// about the LOCAL tree, not the PR head. Without the override the line is unchanged.
func TestBedVerdictLineNamesRepoOverride(t *testing.T) {
	with := bedVerdictLine("check-foo", true, 3, "opencharly/charly=/home/me/oc-charly")
	if !strings.Contains(with, "repo override active") {
		t.Fatalf("bedVerdictLine() with override = %q, want the override named in the verdict", with)
	}
	if !strings.Contains(with, "opencharly/charly=/home/me/oc-charly") {
		t.Fatalf("bedVerdictLine() with override = %q, want the pair itself named", with)
	}
	if !strings.Contains(with, "LOCAL tree, not the PR head") {
		t.Fatalf("bedVerdictLine() with override = %q, want the local-tree caveat", with)
	}
	without := bedVerdictLine("check-foo", true, 3, "")
	if strings.Contains(without, "repo override") {
		t.Fatalf("bedVerdictLine() without override = %q, want no override mention", without)
	}
	if !strings.Contains(without, "charly check run check-foo: PASS (steps=3)") {
		t.Fatalf("bedVerdictLine() without override = %q, want the plain verdict shape", without)
	}
}

// TestWriteBedSummaryCarriesRepoOverride pins that the persistent summary.yml also
// records the override pair, so the artifact a validator reads carries the caveat.
func TestWriteBedSummaryCarriesRepoOverride(t *testing.T) {
	dir := t.TempDir()
	res := &bedRunResult{
		Bed:          "check-foo",
		CalVer:       "2026.240.1230",
		OK:           true,
		RepoOverride: "opencharly/charly=/home/me/oc-charly",
		Step:         []stepResult{{Name: "image-build", OK: true}},
	}
	writeBedSummary(dir, res)
	raw, err := os.ReadFile(filepath.Join(dir, "summary.yml"))
	if err != nil {
		t.Fatalf("read summary.yml: %v", err)
	}
	if !strings.Contains(string(raw), "repo_override: opencharly/charly=/home/me/oc-charly") {
		t.Fatalf("summary.yml = %q, want the repo_override line", string(raw))
	}
}

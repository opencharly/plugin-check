package check

import (
	"os"
	"strings"
	"testing"
)

// bed_diagnostics_store_bloat_test.go — the allowlist entry for plugin-build's podman-store
// hygiene nudge, and the boundary that keeps it narrow.
//
// THE CONTRADICTION THIS FILE PINS. plugin-build's own source declares the nudge non-blocking —
// "It is fail-soft by design: a store probe that fails (podman absent, non-JSON output, no Images
// entry) is skipped silently — the warning is a hygiene nudge, never a build blocker"
// (plugin-build/candy/plugin-build/store_bloat.go, above storeBloatReclaimableThreshold) — while
// the check scanner counts the same line in its warning tier, and R10 succeeds only at ZERO
// warnings. So the validator turns a line its emitter declares non-blocking into a merge BLOCK.
// The condition it reports is a SHARED host store's reclaimable bytes above 50 GiB, which drifts
// as beds build, so the SAME change passes one run and is gated the next.
//
// These tests prove three things and no more: the nudge is allowlisted (counted nowhere else, not
// fatal, still auditable), the Why is surfaced into summary.yml on every run, and a genuinely
// different storage failure — or the SAME sentence in a different shape or severity tier — is
// still fatal.

const (
	// storeBloatAllowID is the entry under test.
	storeBloatAllowID = "podman-store-bloat-hygiene-nudge"

	// storeBloatExcerptPath is a VERBATIM three-line excerpt of the retained step log that
	// carried the nudge — .check/check-githubrunner-pod/2026.254.2125/image-build.log lines 6-8,
	// a real `charly box build` drive on a host whose store was 69% reclaimable. It is the ONLY
	// coupling between this repo's pattern and plugin-build's emitter: the emitter is a separate
	// module and a separate plugin candy, so no Go symbol can be shared, and only real bytes can
	// pin the sentence. If the emitter rewords it, this test fails and the entry must be
	// re-derived — which is exactly the fail-closed direction the entry's comment claims.
	storeBloatExcerptPath = "testdata/podman-store-bloat/image-build-excerpt.log"
)

// storeBloatNudgeExcerpt returns the retained excerpt verbatim.
func storeBloatNudgeExcerpt(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(storeBloatExcerptPath)
	if err != nil {
		t.Fatalf("read %s: %v — the allowlist entry's pattern is pinned to these bytes",
			storeBloatExcerptPath, err)
	}
	return string(b)
}

// TestStoreBloatNudgeIsAllowlistedNotFatal is the headline property: the line the emitter declares
// non-blocking must not be counted as a warning, must not fail its step, and must not disappear —
// it stays in the findings, carrying the allowlist ID a reader can look up.
func TestStoreBloatNudgeIsAllowlistedNotFatal(t *testing.T) {
	d := scanStepDiagnostics(storeBloatNudgeExcerpt(t))

	if d.Errors != 0 || d.Warnings != 0 || d.Allowlisted != 1 {
		t.Fatalf("the retained nudge must be allowlisted and counted nowhere else; got "+
			"errors=%d warnings=%d allowlisted=%d", d.Errors, d.Warnings, d.Allowlisted)
	}
	if len(d.Findings) != 1 {
		t.Fatalf("the excerpt must carry exactly one diagnostic line; got %d findings (%+v)",
			len(d.Findings), d.Findings)
	}
	if d.fails(defaultDiagnosticPolicy()) {
		t.Errorf("the nudge must not fail its step under the default policy; got %+v", d)
	}
	if msg := d.failure(defaultDiagnosticPolicy(), "image-build", "image-build.log"); msg != "" {
		t.Errorf("failure() must render no reason for an allowlisted nudge; got %q", msg)
	}

	var claimed []string
	for _, f := range d.Findings {
		if f.AllowID != "" {
			claimed = append(claimed, f.AllowID)
		}
	}
	if len(claimed) != 1 || claimed[0] != storeBloatAllowID {
		t.Errorf("AllowID on the finding = %v, want exactly [%s] — an exemption must stay auditable",
			claimed, storeBloatAllowID)
	}

	shapes := d.shapes()
	if len(shapes) != 1 || shapes[0].AllowID != storeBloatAllowID || shapes[0].Severity != severityWarning {
		t.Errorf("shapes() = %+v, want the one nudge shape, warning-tier, carrying the allowlist ID",
			shapes)
	}
}

// TestStoreBloatNudgeWhyIsSurfacedInSummary proves the exemption is re-read on every run, not
// reviewed once and inherited: the run rollup prints the entry's Why verbatim, and the per-step
// block names the entry instead of hiding the line.
func TestStoreBloatNudgeWhyIsSurfacedInSummary(t *testing.T) {
	d := scanStepDiagnostics(storeBloatNudgeExcerpt(t))

	var run strings.Builder
	writeRunDiagnostics(&run, d)
	rollup := run.String()
	for _, want := range []string{
		"- id: " + storeBloatAllowID,
		"suppressed: 1",
		// the product's own contract, quoted in the Why the reader audits
		"fail-soft by design",
		"never a build blocker",
		"opencharly/charly#173",
	} {
		if !strings.Contains(rollup, want) {
			t.Errorf("run rollup is missing %q:\n%s", want, rollup)
		}
	}

	var step strings.Builder
	writeStepDiagnostics(&step, "  ", d)
	if !strings.Contains(step.String(), "allowlisted: "+storeBloatAllowID) {
		t.Errorf("the per-step diagnostics block must name the entry rather than hide the line:\n%s",
			step.String())
	}
}

// TestStoreBloatNudgeSurvivesWarningTierPromotion is the reason the entry is an ALLOWLIST entry
// and not a policy change: the nudge is exempt in any tier, so promoting the warning tier (the
// one-field flip the scanner's header describes) cannot resurrect it — while an unclaimed warning
// still goes red under the very same policy.
func TestStoreBloatNudgeSurvivesWarningTierPromotion(t *testing.T) {
	promoted := diagnosticPolicy{ErrorsFatal: true, WarningsFatal: true}

	d := scanStepDiagnostics(storeBloatNudgeExcerpt(t))
	if d.fails(promoted) {
		t.Errorf("an allowlisted line is exempt in every tier; the promotion must not resurrect "+
			"the nudge; got %+v", d)
	}

	// The contrast, under the same policy: a captured warning shape no entry claims.
	unclaimed := scanStepDiagnostics("STEP 1/1: RUN pacman -Syu --noconfirm --needed some-package\n" +
		"warning: could not fully load metadata for package some-package-1.0-1\n")
	if unclaimed.Warnings != 1 || !unclaimed.fails(promoted) {
		t.Errorf("promotion must still red an unclaimed warning; got %+v", unclaimed)
	}
}

// TestStoreBloatAllowanceIsAnchoredAndTierScoped is the boundary: the entry claims the nudge
// sentence and nothing else. Every negative row here is a line whose wording or tier sits next to
// the nudge — a genuinely different storage failure, the same sentence at error severity, a
// truncated nudge, a re-emitted copy behind an emitter prefix, and a reworded nudge (the
// fail-closed case the entry's comment claims).
func TestStoreBloatAllowanceIsAnchoredAndTierScoped(t *testing.T) {
	const nudge = "warning: podman store is bloated (104.2GiB reclaimable, ~69% of 149.9GiB) — " +
		"the overlay-store corruption class tracked in opencharly/charly#173 tracks this factor. " +
		"Run `charly clean --deep` (pair with --invalidate for the fullest reclaim) before building."

	cases := []struct {
		name    string
		line    string
		claimed bool
		errs    int
		warns   int
	}{
		{
			name:    "the real nudge sentence is claimed",
			line:    nudge,
			claimed: true,
			errs:    0,
			warns:   0,
		},
		{
			// A storage failure the emitter would never print as a nudge: podman's own blob/layer
			// write error. It must stay fatal.
			name: "a genuinely different storage failure stays fatal",
			line: "error: writing blob: adding layer with blob " +
				"\"sha256:9f2c4b1a0d7e38f5c6a1b2d3e4f50718293a4b5c6d7e8f9012345678abcdef01\": " +
				"Error processing tar file(exit status 1): write /usr/lib/libx.so: no space left on device",
			claimed: false,
			errs:    1,
			warns:   0,
		},
		{
			// The logrus form the image builder emits when a commit fails.
			name: "a logrus storage failure stays fatal",
			line: "time=\"2026-09-12T09:41:00.000000000+02:00\" level=error " +
				"msg=\"Error committing the finished image: open /var/lib/containers/storage/overlay: " +
				"no space left on device\"",
			claimed: false,
			errs:    1,
			warns:   0,
		},
		{
			// TIER SCOPING: the identical sentence emitted at error severity is not claimed —
			// a warning-tier exemption can never absolve an error-tier finding.
			name:    "the same sentence at error severity is not claimed",
			line:    "error: " + strings.TrimPrefix(nudge, "warning: "),
			claimed: false,
			errs:    1,
			warns:   0,
		},
		{
			// ANCHORING: the pattern is the WHOLE sentence, so a truncated line still counts.
			name:    "a truncated nudge is not claimed",
			line:    "warning: podman store is bloated (104.2GiB reclaimable, ~69% of 149.9GiB)",
			claimed: false,
			errs:    0,
			warns:   1,
		},
		{
			// ANCHORING: the match is anchored at line start, so a re-emitted copy behind an
			// emitter prefix is a separate finding.
			name:    "a re-emitted copy behind an emitter prefix is not claimed",
			line:    "charly: " + nudge,
			claimed: false,
			errs:    0,
			warns:   1,
		},
		{
			// FAIL-CLOSED: reworded, the entry stops claiming — the nudge surfaces as a warning
			// rather than a stale pattern silently covering a changed message.
			name: "a reworded nudge is not claimed",
			line: "warning: podman store is bloated (104.2GiB reclaimable, ~69% of 149.9GiB) — " +
				"see opencharly/charly#173. Run `charly clean --deep` before building.",
			claimed: false,
			errs:    0,
			warns:   1,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := scanStepDiagnostics("STEP 1/2: RUN true\n" + c.line + "\nSTEP 2/2: RUN true\n")
			if d.Errors != c.errs || d.Warnings != c.warns {
				t.Errorf("errors=%d warnings=%d, want errors=%d warnings=%d (findings: %+v)",
					d.Errors, d.Warnings, c.errs, c.warns, d.Findings)
			}
			gotClaimed := false
			for _, f := range d.Findings {
				if f.AllowID == storeBloatAllowID {
					gotClaimed = true
				}
			}
			if gotClaimed != c.claimed {
				t.Errorf("claimed by %s = %t, want %t", storeBloatAllowID, gotClaimed, c.claimed)
			}
			if wantFatal := c.errs > 0; d.fails(defaultDiagnosticPolicy()) != wantFatal {
				t.Errorf("fails(defaultDiagnosticPolicy()) = %t, want %t",
					d.fails(defaultDiagnosticPolicy()), wantFatal)
			}
		})
	}
}

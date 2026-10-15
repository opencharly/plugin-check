package check

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// check_cmd_provenance_test.go — a verdict verb must ALWAYS name the artifact it judged, and a
// phase that judged NOTHING must say so instead of reporting a pass.
//
// Two contracts live in this file, both about the same path — the one where a plan resolution comes
// back EMPTY:
//
//  1. `charly check box` bailed on the no-plan path BEFORE printing its `Image:` line, so an image
//     carrying no baked plan produced no ref, no version, nothing — and exited 0. That mattered
//     directly: the mitigation this cutover told operators to use while the resolver was being
//     fixed was "read the Image: line before believing a verdict", and on exactly those images
//     there was no line to read. Reported twice independently, from two different cutovers.
//  2. Every arm returned nil on that path, i.e. exit 0, so a phase that asserted nothing was
//     indistinguishable from one that asserted everything and passed. In a bed it was banked as
//     `ok: true` (opencharly/plugin-check#110, measured on `distro-arch`'s `check-arch-vm` run
//     `2026.287.2125`, whose `check-live-charly-cachyos-tailscale-test` child PASSed on a log
//     whose entire content was "No plan steps defined for this image.").
//
// captureStderr runs fn with os.Stderr redirected and returns what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	os.Stderr = orig
	return <-done
}

// TestNoPlanStepsIsASkipNotAPass gates ALL FOUR arms, not the two that happened to be reported.
// The empty-resolution path exists in every mode that resolves a plan, and the sibling lesson in
// this file is why: "deleting the line from feature_box_gather.go broke no test at all, which is how
// a principle quietly becomes true of one caller and false of the other."
//
// The assertions are the two operator-visible facts, per arm: the arm still names what it judged
// (provenance + the no-plan sentence, unchanged), and it now exits as a SKIP — `mapCheckExitError`
// maps the returned error to `sdk.ExitCodeError{Code: 3}`, which is the fact a caller reads and the
// reason a bed can no longer bank the phase as a pass.
func TestNoPlanStepsIsASkipNotAPass(t *testing.T) {
	const ref = "ghcr.io/opencharly/fedora-nonfree:2026.227.0836"
	orig := hostCheckRun
	defer func() { hostCheckRun = orig }()

	arms := []struct {
		name string
		// run invokes the arm; the no-plan reply is installed by the caller.
		run func() error
		// wantProvenance is what the arm prints to name the artifact it judged; empty when the arm
		// never printed one (it names the target in the SKIPPED line instead).
		wantProvenance string
		// wantNoPlan is the arm's own no-plan sentence, preserved verbatim: the prose is what an
		// operator has been reading, and this change alters the OUTCOME, not the message.
		wantNoPlan string
		// wantSubject is what the SKIPPED line must name.
		wantSubject string
	}{
		{
			name:           "box",
			run:            func() error { return (&CheckBoxCmd{Image: "fedora-nonfree"}).Run() },
			wantProvenance: "Image: " + ref,
			wantNoPlan:     "No plan steps defined for this image.",
			wantSubject:    "this image",
		},
		{
			name:           "feature-box",
			run:            func() error { return (&CheckFeatureBoxCmd{Image: "fedora-nonfree"}).Run() },
			wantProvenance: "Image: " + ref,
			wantNoPlan:     "No plan steps baked into this image (author a plan: with check: steps).",
			wantSubject:    "this image",
		},
		{
			name:           "live",
			run:            func() error { return (&CheckLiveCmd{Box: "check-arch-vm.arch-host"}).Run() },
			wantProvenance: "", // the live arm had no provenance line to lose; the skip line names the ref
			wantNoPlan:     "No plan steps defined for this image.",
			wantSubject:    "deployment check-arch-vm.arch-host",
		},
		{
			name:           "feature-run",
			run:            func() error { return (&CheckFeatureRunCmd{Box: "check-arch-vm"}).Run() },
			wantProvenance: "",
			wantNoPlan:     "No plan steps baked into this deployment's image (author a plan: with check: steps).",
			wantSubject:    "this deployment's image",
		},
	}

	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			hostCheckRun = func(spec.CheckRunRequest) (kit.CheckRunReply, error) {
				return kit.CheckRunReply{Image: ref, NoSteps: true}, nil
			}
			var gotErr error
			out := captureStderr(t, func() { gotErr = arm.run() })

			if arm.wantProvenance != "" && !strings.Contains(out, arm.wantProvenance) {
				t.Fatalf("no-plan path printed no provenance:\n%s\nwant %q — a verdict with no artifact identity is unverifiable, and this is the path where it matters most", out, arm.wantProvenance)
			}
			if !strings.Contains(out, arm.wantNoPlan) {
				t.Fatalf("the arm's own no-plan message was lost:\n%s\nwant %q", out, arm.wantNoPlan)
			}
			if !strings.Contains(out, "SKIPPED — ") {
				t.Fatalf("an empty resolution was not announced as a skip:\n%s\nwant a `SKIPPED — …` line: nothing ran, so there is no verdict, and silence reads as a pass (opencharly/plugin-check#110)", out)
			}
			if !strings.Contains(out, arm.wantSubject) {
				t.Fatalf("the skip line does not name what resolved nothing:\n%s\nwant %q in it", out, arm.wantSubject)
			}
			if !strings.Contains(out, "plugin-check#110") {
				t.Fatalf("the skip line does not name the defect it belongs to:\n%s", out)
			}
			var skip *CheckSkippedError
			if !errors.As(gotErr, &skip) {
				t.Fatalf("an empty resolution must be a %T, got %#v — exit 0 here is the vacuous PASS the issue is about", skip, gotErr)
			}
			// The fact a caller actually reads: the process exit code. This is the assertion that
			// fails without the change (the arm returned nil, i.e. 0).
			var ec *sdk.ExitCodeError
			mapped := mapCheckExitError(gotErr)
			if !errors.As(mapped, &ec) || ec.Code != CheckSkippedExitCode {
				t.Fatalf("mapCheckExitError(%v) = %v, want sdk.ExitCodeError{Code: %d}: a skip that does not exit 3 is a skip no script can see", gotErr, mapped, CheckSkippedExitCode)
			}
		})
	}
}

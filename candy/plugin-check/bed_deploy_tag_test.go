package check

import (
	"strings"
	"testing"
)

// TestBedDeployCarriesTheRunTag is the gate for opencharly/plugin-check#115's bed-side half.
//
// `charly deploy add` with NO `--tag` resolves "the newest local CalVer" of the box, so a box with
// more than one local build can have its pod or VM run an artifact this run did not build — and the
// live verdict then certifies it. Measured on `check-crabbox-pod`: `check-image` (run-tagged ref)
// asserted the layer's `0.51.0` and passed, while the pod's `check-live` reported `0.48.1` and the
// older layer's baked plan. The pod path had been tagged BY HAND at one call site; the VM path had
// no tag at any of its three. Every bed deploy goes through bedAdd, so this is where the tag belongs
// (R2) — and this test is why the next call site cannot lose it.
func TestBedDeployCarriesTheRunTag(t *testing.T) {
	const tag = "2026.288.1207"

	// Every flavour a bed deploy comes in: the VM root, a VM's host-rooted child, the in-place local
	// arm, and the pod arm's positional form.
	argvs := map[string][]string{
		"vm root":     bedAdd([]string{"check-arch-vm", "arch"}, tag),
		"vm child":    bedAddChild("check-arch-vm", "arch-host", tag),
		"local child": bedAddChild("check-crabbox-e2e-pod", "child", tag),
		"pod arm":     append(bedAdd([]string{"check-crabbox-pod", "crabbox"}, tag), "--node-only"),
	}
	for name, argv := range argvs {
		joined := strings.Join(argv, " ")
		if !strings.Contains(joined, "--tag "+tag) {
			t.Errorf("%s: argv = %v, want it to carry `--tag %s` — a deploy that does not pin the run's tag resolves the newest local build of the box, which is how a bed certifies an artifact it did not build (opencharly/plugin-check#115)", name, argv, tag)
		}
		if argv[0] != "deploy" || argv[1] != "add" {
			t.Errorf("%s: argv = %v, want it to start with `deploy add`", name, argv)
		}
		if !strings.Contains(joined, "--dev-local-pkg") {
			t.Errorf("%s: argv = %v, lost --dev-local-pkg", name, argv)
		}
	}

	// And with no tag the flag must not be invented: the anchored/pre-run paths legitimately have no
	// CalVer to pin, and a `--tag ""` would be a second, different request to the resolver.
	if got := strings.Join(bedAdd([]string{"some-bed"}, ""), " "); strings.Contains(got, "--tag") {
		t.Errorf("bedAdd(\"\") = %q, want no --tag — an empty tag is not a pin", got)
	}
}

package check

import (
	"context"
	"slices"
	"testing"

	"github.com/opencharly/sdk/kit"
)

// TestResolveBedContainerConvertsRosterAddress pins the ONE conversion every lookup must apply.
//
// A bed's deploy CREATES its container under the DEPLOY KEY, not the roster address: bed_run.go's
// configStartArgs passes deployKeyForBed(name) to `charly config`/`start`, because a roster address like
// "openclaw.check-openclaw-pod" does not resolve from the project root. spec.ContainerName is faithful to
// the ref it is handed -- it maps "/" to "-" and does nothing else -- so the two spellings name two
// different containers:
//
//	charly config openclaw.check-openclaw-pod  ->  charly-openclaw.check-openclaw-pod   (roster address)
//	charly config openclaw/check-openclaw-pod  ->  charly-openclaw-check-openclaw-pod   (deploy key)
//
// A lookup handing the resolver the raw roster address therefore computes the first name while the deploy
// created the second, and reports a running container as "container ... is not running"
// (deploykit/container_resolve.go:40). Measured: openclaw.check-openclaw-pod's check-live step failed
// exactly that way while its `start` step reported ok: true.
//
// The name the running gate is ASKED about is captured from kit.ContainerRunning, so this test needs no
// container and no bed: it fails if a lookup does not convert, which is the whole property.
func TestResolveBedContainerConvertsRosterAddress(t *testing.T) {
	origRuntime, origRunning := kit.ResolveRuntime, kit.ContainerRunning
	t.Cleanup(func() { kit.ResolveRuntime, kit.ContainerRunning = origRuntime, origRunning })

	kit.ResolveRuntime = func() (*kit.ResolvedRuntime, error) {
		return &kit.ResolvedRuntime{RunEngine: "podman"}, nil
	}

	t.Run("a roster address resolves the container the deploy CREATED", func(t *testing.T) {
		var asked []string
		kit.ContainerRunning = func(engine, name string) bool { asked = append(asked, name); return true }

		engine, name, err := resolveBedContainer(context.Background(), "openclaw.check-openclaw-pod", "")
		if err != nil {
			t.Fatalf("resolveBedContainer(openclaw.check-openclaw-pod) = %v, want nil", err)
		}
		if engine != "podman" || name != "charly-openclaw-check-openclaw-pod" {
			t.Fatalf("resolved %s/%s, want podman/charly-openclaw-check-openclaw-pod — the name `charly config openclaw/check-openclaw-pod` creates", engine, name)
		}
		if len(asked) != 1 || asked[0] != "charly-openclaw-check-openclaw-pod" {
			t.Fatalf("the running gate was asked about %q, want [charly-openclaw-check-openclaw-pod]; charly-openclaw.check-openclaw-pod is the DOTTED name the deploy does NOT create", asked)
		}
	})

	t.Run("a plain name with no namespace is left alone", func(t *testing.T) {
		var asked []string
		kit.ContainerRunning = func(engine, name string) bool { asked = append(asked, name); return true }

		_, name, err := resolveBedContainer(context.Background(), "holder-bed", "")
		if err != nil {
			t.Fatalf("resolveBedContainer(holder-bed) = %v, want nil", err)
		}
		if name != "charly-holder-bed" {
			t.Fatalf("resolved %q, want charly-holder-bed — a name with no namespace has no roster address to convert", name)
		}
	})
}

// TestBedAddConvertsTheRosterAddress pins the third site of the same conversion.
//
// bedAdd has eight callers and every one hands it a bed's ROSTER address (`name`, or a nested child
// `name+"."+childKey`). `charly deploy add openclaw.check-openclaw-pod` does not resolve from the project root,
// so the deploy resolver derives a bare NAMESPACE as its requested box and FilterBox rejects it:
// `unknown box "openclaw"` (sdk/deploykit/graph.go:312), measured on this bed's deploy-add and filed as
// opencharly/plugin-build#28. Converting inside bedAdd is what makes the ninth caller impossible to miss.
func TestBedAddConvertsTheRosterAddress(t *testing.T) {
	t.Run("a roster address becomes the deploy key, flags follow it", func(t *testing.T) {
		got := bedAdd([]string{"openclaw.check-openclaw-pod"}, "")
		want := []string{"deploy", "add", "openclaw/check-openclaw-pod", "--dev-local-pkg"}
		if len(got) != len(want) {
			t.Fatalf("bedAdd = %q, want %q", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("bedAdd = %q, want %q", got, want)
			}
		}
	})

	t.Run("a namespaced nested child keeps its tail", func(t *testing.T) {
		got := bedAddChild("openclaw.check-openclaw-pod", "child", "")
		if got[2] != "openclaw/check-openclaw-pod.child" {
			t.Fatalf("bedAddChild child ref = %q, want openclaw/check-openclaw-pod.child", got[2])
		}
	})

	t.Run("a plain name with no namespace is left alone", func(t *testing.T) {
		if got := bedAdd([]string{"check-local"}, ""); got[2] != "check-local" {
			t.Fatalf("bedAdd(plain) = %q, want check-local unchanged", got[2])
		}
	})

	t.Run("a remote ref is NOT mangled", func(t *testing.T) {
		const remote = "@github.com/opencharly/charly/charly.yml"
		if got := bedAdd([]string{remote}, ""); got[2] != remote {
			t.Fatalf("bedAdd(remote) = %q, want it untouched -- spec.ResolveBoxName resolves remote refs by parsing", got[2])
		}
	})

	t.Run("a vm template argument is not mistaken for the ref", func(t *testing.T) {
		got := bedAdd([]string{"openclaw.check-openclaw-pod", "cachyos.vm"}, "")
		if got[2] != "openclaw/check-openclaw-pod" || got[3] != "cachyos.vm" {
			t.Fatalf("bedAdd(ref, vmTemplate) = %q, want the ref converted and the template untouched", got)
		}
	})
}

// TestBedAddChildConvertsTheRootBeforeAppendingTheChild pins the nested-child ref shape.
//
// A child reference is `<root>.<child>`. deployKeyForBed splits at the FIRST dot, which is the
// namespace boundary only for a NAMESPACED root. Passing the whole dotted address through bedAdd
// rewrites a non-namespaced root's child as `<root>/<child>` — a deploy key nothing creates — so
// the bed fails at `deploy add` before any check runs (plugin-check#127). The conversion must
// happen on the ROOT alone, and the child is appended to the resulting key.
func TestBedAddChildConvertsTheRootBeforeAppendingTheChild(t *testing.T) {
	const tag = "2026.295.1200"

	t.Run("a non-namespaced root keeps the dotted child path", func(t *testing.T) {
		got := bedAddChild("check-containerd-vm", "check-containerd-vm-member", tag)
		want := []string{
			"deploy", "add", "check-containerd-vm.check-containerd-vm-member",
			"--dev-local-pkg", "--tag", tag,
		}
		if !slices.Equal(got, want) {
			t.Fatalf("bedAddChild(non-namespaced root) = %q, want %q — `check-containerd-vm/check-containerd-vm-member` is a deploy key nothing creates", got, want)
		}
	})

	t.Run("a namespaced root still converts at the namespace boundary", func(t *testing.T) {
		got := bedAddChild("openclaw.check-openclaw-pod", "child", "")
		want := []string{
			"deploy", "add", "openclaw/check-openclaw-pod.child",
			"--dev-local-pkg",
		}
		if !slices.Equal(got, want) {
			t.Fatalf("bedAddChild(namespaced root) = %q, want %q", got, want)
		}
	})
}

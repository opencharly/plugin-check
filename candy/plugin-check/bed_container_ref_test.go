package check

import (
	"context"
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

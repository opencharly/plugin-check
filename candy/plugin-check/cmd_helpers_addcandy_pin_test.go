package check

// cmd_helpers_addcandy_pin_test.go — the unit half of opencharly/plugin-check#112: the resolved-
// project request must carry the deploy's OWN add_candy: refs, scoped to the deploy's box, so the
// coordinate a deploy pinned is a scan CANDIDATE instead of being absent from a resolution that only
// walked the project's image closure.
//
// The LIVE half is the disposable bed at the repo root, `check-addcandy-pin-honoured`, whose plan
// step drives the child `check live` phase of `testdata/addcandy-pin-fixture` — a project that pins
// layer-direnv at the NEWER of two coordinates it references. That bed passes on this tree and fails
// on the previous binary; it is the behavioural witness, not these tests.
//
// These tests decide the half no host has to be present for: WHICH refs are collected, and WHETHER
// they travel on the request. Both fail if the change is reverted — drop ExtraCandyRefs from
// resolvedProjectRequest and TestResolvedProjectRequestCarriesAddCandyRefs goes red; drop the scope
// or the recursion from scopedAddCandyRefs and TestScopedAddCandyRefs goes red.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

const (
	pinnedRef    = "@github.com/opencharly/layer-direnv:v2026.287.2155"
	transitiveRe = "@github.com/opencharly/layer-direnv:v2026.272.0057"
)

// TestResolvedProjectRequestCarriesAddCandyRefs asserts the refs reach the WIRE — the request the
// plugin marshals is what decides whether the host's reachability-scoped scan ever fetches the
// pinned coordinate.
func TestResolvedProjectRequestCarriesAddCandyRefs(t *testing.T) {
	extra := []spec.ExtraCandyRef{
		{Ref: pinnedRef, Scope: spec.BoxScope("check-arch-vm")},
		{Ref: transitiveRe},
	}
	raw, err := json.Marshal(resolvedProjectRequest("/some/project", extra))
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	got := string(raw)
	for _, want := range []string{
		`"dir":"/some/project"`,
		`"local_superproject":true`,
		`"extra_candy_refs":[{"ref":"` + pinnedRef + `","scope":"` + spec.BoxScope("check-arch-vm") + `"},{"ref":"` + transitiveRe + `"}]`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("request does not carry %s\n  got: %s", want, got)
		}
	}

	// Negative control: with no refs the field is absent, so the assertion above is about the refs
	// and not about a constant in the marshalled shape.
	plain, err := json.Marshal(resolvedProjectRequest("/some/project", nil))
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if strings.Contains(string(plain), "extra_candy_refs") {
		t.Fatalf("an unwidened request must not carry the field\n  got: %s", plain)
	}
}

// TestScopedAddCandyRefs pins the collection rule: the named node's own add_candy: refs, its
// in-substrate descendants' in authored order, every one tagged with the deploy's BOX scope — and a
// deploy-level sibling's NOT, because that sibling is its own composition (the host's
// deployNodePluginContext states the same boundary).
func TestScopedAddCandyRefs(t *testing.T) {
	nested := &spec.DeployNode{AddCandy: []spec.CandyRef{"@github.com/opencharly/layer-charly:" + "v2026.292.2352"}}
	alongside := &spec.DeployNode{AddCandy: []spec.CandyRef{"@github.com/opencharly/layer-ripgrep:v2026.235.1653"}}
	node := &spec.DeployNode{
		AddCandy: []spec.CandyRef{pinnedRef},
		Member: []spec.Member{
			{Name: "host", Position: spec.PositionInSubstrate, Node: nested},
			{Name: "sibling", Position: spec.PositionDeployLevel, Node: alongside},
		},
	}

	got := scopedAddCandyRefs(spec.BoxScope("check-arch-vm"), node)
	want := []spec.ExtraCandyRef{
		{Ref: pinnedRef, Scope: spec.BoxScope("check-arch-vm")},
		{Ref: "@github.com/opencharly/layer-charly:v2026.292.2352", Scope: spec.BoxScope("check-arch-vm")},
	}
	if len(got) != len(want) {
		t.Fatalf("collected %d ref(s), want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ref[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	if refs := scopedAddCandyRefs(spec.BoxScope("x"), nil); refs != nil {
		t.Fatalf("a nil node must collect nothing, got %+v", refs)
	}
	if refs := scopedAddCandyRefs(spec.BoxScope("x"), &spec.DeployNode{}); len(refs) != 0 {
		t.Fatalf("a node with no add_candy must collect nothing, got %+v", refs)
	}
}

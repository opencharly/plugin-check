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
// This test decides the half no host has to be present for: WHETHER the refs travel on the request.
// It fails if the change is reverted — drop ExtraCandyRefs from resolvedProjectRequest and it goes
// red, and the negative control pins that the assertion is about the refs and not about a constant
// in the marshalled shape.
//
// The COLLECTION half is not this package's to test any more: which refs a deploy contributes, at
// what scope, is `spec.DeployNode.ScopedAddCandyRefs`' one home (opencharly/spec#309/#310), tested
// there — including the mutation that drops the in-substrate recursion. Duplicating that assertion
// here would re-create, in a test, exactly the copy the extraction removed.

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

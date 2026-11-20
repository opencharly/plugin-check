package check

// walked_candy_vars_test.go — the WALKED candies' declared var: must reach the check env.
//
// This is the last leg of the producer chain that started at opencharly/plugin-pipeline#70.
// #Op's config: comment states the union a step's ${NAME} resolves from -- "candy var: ∪
// auto-exports" -- and `check:` steps never received the candy half: their env came from the
// resolver and nothing read the declarations. The resolved description now carries them
// (spec#413, sdk#603); this folds them in.

import (
	"testing"

	"github.com/opencharly/sdk/kit"
)

// TestWithCandyVarsFoldsTheWalkedDeclarations pins that every section's vars land.
func TestWithCandyVarsFoldsTheWalkedDeclarations(t *testing.T) {
	set := &kit.LabelDescriptionSet{
		Candy: []kit.LabeledDescription{
			{Origin: "candy:a", Vars: map[string]string{"FROM_A": "1"}},
			{Origin: "candy:b", Vars: map[string]string{"FROM_B": "2"}},
			{Origin: "candy:no-vars"},
		},
	}
	env := map[string]string{"AUTO": "exported"}

	got := withCandyVars(env, set)

	for name, want := range map[string]string{"AUTO": "exported", "FROM_A": "1", "FROM_B": "2"} {
		if v, ok := got[name]; !ok {
			t.Errorf("%s is not in the check env (%v) — a candy that declares a variable "+
				"must have it supplied to the step, not merely known", name, got)
		} else if v != want {
			t.Errorf("%s = %q, want %q", name, v, want)
		}
	}
	// The auto-export the resolver supplied must survive: this ADDS to the union.
	if _, ok := env["FROM_A"]; ok {
		t.Error("withCandyVars mutated the caller's map — the walk must copy on first fold")
	}
}

// TestWithCandyVarsLaterSectionWins pins the precedence WITHIN the set: candy, then box,
// then deploy, so the more specific section has the last word.
func TestWithCandyVarsLaterSectionWins(t *testing.T) {
	set := &kit.LabelDescriptionSet{
		Candy:  []kit.LabeledDescription{{Origin: "candy:a", Vars: map[string]string{"X": "candy"}}},
		Deploy: []kit.LabeledDescription{{Origin: "deploy:d", Vars: map[string]string{"X": "deploy"}}},
	}
	if got := withCandyVars(map[string]string{}, set)["X"]; got != "deploy" {
		t.Errorf("X = %q, want deploy — the deploy overlay is the more specific section", got)
	}
}

// TestWithCandyVarsEmptyIsANoOp pins that the common case allocates nothing and returns the
// input unchanged, so a bed whose candies declare no vars pays nothing.
func TestWithCandyVarsEmptyIsANoOp(t *testing.T) {
	env := map[string]string{"AUTO": "x"}
	for _, set := range []*kit.LabelDescriptionSet{
		nil,
		{},
		{Candy: []kit.LabeledDescription{{Origin: "candy:a"}}},
	} {
		got := withCandyVars(env, set)
		if len(got) != 1 || got["AUTO"] != "x" {
			t.Errorf("withCandyVars(%v) = %v, want the input unchanged", set, got)
		}
	}
}

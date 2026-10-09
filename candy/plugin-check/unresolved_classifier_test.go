package check

import (
	"context"
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// TestDeclaredVarNames pins the vocabulary's source and, at the same time, the distinction the
// classifier's two arms rest on: an envelope that EXISTS but declares nothing is an EMPTY set, while
// a nil envelope is "unknown" — and only the first may ever produce UnresolvedUnknown.
func TestDeclaredVarNames(t *testing.T) {
	rp := &spec.ResolvedProject{CandyModels: map[string]spec.CandyModel{
		"a": {
			Vars: map[string]string{"DECLARED": "1"},
			Env:  &spec.EnvConfig{Vars: map[string]string{"ENVDECL": "2"}},
		},
		"b": {Vars: map[string]string{"OTHER": "3"}},
	}}
	names := declaredVarNames(rp)
	for _, want := range []string{"DECLARED", "ENVDECL", "OTHER"} {
		if !names[want] {
			t.Errorf("%s missing from the declared vocabulary: %v", want, names)
		}
	}
	if names["NOT_DECLARED"] {
		t.Error("declaredVarNames invented a name nothing declares")
	}
	if got := declaredVarNames(&spec.ResolvedProject{}); got == nil {
		t.Error("a project that declares nothing must be an EMPTY set, not nil — nil means the host cannot answer")
	}
	if got := declaredVarNames(nil); got != nil {
		t.Errorf("a nil envelope must be nil (unknown), got %v", got)
	}
}

// TestInstallUnresolvedClassifier is the coverage that fails without the classifier: the arm this
// whole change exists for — a non-nil vocabulary answering UnresolvedUnknown for a name nothing
// declares — plus both conservative arms. `kit.Runner.ClassifyUnresolved` is the public surface the
// plan walk reaches, so this drives the real install path rather than a copy of the rule.
func TestInstallUnresolvedClassifier(t *testing.T) {
	kr := kit.NewRunner(kit.RunnerConfig{})
	op := &spec.Op{}

	installUnresolvedClassifier(kr, map[string]bool{"DECLARED": true})
	if got := kr.ClassifyUnresolved(op, "DECLARED"); got != kit.UnresolvedConditional {
		t.Errorf("a declared name = %v, want UnresolvedConditional (its absence here is a legitimate skip)", got)
	}
	if got := kr.ClassifyUnresolved(op, "NOT_DECLARED"); got != kit.UnresolvedUnknown {
		t.Errorf("a name nothing declares = %v, want UnresolvedUnknown (a dead assertion on an assert-only step)", got)
	}

	installUnresolvedClassifier(kr, map[string]bool{})
	if got := kr.ClassifyUnresolved(op, "NOT_DECLARED"); got != kit.UnresolvedUnknown {
		t.Errorf("an EMPTY vocabulary = %v, want UnresolvedUnknown — the project declares nothing", got)
	}

	installUnresolvedClassifier(kr, nil)
	if got := kr.ClassifyUnresolved(op, "NOT_DECLARED"); got != kit.UnresolvedConditional {
		t.Errorf("a nil (UNKNOWN) vocabulary = %v, want the conservative UnresolvedConditional", got)
	}
}

// TestDeclaredVocabularyForFallsBackConservatively pins the ONE place a check-run gather obtains the
// declared vocabulary (opencharly/plugin-check#95). The six gathers that used to pass nil must never
// invent an answer: an unresolvable project is a skip-class answer, never a failure built from a lookup
// error. A nil executor (no reverse channel) and an empty dir both mean "no project to resolve".
func TestVocabularyDirFallsBackToCwd(t *testing.T) {
	// The request's own dir wins when it has one.
	if got := vocabularyDir("/some/project"); got != "/some/project" {
		t.Errorf("a request Dir must win, got %q", got)
	}
	// An EMPTY dir is not a refusal: it falls back to the process working directory. This is the
	// whole difference between a working fix and an inert one — `charly check box` sends Mode:"box"
	// with no Dir at all, and the first version of this change asked for req.Dir and therefore still
	// skipped the box path. Asserted against the live cwd so the fallback is proven, not assumed.
	if got := vocabularyDir(""); got != projectDirFromCwd() {
		t.Errorf("an empty dir must fall back to the cwd (%q), got %q", projectDirFromCwd(), got)
	}
	if projectDirFromCwd() == "" {
		t.Fatal("the test cannot prove the fallback from an empty cwd")
	}
}

// TestDeclaredVocabularyForFallsBackConservatively pins that a nil executor yields nothing: with no
// reverse channel there is nothing to ask, so the answer stays the conservative skip rather than a
// failure invented from the absence of a lookup.
func TestDeclaredVocabularyForFallsBackConservatively(t *testing.T) {
	if got := declaredVocabularyFor(nil, context.Background(), "/some/project"); got != nil {
		t.Errorf("a nil executor must yield no vocabulary, got %v", got)
	}
}

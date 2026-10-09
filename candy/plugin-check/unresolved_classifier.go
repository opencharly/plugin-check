package check

import (
	"context"
	"os"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// unresolved_classifier.go — the HOST's answer to the one question the kit plan walk cannot answer
// for itself: can ANY mode or scope of this project supply this unresolved variable name?
//
// opencharly/charly#865. A `check:` step whose variable could not resolve SKIPPED, and a plan that
// had quietly stopped asserting read exactly like a green one. The walk could not do better: every
// unresolved name that was not a `${HOST:…}` cross-member reference landed in one skip arm, so
// three different situations were indistinguishable — a peer that is unreachable (a real failure),
// an input that genuinely does not apply to THIS run (a legitimate skip), and a name the author
// wrote that can never resolve in ANY run (a dead assertion reported as a pass).
//
// sdk/kit now asks the host, through kit.PlanContext.ClassifyUnresolved, because only the host knows
// its own variable vocabulary. This file is where the host answers, and the answer needs no new seam
// and no new wire field: the vocabulary rides the resolved-project envelope this plugin already
// computes (checkproject.go), because spec.CandyModel carries it directly as
//
//	Vars map[string]string   // the candy's declared `vars:`
//	Env  *EnvConfig          // whose Vars is the declared `env.vars:`
//
// The rule is deliberately one-sided: a name SOME candy in the project declares is treated as
// scope- or mode-dependent, so its absence from this step's expansion env stays a skip. Only a name
// nothing in the project declares is UnresolvedUnknown — and even then the walk fails it ONLY on an
// assert-only step, because a mutating step's scope may legitimately supply it somewhere else.

// declaredVarNames is every variable name the resolved project declares, across all its candies.
// It is a SET of names, never a per-candy lookup on purpose: a candy composes into deployments its
// own manifest cannot see, so a name declared by ANY candy may legitimately resolve in a deployment
// where that candy is present. Narrowing it per-candy would turn a legitimate skip into a false
// dead-assertion failure, which is the one error this classifier must not make.
//
// A nil envelope yields nil — "the host cannot answer" — which is NOT an empty set. An envelope that
// exists but declares nothing yields an EMPTY set, and the two are treated differently by
// installUnresolvedClassifier; conflating them would turn every gather that never loaded a project
// into one that fails steps over a vocabulary it never read.
// A NIL return means "this gather has no resolved project, so the host cannot answer at all" — it
// is NOT the same as an empty set, which means "the project declares nothing". The distinction is
// load-bearing: see installUnresolvedClassifier.
// projectDirFromCwd returns the process working directory — the project root an Invoke was
// dispatched in. Empty on error, which every caller reads as "no project to resolve".
func projectDirFromCwd() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

// vocabularyDir returns the directory the vocabulary should be resolved from: the request's own dir,
// or the process working directory when the request carries none. `charly check box` dispatches
// Mode:"box" with an Image and NOTHING else, so every gather it reaches arrives with an empty dir —
// without this fallback the classifier is inert on exactly the path it exists for (the R10 bed caught
// that: with req.Dir alone the box check still skipped).
func vocabularyDir(dir string) string {
	if dir != "" {
		return dir
	}
	return projectDirFromCwd()
}

// declaredVocabularyFor resolves the project at dir and returns its declared-variable vocabulary, or
// nil when it cannot be resolved. This is the ONE place a check-run gather obtains the vocabulary, so
// the six gathers that were left passing nil (opencharly/plugin-check#95) cannot each invent their own
// answer — and so a site that genuinely has no project keeps the conservative nil rather than a guess.
//
// Best-effort on purpose, matching every other resolution on these paths: an unresolvable project is a
// skip-class answer (UnresolvedConditional), never a failure invented from a lookup error.
func declaredVocabularyFor(ex *sdk.Executor, ctx context.Context, dir string) map[string]bool {
	if ex == nil {
		return nil
	}
	// The request need not carry a Dir: `charly check box` dispatches Mode:"box" with an Image and
	// NOTHING else, so every gather it reaches arrives with an empty dir. Falling back to the process
	// working directory is correct rather than convenient — the box check runs in the project that
	// dispatched it — and without it this whole change is inert on exactly the path it was written
	// for. The R10 bed caught that: with req.Dir alone the box check still skipped.
	dir = vocabularyDir(dir)
	if dir == "" {
		return nil
	}
	rp, err := resolvedProject(ex, ctx, dir)
	if err != nil || rp == nil {
		return nil
	}
	return declaredVarNames(rp)
}

func declaredVarNames(rp *spec.ResolvedProject) map[string]bool {
	if rp == nil {
		return nil
	}
	names := make(map[string]bool, len(rp.CandyModels))
	for _, m := range rp.CandyModels {
		for k := range m.Vars {
			names[k] = true
		}
		if m.Env != nil {
			for k := range m.Env.Vars {
				names[k] = true
			}
		}
	}
	return names
}

// installUnresolvedClassifier wires the host's answer onto the runner the plan walk drives. Split
// out from newPluginCheckRunner so every construction site is forced through it rather than each
// remembering to call it.
// A nil vocabulary — a gather that never loaded a resolved project — answers UnresolvedConditional
// for everything, i.e. the pre-charly#865 skip. That is deliberate and it is the only safe answer
// there: failing a step over a vocabulary the host never read would manufacture a failure out of
// missing knowledge (R4), and would break legitimate skips. A host that wants the new failure must
// load the project it is checking against, which is exactly what the bed and live gathers do.
func installUnresolvedClassifier(runner *kit.Runner, declared map[string]bool) {
	runner.SetUnresolvedClassifier(func(_ *spec.Op, name string) kit.UnresolvedClass {
		if declared == nil || declared[name] {
			return kit.UnresolvedConditional
		}
		return kit.UnresolvedUnknown
	})
}

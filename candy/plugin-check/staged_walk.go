package check

import (
	"context"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// staged_walk.go — the CC-3 consumer seam (Cutover C tasks 5-7): the live gather
// drives the new kit.RunPlanStaged walk whenever the bed's plan carries the
// `stage:` shared modifier or the deploy node carries `parallel: true`.
//
// The root live gather's LabelDescriptionSet ALREADY spans the whole bed: the
// load-time FlattenVenuesByPosition pass hoists every member's plan steps into the
// root node.Plan with the member's venue stamped onto Op.Venue (a bare name for a
// deploy-level member, a dotted path for an in-substrate member). So ONE live check
// on the bed root carries every member's steps — this file PARTITIONS that set by
// venue into per-member runs:
//
//   - each member gets its OWN independent kit.Runner (A4 — never share one Runner
//     across concurrent members), built with the member's own venue executor;
//   - the walk (kit.RunPlanStaged) groups steps by stage in first-seen order and
//     executes the stage groups in order; WITHIN a stage, steps on different members
//     run CONCURRENTLY (always, by construction); same-member steps keep authored
//     order. `parallel:` does not interact with stages (stages dominate).
//   - without stages, `parallel: true` runs whole-member plans concurrently;
//     without stages AND without parallel, today's sequential single-runner walk.
//
// Result collection + drain semantics live in the kit walk (per-member slices, one
// mutex for the shared-summary append, WaitGroup stage barriers); this file only
// supplies the member runs + the runner construction seam.

// hasStageSteps reports whether ANY plan step of the set carries the stage: modifier.
func hasStageSteps(set *kit.LabelDescriptionSet) bool {
	if set == nil {
		return false
	}
	for _, sec := range [][]kit.LabeledDescription{set.Candy, set.Box, set.Deploy} {
		for _, ld := range sec {
			for i := range ld.Plan {
				if ld.Plan[i].Stage != "" {
					return true
				}
			}
		}
	}
	return false
}

// bedParallel reports whether the deploy node (or its per-host overlay twin) carries
// `parallel: true` — the SUBSTRATE scalar covering the deploy's members.
func bedParallel(node, overlay *spec.DeployNode) bool {
	if node != nil && node.Parallel {
		return true
	}
	return overlay != nil && overlay.Parallel
}

// memberVenueKeys returns the DISTINCT venue keys of the set's Deploy steps in
// first-seen (authored) order, EXCLUDING the root venue (root steps stay on the
// caller's own runner). Venue-less steps (baked image steps, root steps with empty
// or root-named venue) are root steps and excluded.
func memberVenueKeys(root string, set *kit.LabelDescriptionSet) []string {
	var out []string
	seen := map[string]bool{}
	for _, ld := range set.Deploy {
		for i := range ld.Plan {
			v := ld.Plan[i].Venue
			if v == "" || v == root {
				continue
			}
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	return out
}

// partitionMemberSet builds the LabelDescriptionSet for ONE member venue: exactly
// the Deploy steps whose Op.Venue equals venue, in authored order. Candy/Box
// sections stay on the root run (baked image acceptance belongs to the root).
func partitionMemberSet(set *kit.LabelDescriptionSet, venue string) *kit.LabelDescriptionSet {
	out := &kit.LabelDescriptionSet{}
	for _, ld := range set.Deploy {
		steps := make([]spec.Step, 0, len(ld.Plan))
		for i := range ld.Plan {
			if ld.Plan[i].Venue == venue {
				steps = append(steps, ld.Plan[i])
			}
		}
		if len(steps) > 0 {
			out.Deploy = append(out.Deploy, kit.LabeledDescription{Origin: ld.Origin, Description: ld.Description, Plan: steps})
		}
	}
	return out
}

// memberPlanHasSteps reports whether the member set carries any steps.
func memberPlanHasSteps(m *kit.LabelDescriptionSet) bool {
	for _, sec := range [][]kit.LabeledDescription{m.Candy, m.Box, m.Deploy} {
		for _, ld := range sec {
			if len(ld.Plan) > 0 {
				return true
			}
		}
	}
	return false
}

// stagedCheckRunner is the per-venue runner factory the live arm supplies: it builds
// an INDEPENDENT kit.Runner for one member venue (own executor chain + own env).
type stagedCheckRunner func(venue string) kit.PlanContext

// runLiveStaged drives the staged/parallel walk for a whole-bed set. When the set has
// member venues (deploy-level or in-substrate members with steps), each member gets an
// own independent runner via the factory and kit.RunPlanStaged runs the members'
// plans grouped by stage / parallel. members is the caller's ROOT runner + set (the
// root's own steps + baked Candy/Box); parallel is the node's parallel: scalar.
// Returns the ordered per-step results.
func runLiveStaged(ctx context.Context, members []kit.MemberRun, parallel bool, strict bool) []kit.StepResult {
	if len(members) == 0 {
		return nil
	}
	return kit.RunPlanStaged(ctx, members, parallel, strict)
}

// buildMemberRuns constructs the kit.MemberRun list for ALL venues (root first, then
// member venues in first-seen order), using the caller's root runner for the root and
// the factory for each member venue. Returns nil when no member venues carry steps —
// the caller keeps the plain single-runner path (kit.RunPlan) unchanged.
func buildMemberRuns(ctx context.Context, ex *sdk.Executor, dir, rootName string, req spec.CheckRunRequest, set *kit.LabelDescriptionSet,
	rootRunner kit.PlanContext, factory stagedCheckRunner) []kit.MemberRun {

	venues := memberVenueKeys(rootName, set)
	if len(venues) == 0 {
		return nil
	}
	members := []kit.MemberRun{{Origin: rootName, Set: set, Runner: rootRunner}}
	for _, venue := range venues {
		m := partitionMemberSet(set, venue)
		if !memberPlanHasSteps(m) {
			continue
		}
		runner := factory(venue)
		if runner == nil {
			continue
		}
		members = append(members, kit.MemberRun{Origin: venue, Set: m, Runner: runner})
	}
	return members
}

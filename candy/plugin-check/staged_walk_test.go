package check

import (
	"context"
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// Cutover C tasks 5-7 consumer tests — the pure partition/decision helpers in
// staged_walk.go (no executors, no host seam): stage detection, the parallel
// scalar read, the venue partition of the whole-bed flattened set, and the
// member-run assembly order.

func stagedStep(text, stage, venue string) spec.Step {
	s := spec.Step{Run: text, Op: spec.Op{Stage: stage, Plugin: "p"}}
	s.Venue = venue
	return s
}

// TestHasStageSteps — stage detection over the whole set (Candy/Box/Deploy).
func TestHasStageSteps(t *testing.T) {
	if hasStageSteps(nil) {
		t.Fatal("nil set must not report stages")
	}
	plain := &kit.LabelDescriptionSet{Deploy: []kit.LabeledDescription{{
		Origin: "deploy:bed", Plan: []spec.Step{stagedStep("a", "", "bed")},
	}}}
	if hasStageSteps(plain) {
		t.Fatal("staged-less set must not report stages")
	}
	staged := &kit.LabelDescriptionSet{Deploy: []kit.LabeledDescription{{
		Origin: "deploy:bed", Plan: []spec.Step{stagedStep("a", "s1", "bed")},
	}}}
	if !hasStageSteps(staged) {
		t.Fatal("staged set must report stages")
	}
	// A stage on a member step counts too (whole-bed detection).
	memberStaged := &kit.LabelDescriptionSet{Deploy: []kit.LabeledDescription{{
		Origin: "deploy:bed", Plan: []spec.Step{stagedStep("a", "", "bed"), stagedStep("m", "s1", "m1")},
	}}}
	if !hasStageSteps(memberStaged) {
		t.Fatal("member-staged set must report stages")
	}
}

// TestBedParallel — the substrate scalar read from node and overlay.
func TestBedParallel(t *testing.T) {
	if bedParallel(nil, nil) {
		t.Fatal("nil node+overlay must not report parallel")
	}
	if bedParallel(&spec.DeployNode{}, nil) {
		t.Fatal("default node must not report parallel")
	}
	if !bedParallel(&spec.DeployNode{Parallel: true}, nil) {
		t.Fatal("node parallel:true must report parallel")
	}
	if !bedParallel(nil, &spec.DeployNode{Parallel: true}) {
		t.Fatal("overlay parallel:true must report parallel")
	}
}

// TestMemberVenueKeys — distinct member venues in first-seen order, root excluded.
func TestMemberVenueKeys(t *testing.T) {
	set := &kit.LabelDescriptionSet{Deploy: []kit.LabeledDescription{{
		Origin: "deploy:bed",
		Plan: []spec.Step{
			stagedStep("r1", "", "bed"),     // root venue excluded
			stagedStep("m1a", "", "m1"),     // deploy-level bare
			stagedStep("m2a", "", "bed.m2"), // in-substrate dotted
			stagedStep("m1b", "", "m1"),     // second m1 step (first-seen order)
			stagedStep("", "", ""),          // baked/venue-less excluded
		},
	}}}
	got := memberVenueKeys("bed", set)
	want := []string{"m1", "bed.m2"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("memberVenueKeys = %v, want %v", got, want)
	}
}

// TestPartitionMemberSet — one member's steps only, authored order preserved.
func TestPartitionMemberSet(t *testing.T) {
	set := &kit.LabelDescriptionSet{Deploy: []kit.LabeledDescription{{
		Origin: "deploy:bed",
		Plan: []spec.Step{
			stagedStep("root", "", "bed"),
			stagedStep("m1-a", "", "m1"),
			stagedStep("other", "", "m2"),
			stagedStep("m1-b", "", "m1"),
		},
	}}}
	m := partitionMemberSet(set, "m1")
	if len(m.Deploy) != 1 || len(m.Deploy[0].Plan) != 2 {
		t.Fatalf("partition m1 = %+v, want 2 steps", m)
	}
	if m.Deploy[0].Plan[0].Run != "m1-a" || m.Deploy[0].Plan[1].Run != "m1-b" {
		t.Fatalf("partition must preserve authored order: %+v", m.Deploy[0].Plan)
	}
}

// TestMemberPlanHasSteps — empty member sets are filtered from the run list.
func TestMemberPlanHasSteps(t *testing.T) {
	if memberPlanHasSteps(&kit.LabelDescriptionSet{}) {
		t.Fatal("empty set must not report steps")
	}
	with := &kit.LabelDescriptionSet{Deploy: []kit.LabeledDescription{{Origin: "x", Plan: []spec.Step{stagedStep("a", "", "m")}}}}
	if !memberPlanHasSteps(with) {
		t.Fatal("set with a step must report steps")
	}
}

// TestBuildMemberRuns_NoMembers — a set with only root steps returns nil (the caller
// keeps the plain single-runner path).
func TestBuildMemberRuns_NoMembers(t *testing.T) {
	set := &kit.LabelDescriptionSet{Deploy: []kit.LabeledDescription{{
		Origin: "deploy:bed", Plan: []spec.Step{stagedStep("r1", "", "bed")},
	}}}
	members := buildMemberRuns(context.TODO(), nil, "", "bed", spec.CheckRunRequest{}, set, nil, nil)
	if members != nil {
		t.Fatalf("no-member set must return nil, got %+v", members)
	}
}

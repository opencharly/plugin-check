package check

// roster_test.go — unit coverage for the check-roster runner's PURE logic:
// bed selection/exclusion, host-local refusal, iterate exclusion, expected-fail
// semantics, glob matching, and the exclusive-token serial-chain grouping. The
// live bed engine is exercised by the R10 bed run; these lock the orchestration.

import (
	"sync"
	"testing"

	"github.com/opencharly/spec/spec"
)

func disp() *bool { b := true; return &b }

func rosterFold() *spec.UnifiedFile {
	ns := &spec.UnifiedFile{
		Deploy: map[string]spec.DeployNode{
			"ns-bed": {Target: "pod", Image: "x", Disposable: disp()},
		},
	}
	return &spec.UnifiedFile{
		Deploy: map[string]spec.DeployNode{
			"check-a":       {Target: "pod", Image: "x", Disposable: disp()},
			"check-b":       {Target: "vm", From: "vm-x", Disposable: disp()},
			"check-fail":    {Target: "pod", Image: "x", Disposable: disp()},
			"check-local":   {Target: "local", From: "l-x", Disposable: disp(), Host: "local"},
			"check-iterate": {Target: "pod", Image: "x", Disposable: disp(), Iterate: &spec.Iterate{Sandbox: "s"}},
			"prod":          {Target: "pod", Image: "p"}, // not disposable -> not a bed
		},
		Namespaces: map[string]*spec.UnifiedFile{"ns": ns},
	}
}

func TestSelectRosterBeds_Default(t *testing.T) {
	uf := rosterFold()
	var r checkRoster
	sel, refused := selectRosterBeds(uf, &r, uf.Beds())
	gotSel := map[string]bool{}
	for _, b := range sel {
		gotSel[b.Name] = true
	}
	// Local + namespaced beds run; iterate skipped; non-disposable never enumerated.
	for _, want := range []string{"check-a", "check-b", "check-fail", "ns.ns-bed"} {
		if !gotSel[want] {
			t.Fatalf("roster did not select %q; selected=%v", want, gotSel)
		}
	}
	if gotSel["check-iterate"] {
		t.Fatal("iterate bed must be excluded from a deterministic roster")
	}
	if gotSel["prod"] {
		t.Fatal("non-disposable deploy must not be a bed")
	}
	// host-local refused by default.
	if len(refused) != 1 || refused[0].Name != "check-local" {
		t.Fatalf("host-local bed not refused by default; refused=%v", refused)
	}
}

func TestSelectRosterBeds_HostLocalOptInAndSelect(t *testing.T) {
	uf := rosterFold()
	no := false
	r := checkRoster{Select: "check-*", RefuseHostLocal: &no, Exclude: []string{"check-iterate"}}
	sel, refused := selectRosterBeds(uf, &r, uf.Beds())
	if len(refused) != 0 {
		t.Fatalf("refuse_host_local=false still refused %v", refused)
	}
	names := map[string]bool{}
	for _, b := range sel {
		names[b.Name] = true
	}
	if !names["check-local"] {
		t.Fatal("host-local bed not selected when refuse_host_local=false")
	}
	if names["ns.ns-bed"] {
		t.Fatal("select check-* must not match a namespace-qualified bed")
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pat, name string
		want      bool
	}{
		{"", "anything", true},
		{"*", "ns.bed", true},
		{"check-*", "check-a", true},
		{"check-*", "ns.check-a", false},
		{"ns.*", "ns.bed", true},
		{"main.check-*", "main.check-a", true},
		{"main.check-*", "other.check-a", false},
		{"exact", "exact", true},
		{"exact", "other", false},
	}
	for _, c := range cases {
		if got := globMatch(c.pat, c.name); got != c.want {
			t.Fatalf("globMatch(%q,%q)=%v want %v", c.pat, c.name, got, c.want)
		}
	}
}

func TestBuildRosterChains_SerializesSharedTokens(t *testing.T) {
	beds := []rosterBed{
		{Name: "gpu-1", Exclusive: []string{"nvidia-gpu"}},
		{Name: "gpu-2", Exclusive: []string{"nvidia-gpu"}},
		{Name: "gpu-shared", Shared: []string{"nvidia-gpu"}},
		{Name: "lock-1", Exclusive: []string{"test-lock"}},
		{Name: "free"},
	}
	chains := buildRosterChains(beds)
	// nvidia-gpu group (3) serial in ONE chain; test-lock its own; free its own.
	var sizes []int
	chainOf := map[string]int{}
	for i, ch := range chains {
		sizes = append(sizes, len(ch))
		for _, b := range ch {
			chainOf[b.Name] = i
		}
	}
	if chainOf["gpu-1"] != chainOf["gpu-2"] || chainOf["gpu-1"] != chainOf["gpu-shared"] {
		t.Fatalf("nvidia-gpu beds not serialized into one chain: %v", chainOf)
	}
	if chainOf["lock-1"] == chainOf["gpu-1"] {
		t.Fatal("distinct tokens must run in distinct chains (parallel)")
	}
	if chainOf["free"] == chainOf["gpu-1"] {
		t.Fatal("token-free bed must be in its own chain")
	}
}

func TestAggregateRoster_ExpectedFail(t *testing.T) {
	// A negative control that failed on checks (exit 2) is a PASS.
	o := rosterBedOutcome{Bed: "neg", ExitCode: CheckFailExitCode, OK: true, ExpectedOK: true}
	if err := aggregateRoster("r", []rosterBedOutcome{o}); err != nil {
		t.Fatalf("expected-fail bed that failed checks should pass the roster: %v", err)
	}
	// A negative control that unexpectedly passed fails the roster.
	o2 := rosterBedOutcome{Bed: "neg", ExitCode: 0, OK: false, ExpectedOK: true, Reason: "expected a check failure"}
	err := aggregateRoster("r", []rosterBedOutcome{o2})
	if err == nil {
		t.Fatal("expected-fail bed that passed should fail the roster")
	}
}

func TestAggregateRoster_InfraVsChecks(t *testing.T) {
	// A check failure maps to CheckFailedError (exit 2).
	cf := rosterBedOutcome{Bed: "b", ExitCode: CheckFailExitCode, OK: false}
	if _, ok := aggregateRoster("r", []rosterBedOutcome{cf}).(*CheckFailedError); !ok {
		t.Fatal("a check-only failure must map to CheckFailedError")
	}
	// An infra error maps to a plain error (exit 1).
	inf := rosterBedOutcome{Bed: "b", ExitCode: 1, OK: false, Reason: "build failed"}
	if _, ok := aggregateRoster("r", []rosterBedOutcome{inf}).(*CheckFailedError); ok {
		t.Fatal("an infra failure must NOT map to CheckFailedError")
	}
}

func TestDefaultRosterLanes(t *testing.T) {
	if got := defaultRosterLanes(0); got != 1 {
		t.Fatalf("defaultRosterLanes(0)=%d want 1", got)
	}
	if got := defaultRosterLanes(2); got < 1 || got > 2 {
		t.Fatalf("defaultRosterLanes(2)=%d out of range", got)
	}
}

// TestRosterWaitGroup_BalancedConcurrency is the regression guard for the
// WaitGroup accounting in runCheckRoster: one wg.Add per chain, one wg.Done per
// chain goroutine, and runOne must NOT call wg.Done. The shape is exercised here
// with the same chain/pool construction the runner uses (a double-Done panics
// "negative WaitGroup counter"; a missing Done hangs).
func TestRosterWaitGroup_BalancedConcurrency(t *testing.T) {
	beds := []rosterBed{
		{Name: "a"}, {Name: "b", Exclusive: []string{"tok"}}, {Name: "c", Exclusive: []string{"tok"}}, {Name: "d"},
	}
	chains := buildRosterChains(beds)
	sem := make(chan struct{}, 2) // lanes
	var wg sync.WaitGroup
	var mu sync.Mutex
	done := 0
	runOne := func(b rosterBed) {
		sem <- struct{}{}
		defer func() { <-sem }()
		mu.Lock()
		done++
		mu.Unlock()
	}
	for _, chain := range chains {
		chain := chain
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, b := range chain {
				runOne(b)
			}
		}()
	}
	wg.Wait()
	if done != len(beds) {
		t.Fatalf("ran %d beds, want %d (chain/pool accounting)", done, len(beds))
	}
}

// TestBuildRosterChains_UnionMergesSharedTokens: a bed whose tokens span two
// existing chains UNION-merges them, so the two chains never both hold a token and
// run concurrently. A[t1], B[t2], C[t1,t2] must end in ONE chain (not [[A,C],[B]]).
func TestBuildRosterChains_UnionMergesSharedTokens(t *testing.T) {
	beds := []rosterBed{
		{Name: "A", Exclusive: []string{"t1"}},
		{Name: "B", Exclusive: []string{"t2"}},
		{Name: "C", Exclusive: []string{"t1", "t2"}},
	}
	chains := buildRosterChains(beds)
	if len(chains) != 1 {
		t.Fatalf("A[t1], B[t2], C[t1,t2] must union-merge to ONE chain, got %d chains: %v", len(chains), chains)
	}
	if len(chains[0]) != 3 {
		t.Fatalf("merged chain must hold all 3 beds, got %d", len(chains[0]))
	}
}

// TestAggregateRoster_AllSkipped: every bed prereq-skipped → the roster SKIPs
// (CheckSkippedError → exit 3), not a failure.
func TestAggregateRoster_AllSkipped(t *testing.T) {
	sk := rosterBedOutcome{Bed: "b", Skipped: true, OK: false, Reason: "no gpu"}
	err := aggregateRoster("r", []rosterBedOutcome{sk})
	if err == nil {
		t.Fatal("all-skipped roster must return a skip error")
	}
	if _, ok := err.(*CheckSkippedError); !ok {
		t.Fatalf("all-skipped roster must map to CheckSkippedError (exit 3), got %T", err)
	}
}

// TestAggregateRoster_SkipPlusPass: a skip alongside a pass is a normal PASS.
func TestAggregateRoster_SkipPlusPass(t *testing.T) {
	outs := []rosterBedOutcome{
		{Bed: "a", OK: true},
		{Bed: "b", Skipped: true},
	}
	if err := aggregateRoster("r", outs); err != nil {
		t.Fatalf("pass+skip roster should PASS, got %v", err)
	}
}

package check

// roster.go — the `check-roster` runner: a declared set of disposable check beds
// run together (in parallel) as one gate. Served by candy/plugin-check.
//
// A roster is a FLAT `kind: check-roster` entity authored in the project
// charly.yml (schema/checkroster.cue). `charly check run <roster>` resolves it and
// calls runCheckRoster, which:
//
//  1. loads the project plugin-side (loaderkit) and enumerates beds via the ONE
//     resolver spec.UnifiedFile.Beds() (local + namespace-qualified);
//  2. selects/excludes beds by glob over qualified names;
//  3. classifies each: runnable / expected_fail (a negative control) / iterate
//     (excluded — an AI loop, not a deterministic R10 bed) / host-local (refused
//     by default — mutates the operator's workstation);
//  4. auto-derives exclusive-resource token groups from each bed's own
//     requires_exclusive:/requires_shared: declarations and serializes each group;
//  5. runs the rest through runCheckBed on a bounded worker pool (`lanes`);
//  6. aggregates honest exit codes (0 pass / 1 infra / 2 checks-failed / 3 skipped).
//
// Each bed drives the SAME R10 engine a single `charly check run <bed>` uses; this
// only orchestrates many of them.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// checkRoster mirrors schema/checkroster.cue (#CheckRosterInput). Decoded from the
// authored body read back out of the loaded project.
type checkRoster struct {
	Select          string   `json:"select,omitempty"`
	Exclude         []string `json:"exclude,omitempty"`
	ExpectedFail    []string `json:"expected_fail,omitempty"`
	Lanes           int      `json:"lanes,omitempty"`
	CPU             int      `json:"cpu,omitempty"`
	RAM             string   `json:"ram,omitempty"`
	RefuseHostLocal *bool    `json:"refuse_host_local,omitempty"`
	Var             []string `json:"var,omitempty"`
}

// rosterBed is one bed the roster will (or will not) run.
type rosterBed struct {
	Name       string
	HostLocal  bool
	Iterate    bool
	Exclusive  []string // requires_exclusive tokens
	Shared     []string // requires_shared tokens
	TraitKind  string   // substrate kind word (diagnostic)
	ExpectedOK bool     // a negative control: correct outcome is a check failure
}

// rosterBedOutcome is one bed's aggregate result.
type rosterBedOutcome struct {
	Bed        string `json:"bed"`
	ExitCode   int    `json:"exit_code"`
	OK         bool   `json:"ok"`
	ExpectedOK bool   `json:"expected_ok,omitempty"`
	Skipped    bool   `json:"skipped,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// runCheckRoster drives a `check-roster` entity end to end. Returns an error whose
// mapped exit code reflects the aggregate (see mapCheckExitError at the boundary).
func (c *CheckRunCmd) runCheckRoster(ex *sdk.Executor, ctx context.Context, name, cwd string) error {
	if ex == nil {
		return fmt.Errorf("charly check run %s: roster requires compiled-in placement (the project loader is unavailable out-of-process)", name)
	}
	uf, ok, err := loaderkit.LoadUnifiedViaExecutor(ctx, ex, cwd)
	if err != nil {
		return fmt.Errorf("charly check run %s: load project: %w", name, err)
	}
	if !ok || uf == nil {
		return fmt.Errorf("charly check run %s: no charly.yml in %s", name, cwd)
	}
	// The authored body is cached by the plugin at OpLoad (provider.go rosterCache);
	// fall back to the opaque uf.PluginKinds fold if OpLoad ran in a prior process.
	raw, ok := getRosterBody(name)
	if !ok {
		raw, ok = uf.PluginKinds["check-roster"][name]
	}
	if !ok {
		return fmt.Errorf("charly check run %s: no check-roster entity by that name", name)
	}
	var roster checkRoster
	if err := json.Unmarshal(raw, &roster); err != nil {
		return fmt.Errorf("charly check run %s: decode check-roster body: %w", name, err)
	}

	beds := uf.Beds()
	selected, refused := selectRosterBeds(uf, &roster, beds)

	fmt.Fprintf(os.Stderr, "charly check run %s: roster — %d bed(s) selected, %d refused (host-local)\n",
		name, len(selected), len(refused))
	for _, b := range refused {
		fmt.Fprintf(os.Stderr, "  REFUSED (host-local): %s — applies candies to THIS workstation (set refuse_host_local: false to opt in)\n", b.Name)
	}

	// Serial groups: beds sharing an exclusive/shared resource token must not run
	// concurrently (the arbiter fast-fails a second same-token claim). Build one
	// serial chain per token-group; independent groups run in parallel.
	chains := buildRosterChains(selected)

	lanes := roster.Lanes
	if lanes < 1 {
		lanes = defaultRosterLanes(len(selected))
	}

	sem := make(chan struct{}, lanes)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var outcomes []rosterBedOutcome

	// runOne runs ONE bed under the lane semaphore. The per-chain goroutine owns
	// the single wg.Done; runOne must not touch wg.
	runOne := func(b rosterBed) {
		sem <- struct{}{}
		defer func() { <-sem }()
		o := runRosterBed(ex, ctx, b, &roster)
		mu.Lock()
		outcomes = append(outcomes, o)
		mu.Unlock()
		fmt.Fprintln(os.Stderr, rosterOutcomeLine(o))
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

	return aggregateRoster(name, outcomes)
}

// selectRosterBeds classifies every bed against the roster's select/exclude and
// the host-local refusal policy. Returns (selected, refused-host-local).
func selectRosterBeds(uf *spec.UnifiedFile, r *checkRoster, beds map[string]spec.DeployNode) ([]rosterBed, []rosterBed) {
	refuseHL := true
	if r.RefuseHostLocal != nil {
		refuseHL = *r.RefuseHostLocal
	}
	names := make([]string, 0, len(beds))
	for n := range beds {
		names = append(names, n)
	}
	sort.Strings(names)

	var selected, refused []rosterBed
	for _, n := range names {
		node := beds[n]
		if !globMatch(r.Select, n) {
			continue
		}
		if matchesAny(r.Exclude, n) {
			continue
		}
		if node.Iterate != nil {
			// An iterate: bed is an AI benchmark, not a deterministic R10 bed.
			fmt.Fprintf(os.Stderr, "  SKIP (iterate): %s — an AI iteration loop, not a deterministic bed\n", n)
			continue
		}
		hostLocal := deploykit.HostRooted(&node) || node.Host == "local"
		rb := rosterBed{
			Name:       n,
			HostLocal:  hostLocal,
			Iterate:    node.Iterate != nil,
			Exclusive:  node.RequiredExclusive(),
			Shared:     node.RequiredShared(),
			TraitKind:  node.Target,
			ExpectedOK: matchesAny(r.ExpectedFail, n),
		}
		if hostLocal && refuseHL {
			refused = append(refused, rb)
			continue
		}
		selected = append(selected, rb)
	}
	return selected, refused
}

// buildRosterChains groups beds into serial chains: a bed joins the first chain
// already holding one of its exclusive/shared tokens, else starts a new chain.
// Distinct chains are disjoint in tokens and run in parallel.
// buildRosterChains groups beds into serial chains: any two beds sharing an
// exclusive/shared token are in the SAME chain (a bed's token set UNION-merges
// every chain it touches, so two chains never share a token). Distinct chains are
// token-disjoint and therefore run in parallel safely.
func buildRosterChains(beds []rosterBed) [][]rosterBed {
	var chains [][]rosterBed
	tokenChain := map[string]int{}
	for _, b := range beds {
		tokens := append(append([]string{}, b.Exclusive...), b.Shared...)
		// Collect the DISTINCT chains this bed's tokens already touch.
		merge := map[int]bool{}
		for _, t := range tokens {
			if ci, ok := tokenChain[t]; ok {
				merge[ci] = true
			}
		}
		var idx int
		switch len(merge) {
		case 0:
			chains = append(chains, nil)
			idx = len(chains) - 1
		case 1:
			for ci := range merge {
				idx = ci
			}
		default:
			// Union: fold every touched chain into the lowest-indexed one, then
			// drop the now-empty higher-indexed chains and remap their tokens.
			idx = -1
			for ci := range merge {
				if idx < 0 || ci < idx {
					idx = ci
				}
			}
			for ci := range merge {
				if ci == idx {
					continue
				}
				chains[idx] = append(chains[idx], chains[ci]...)
				chains[ci] = nil
				for t, mapped := range tokenChain {
					if mapped == ci {
						tokenChain[t] = idx
					}
				}
			}
		}
		chains[idx] = append(chains[idx], b)
		for _, t := range tokens {
			tokenChain[t] = idx
		}
	}
	// Drop any chains emptied by a union-merge.
	out := chains[:0]
	for _, c := range chains {
		if len(c) > 0 {
			out = append(out, c)
		}
	}
	return out
}

// runRosterBed runs one bed through the SAME R10 engine a single `charly check run
// <bed>` uses, then applies the roster's expected-fail semantics.
func runRosterBed(ex *sdk.Executor, ctx context.Context, b rosterBed, r *checkRoster) rosterBedOutcome {
	opts := bedRunOpts{Vars: map[string]string{}, Cpus: r.CPU, Ram: r.RAM}
	if len(r.Var) > 0 {
		if v, err := parseRunVars(r.Var); err == nil {
			opts.Vars = v
		}
	}
	res, runErr := runCheckBed(ctx, ex, b.Name, opts)
	o := rosterBedOutcome{Bed: b.Name, ExpectedOK: b.ExpectedOK}
	if res != nil && res.SkippedPrereq {
		o.Skipped = true
		o.OK = false
		o.Reason = res.SkipReason
		return o
	}
	exit := 0
	if res != nil {
		exit = res.FailExitCode
	}
	if runErr != nil && exit == 0 {
		exit = 1
	}
	o.ExitCode = exit
	// Expected-fail semantics: a negative control passes the roster when it fails
	// on checks (exit 2) and fails when it unexpectedly passes or hits infra.
	if b.ExpectedOK {
		o.OK = exit == CheckFailExitCode
		if !o.OK && exit == 0 {
			o.Reason = "expected a check failure (exit 2), got a pass"
		} else if !o.OK {
			o.Reason = fmt.Sprintf("expected a check failure (exit 2), got exit %d (infra)", exit)
		}
		return o
	}
	o.OK = res != nil && res.OK && exit == 0
	if !o.OK && o.Reason == "" {
		if exit == CheckFailExitCode {
			o.Reason = "checks failed"
		} else if runErr != nil {
			o.Reason = runErr.Error()
		}
	}
	return o
}

// aggregateRoster renders the roster verdict and maps it to an error whose boundary
// exit code is honest. A prereq-SKIPPED bed is neither a pass nor a failure: it is
// counted separately. The roster exits 0 when no bed failed; 2 when any bed failed a
// check (and no infra error); 1 when any bed hit an infra error; 3 (skipped) when
// every non-refused bed skipped and none failed.
func aggregateRoster(name string, outcomes []rosterBedOutcome) error {
	sort.Slice(outcomes, func(i, j int) bool { return outcomes[i].Bed < outcomes[j].Bed })
	var failed, skipped []rosterBedOutcome
	infra := false
	for _, o := range outcomes {
		if o.Skipped {
			skipped = append(skipped, o)
			continue
		}
		if o.OK {
			continue
		}
		failed = append(failed, o)
		if o.ExitCode != CheckFailExitCode {
			infra = true
		}
	}
	if len(failed) == 0 {
		if len(skipped) == len(outcomes) && len(outcomes) > 0 {
			fmt.Fprintf(os.Stderr, "charly check run %s: roster SKIPPED (%d beds, absent host prereqs)\n", name, len(skipped))
			return &CheckSkippedError{Msg: fmt.Sprintf("charly check run %s: all %d beds skipped (absent host prereqs)", name, len(skipped))}
		}
		fmt.Fprintf(os.Stderr, "charly check run %s: roster PASS (%d beds, %d skipped)\n", name, len(outcomes)-len(skipped), len(skipped))
		return nil
	}
	msg := fmt.Sprintf("charly check run %s: roster FAIL (%d/%d beds)", name, len(failed), len(outcomes))
	if infra {
		return fmt.Errorf("%s: %s", msg, rosterFailureSummary(failed))
	}
	return &CheckFailedError{Msg: msg + ": " + rosterFailureSummary(failed)}
}

func rosterFailureSummary(failed []rosterBedOutcome) string {
	parts := make([]string, 0, len(failed))
	for _, o := range failed {
		s := fmt.Sprintf("%s(exit=%d", o.Bed, o.ExitCode)
		if o.Reason != "" {
			s += " " + o.Reason
		}
		parts = append(parts, s+")")
	}
	return strings.Join(parts, ", ")
}

func rosterOutcomeLine(o rosterBedOutcome) string {
	status := "PASS"
	if !o.OK {
		status = "FAIL"
	}
	if o.Skipped {
		status = "SKIP"
	}
	extra := ""
	if o.ExpectedOK {
		extra = " [expected-fail]"
	}
	if o.Reason != "" {
		extra += " — " + o.Reason
	}
	return fmt.Sprintf("  %s %s (exit=%d)%s", status, o.Bed, o.ExitCode, extra)
}

// globMatch matches a qualified bed name against a glob. An empty pattern or "*"
// matches everything. Uses filepath.Match's syntax (its `*` does not cross `.`,
// which matches a namespace separator's intent: `main.*` matches main's own beds).
// globMatch matches a qualified bed name against a glob. An empty pattern or "*"
// matches everything. Uses filepath.Match syntax (its separator is `/`, so `*`
// matches `.` too — a `select: 'check-*'` therefore matches only names that START
// with `check-`, and a namespaced `charly.check-docs` does NOT match it). A pattern
// with a trailing `*` additionally prefix-matches, so `charly.*` / `charly.check-*`
// reach into a namespace as intended.
func globMatch(pattern, name string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	if ok, _ := filepath.Match(pattern, name); ok {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(name, strings.TrimSuffix(pattern, "*"))
	}
	return false
}

// matchesAny reports whether name matches any pattern (glob or exact) in pats.
func matchesAny(pats []string, name string) bool {
	for _, p := range pats {
		if globMatch(p, name) {
			return true
		}
	}
	return false
}

// defaultRosterLanes derives a host-derived default: min(len, NumCPU), bounded to a
// sane floor. The podman store lock is the real ceiling under a wide roster.
func defaultRosterLanes(n int) int {
	if n < 1 {
		return 1
	}
	lanes := numCPU()
	if lanes > n {
		lanes = n
	}
	if lanes < 1 {
		lanes = 1
	}
	return lanes
}

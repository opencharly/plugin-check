// schema/checkroster.cue — the SELF-CONTAINED CUE def validating the
// `check-roster` kind's authored body. A check-roster entity declares a set of
// disposable check beds to run together (in parallel) as one gate. Ships over
// Describe (schema_cue); references no base def so it compiles standalone.
//
// A roster selects beds by `select` (a name glob, namespace-qualified; default
// "*" = every disposable bed in every imported namespace) and runs them at
// `lanes` parallelism. Negative controls are declared `expected_fail` (a
// correct non-zero exit counts as pass). Arbitration groups are AUTO-DERIVED
// from each bed's own requires_exclusive:/requires_shared: declarations — no
// hand-maintained token list.
#CheckRosterInput: {
	// select — a glob matched against every disposable bed's QUALIFIED name
	// (bare for a local bed, `ns.name` for an imported namespace). Empty/"*"
	// selects every bed. Standard globs (`check-*`, `main.check-*`) are honored.
	select?: string
	// exclude — qualified bed names (or globs) never run, even when `select` matches
	// (e.g. a multi-hour `iterate:` bed that is not a deterministic R10 bed).
	exclude?: [...string]
	// expected_fail — qualified bed names whose CORRECT outcome is a check failure
	// (exit 2): the roster passes the gate when such a bed fails on checks, and
	// fails it when the bed unexpectedly passes or hits an infra error.
	expected_fail?: [...string]
	// lanes — max concurrent bed runs. 0/absent => a host-derived default.
	lanes?: int & >=0
	// cpu / ram — enforced on every VM bed the roster runs (threaded to the bed's
	// vm-create), independent of the bed's own declaration.
	cpu?: int & >=1
	ram?: string
	// refuse_host_local — refuse (do not run) beds on `host: local`, which mutate
	// the operator's workstation. Defaults to true; set false deliberately to opt in.
	refuse_host_local?: bool
	// var — per-run variable passthrough (key=value), forwarded to every bed.
	var?: [...string]
}

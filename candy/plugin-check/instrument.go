// instrument.go — the bed-runner instrument lifecycle (Cutover A, A-task-4): resolves
// `instrument:` entries per venue at bedSetup (venue-scoped ids <bed>.<member>.<id>), and at
// each run-phase bracket START dispatches `start` per entry whose phase includes the
// bracket — through the SAME verb-invoke seam plan steps use (kit.VerbResolver.RunVerb over
// the provider registry, placement-invisible) — then `stop` at bracket END and collects
// the evidence rows.
//
// The instrument entry is read generically from the bed-root FleetNode JSON (like the
// deleted record wrap did — a spec regen of the Go shape cannot break this seam): either
// the authored `<word>: <input>` sugar or the parse-desugared plugin/plugin_input pair,
// whichever the loader produced. The capture verb is a SESSION method on its own plugin
// (spice/record/... — A-task-2b); the runner never branches on capture kind: it builds the
// op (method session + the session envelope) and dispatches the word.

package check

import (
	"sync"

	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// instrumentPhase is a run-phase bracket an instrument's capture segment sits in. The
// closed word list mirrors #Phase in spec/schema/instrument.cue (build|live|update|
// teardown) — the runner enforces the enum at resolve time because the serialized node it
// reads may predate the schema's validation.
type instrumentPhase string

const (
	phaseBuild    instrumentPhase = "build"
	phaseLive     instrumentPhase = "live"
	phaseUpdate   instrumentPhase = "update"
	phaseTeardown instrumentPhase = "teardown"
	phaseDefault  instrumentPhase = "live" // #Instrument.phase default: *["live"]
)

func validInstrumentPhase(p string) bool {
	switch instrumentPhase(p) {
	case phaseBuild, phaseLive, phaseUpdate, phaseTeardown:
		return true
	}
	return false
}

// pipelineWord is ONE desugared post-run pipeline stage (the #PipelineWord pair: plugin +
// plugin_input; the authored `- transcode: {to: mp4}` sugar desugars here the same way the
// plan-step desugar does — the single non-modifier key is the verb word).
type pipelineWord struct {
	Plugin string         `yaml:"plugin"`
	Input  map[string]any `yaml:"plugin_input,omitempty"`
}

// instrumentEntry is ONE resolved instrument: the venue-scoped identity, its run-phase
// brackets, and the desugared capture-verb word + input.
type instrumentEntry struct {
	ID       string         // authored id (synthesized when absent)
	ScopedID string         // <venue>.<id> — the session identity + evidence key
	Venue    string         // the fleet-tree venue: <bed> or <bed>.<member>
	Phases   []string       // resolved brackets (default ["live"])
	Verb     string         // the capture verb word (any plugin word, never a runner enum)
	Input    map[string]any // the capture verb's input map (method session is added at dispatch)
	Pipeline []pipelineWord // post-run pipeline dispatches
}

// instrumentModifierKeys are the #Instrument fields that are NOT the capture-verb sugar key.
// The verb sugar is the ONE remaining key of an authored entry (the #Step desugar contract).
var instrumentModifierKeys = map[string]bool{
	"id": true, "phase": true, "pipeline": true, "timeout": true,
	"context": true, "plugin": true, "plugin_input": true,
}

// resolveInstruments parses the instrument: entries of the bed-root FleetNode JSON (the
// root substrate node + every member/child node) into venue-scoped entries. Deterministic:
// root entries first, then member nodes in sorted key order.
func resolveInstruments(nodeJSON []byte, bed string) ([]instrumentEntry, error) {
	var node map[string]any
	if err := yaml.Unmarshal(nodeJSON, &node); err != nil || node == nil {
		return nil, fmt.Errorf("resolve instruments: bed node JSON: %v", err)
	}
	var out []instrumentEntry
	add := func(venue string, raw any) error {
		entries, err := parseInstrumentList(venue, raw)
		if err != nil {
			return err
		}
		out = append(out, entries...)
		return nil
	}
	// The root venue: the deploy spec's own instrument: block, with the same "deploy"
	// envelope tolerance the deleted record wrap used (the node may carry its deploy spec
	// under a wrapper key).
	rootNode := node
	if dep, ok := node["deploy"].(map[string]any); ok {
		if _, has := dep["instrument"]; has {
			rootNode = dep
		}
	}
	if err := add(bed, rootNode["instrument"]); err != nil {
		return nil, err
	}
	// Member venues: the peer (brought-up-alongside) and nested (deploy-into) member maps of
	// the serialized FleetNode (spec.Deploy.Members/Children, wire keys "peer"/"nested").
	for _, mapKey := range []string{"peer", "nested"} {
		members, ok := node[mapKey].(map[string]any)
		if !ok {
			continue
		}
		keys := make([]string, 0, len(members))
		for k := range members {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			m, ok := members[k].(map[string]any)
			if !ok {
				continue
			}
			venue := bed + "." + k
			if err := add(venue, m["instrument"]); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// parseInstrumentList desugars ONE venue's instrument: list (raw any from the node JSON: a
// list, possibly nil) into resolved entries with venue-scoped ids.
func parseInstrumentList(venue string, raw any) ([]instrumentEntry, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("venue %s: instrument: must be a list", venue)
	}
	seen := map[string]bool{}
	var out []instrumentEntry
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("venue %s: instrument[%d]: must be a mapping", venue, i)
		}
		verb, input, err := instrumentVerb(m)
		if err != nil {
			return nil, fmt.Errorf("venue %s: instrument[%d]: %w", venue, i, err)
		}
		id, _ := m["id"].(string)
		if id == "" {
			id = fmt.Sprintf("%s-%d", verb, i+1)
		}
		phases := instrumentPhases(m)
		pipelines, err := parsePipelines(m["pipeline"])
		if err != nil {
			return nil, fmt.Errorf("venue %s: instrument %q: %w", venue, id, err)
		}
		scoped := venue + "." + id
		if seen[scoped] {
			return nil, fmt.Errorf("venue %s: duplicate instrument id %q (venue-scoped ids must be unique)", venue, id)
		}
		seen[scoped] = true
		out = append(out, instrumentEntry{
			ID: id, ScopedID: scoped, Venue: venue,
			Phases: phases, Verb: verb, Input: input, Pipeline: pipelines,
		})
	}
	return out, nil
}

// instrumentVerb extracts the desugared verb word + input from one entry: the
// plugin/plugin_input pair wins (a parse-desugared node); otherwise the single
// non-modifier key IS the verb word (authored sugar). Exactly-one-verb is the
// #Step/#Instrument discipline, enforced here because the serialized node may predate the
// parse desugar.
func instrumentVerb(m map[string]any) (string, map[string]any, error) {
	if p, ok := m["plugin"].(string); ok && p != "" {
		inp, _ := m["plugin_input"].(map[string]any)
		return p, inp, nil
	}
	var verb string
	for k := range m {
		if instrumentModifierKeys[k] {
			continue
		}
		if verb != "" {
			return "", nil, fmt.Errorf("exactly one capture verb per instrument (found %q and %q)", verb, k)
		}
		verb = k
	}
	if verb == "" {
		return "", nil, fmt.Errorf("no capture verb on the instrument (need one <word>: <input> key)")
	}
	inp, _ := m[verb].(map[string]any)
	return verb, inp, nil
}

// instrumentPhases resolves the phase brackets with the #Instrument default; unknown phase
// words fall back to the default bracket (the schema owns the hard validation).
func instrumentPhases(m map[string]any) []string {
	raw, ok := m["phase"].([]any)
	if !ok || len(raw) == 0 {
		return []string{string(phaseDefault)}
	}
	var out []string
	for _, p := range raw {
		if s, ok := p.(string); ok && validInstrumentPhase(s) {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return []string{string(phaseDefault)}
	}
	return out
}

// parsePipelines desugars one entry's pipeline: list — each item is a map with exactly one
// verb key (the sugar). nil/absent yields no pipelines.
func parsePipelines(raw any) ([]pipelineWord, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("pipeline: must be a list")
	}
	var out []pipelineWord
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("pipeline[%d]: must be a mapping", i)
		}
		var word string
		var input map[string]any
		for k, v := range m {
			if word != "" {
				return nil, fmt.Errorf("pipeline[%d]: exactly one verb per pipeline stage (found %q and %q)", i, word, k)
			}
			word = k
			input, _ = v.(map[string]any)
		}
		if word == "" {
			return nil, fmt.Errorf("pipeline[%d]: no verb", i)
		}
		out = append(out, pipelineWord{Plugin: word, Input: input})
	}
	return out, nil
}

// inPhase reports whether the entry participates in the given run-phase bracket.
func (e *instrumentEntry) inPhase(p instrumentPhase) bool {
	for _, want := range e.Phases {
		if instrumentPhase(want) == p {
			return true
		}
	}
	return false
}

// instrumentDispatcher is the verb-invoke seam an instrument dispatch rides: the SAME seam
// plan steps use (RunVerb over the provider registry). An interface so the bracket logic is
// unit-testable without the provider registry.
type instrumentDispatcher interface {
	dispatch(ctx context.Context, op *spec.Op) error
}

// venueInstrumentDispatcher is the real dispatcher: a per-venue kit.Runner whose Verbs()
// resolver reaches the provider registry (newPluginCheckRunner + the pluginVerbResolver) —
// placement-invisible, exactly what the plan walk dispatches through.
type venueInstrumentDispatcher struct {
	mu     sync.Mutex
	runner *kit.Runner
	build  func(context.Context) (*kit.Runner, error)
	built  bool
}

func (d *venueInstrumentDispatcher) dispatch(ctx context.Context, op *spec.Op) error {
	if !d.built {
		runner, err := d.build(ctx)
		if err != nil {
			return err
		}
		d.mu.Lock()
		d.runner = runner
		d.built = true
		d.mu.Unlock()
	}
	res, handled := d.runner.Verbs().RunVerb(ctx, op)
	if !handled {
		return fmt.Errorf("verb %q: not handled by the registry", op.Plugin)
	}
	if res.Status != spec.StatusPass {
		return fmt.Errorf("verb %q: %s", op.Plugin, res.Message)
	}
	return nil
}

// instrumentRuntime is the bed-runner-owned instrument state for ONE run: the resolved
// entries, the evidence collector, and the per-venue dispatchers. finalized guards the
// EVERY-terminal-path finalizer (the success tail calls it explicitly BEFORE summary.yml is
// written so the summary can reference the envelope; the deferred registration calls it
// again at return — run-once, so the evidence phase never double-dispatches).
type instrumentRuntime struct {
	bed           string
	logDir        string
	entries       []instrumentEntry
	collector     *evidenceCollector
	dispatchers   map[string]instrumentDispatcher // per venue (root = bed, members = <bed>.<key>)
	finalized     bool                            // the run-once guard for finalizeRunInstruments
	pipelinesDone bool                            // the run-once guard for the pipeline phase
}

// sessionEnvelope is the runner-provided identity a session-capable verb receives inside its
// input at start/stop: the venue-scoped id + the state dir (the shared evidence envelope;
// the provider never sees the transport).
type sessionEnvelope struct {
	ID       string `json:"id"`
	StateDir string `json:"state_dir"`
	LogDir   string `json:"log_dir"`
	// ArtifactDir is the run's generic evidence-artifact directory (verb-agnostic;
	// the provider appends its own filename/extension - plugin-check owns zero
	// capture-format knowledge). Injected on stop for session verbs that pull a
	// recording to the host (e.g. record's tmux -> .cast).
	ArtifactDir string `json:"artifact_dir"`
}

// buildInstrumentOp assembles the dispatch op for one instrument at one bracket end: the
// method-session input + the session envelope + the authored input fields.
func buildInstrumentOp(e *instrumentEntry, op string, env sessionEnvelope) *spec.Op {
	input := map[string]any{
		"method":     "session",
		"action":     op,
		"session_id": env.ID,
		"state_dir":  env.StateDir,
		"log_dir":      env.LogDir,
			"artifact_dir": env.ArtifactDir,
	}
	for k, v := range e.Input {
		input[k] = v
	}
	return &spec.Op{ID: e.ScopedID, Plugin: e.Verb, PluginInput: input}
}

// newInstrumentRuntime resolves the run's instruments and wires every venue's dispatcher.
// The dispatcher factory is injectable so bracket tests run without the registry; the
// default builds per-venue runners through the real seam.
func newInstrumentRuntime(ctx context.Context, ex *sdk.Executor, d *spec.CheckBedReply, name string) (*instrumentRuntime, error) {
	entries, err := resolveInstruments(d.NodeJSON, name)
	if err != nil {
		return nil, err
	}
	rt := &instrumentRuntime{
		bed:         name,
		logDir:      d.LogDir,
		entries:     entries,
		collector:   newEvidenceCollector(),
		dispatchers: map[string]instrumentDispatcher{},
	}
	if len(entries) == 0 {
		return rt, nil
	}
	for _, e := range entries {
		if _, ok := rt.dispatchers[e.Venue]; ok {
			continue
		}
		rt.dispatchers[e.Venue] = &venueInstrumentDispatcher{build: func(context.Context) (*kit.Runner, error) {
			return instrumentRunnerFor(ctx, ex, d, e.Venue, name)
		}}
	}
	return rt, nil
}

// instrumentRunnerFor builds one venue's dispatch runner. The ROOT venue's executor comes
// straight from the bed descriptor (a build-bracket instrument starts before the
// domain/container exists — no live resolution), and the ROOT CheckEnv is the live-snapshot
// shape the out-of-process verbs decode. MEMBER venues resolve through the SAME live
// resolver the check-live arms use (members.go liveTargetResolver, R3) and are only
// reachable once the member is up.
func instrumentRunnerFor(ctx context.Context, ex *sdk.Executor, d *spec.CheckBedReply, venue, rootName string) (*kit.Runner, error) {
	cfg := kit.RunnerConfig{
		Mode:       kit.ModeLive,
		Env:        map[string]string{"IMAGE": venue, "INSTANCE": ""},
		Box:        venue,
		Instance:   "",
		VerifyOnly: true,
	}
	if venue == rootName {
		var exec kit.Executor
		switch {
		case d.IsVM:
			exec = &kit.SSHExecutor{Host: kit.VmSshAlias(d.BedDomain), ConnectTimeout: 10}
		default:
			engine, containerName, cerr := deploykit.ResolveContainer(venue, "")
			if cerr != nil {
				return nil, cerr
			}
			exec = deploykit.ContainerChain(engine, containerName)
		}
		cfg.Exec = exec
		return newPluginCheckRunner(ex, ctx, spec.CheckEnv{
			Mode: "live", Box: venue, Venue: d.BedDomain, VenueKind: venueKindOf(d),
		}, cfg), nil
	}
	// Member venue: the live resolver (the members-up walk already proved it reachable).
	dir, _ := os.Getwd()
	resolve := liveTargetResolver(ex, ctx, dir, "")
	_, exec, err := resolve(venue)
	if err != nil {
		return nil, err
	}
	cfg.Exec = exec
	return newPluginCheckRunner(ex, ctx, spec.CheckEnv{
		Mode: "live", Box: venue, Venue: venue, VenueKind: "member",
	}, cfg), nil
}

// venueKindOf returns the root venue kind word for the CheckEnv snapshot.
func venueKindOf(d *spec.CheckBedReply) string {
	switch {
	case d.IsVM:
		return "vm"
	case d.IsLocal:
		return "local"
	case d.IsExternal:
		return "external"
	case d.IsGroup:
		return "group"
	}
	return "pod"
}

// runInstrumentBracket dispatches one bracket end across every entry that participates:
// op = "start" | "stop". A failed instrument is a failed bed (the capture contract is part
// of the run, R7). At a stop, the segment is collected into the evidence envelope.
func (rt *instrumentRuntime) runInstrumentBracket(ctx context.Context, p instrumentPhase, op string) error {
	for i := range rt.entries {
		e := &rt.entries[i]
		if !e.inPhase(p) {
			continue
		}
		disp, ok := rt.dispatchers[e.Venue]
		if !ok {
			return fmt.Errorf("instrument %s: no dispatcher for venue %s", e.ScopedID, e.Venue)
		}
		env := sessionEnvelope{ID: e.ScopedID, StateDir: rt.stateDir(e.ScopedID), LogDir: rt.logDir, ArtifactDir: filepath.Join(rt.logDir, "media")}
		if derr := disp.dispatch(ctx, buildInstrumentOp(e, op, env)); derr != nil {
			return fmt.Errorf("instrument %s %s: %w", e.ScopedID, op, derr)
		}
		if op == "stop" {
			rt.collectSegment(ctx, e, p)
		}
	}
	return nil
}

// stateDir returns the capture state dir for one venue-scoped session id.
func (rt *instrumentRuntime) stateDir(scopedID string) string {
	return sessionStateDir(rt.logDir, scopedID)
}

// collectSegment reads the session handle + the provider's evidence row (state dir) after a
// bracket stop and appends the segment to the instrument's evidence row.
func (rt *instrumentRuntime) collectSegment(ctx context.Context, e *instrumentEntry, p instrumentPhase) {
	stateDir := rt.stateDir(e.ScopedID)
	h, herr := SessionHandleFromDisk(stateDir)
	if herr != nil || h == nil {
		// No handle on record: the start fell through without a spawn (a venue-side
		// transport like record's tmux never touches the service) — the run records the
		// bracket window it observed and whatever the provider's row carries (frames,
		// bytes, artifacts — blind).
		row := readProviderRow(stateDir)
		seg := map[string]any{"start": "", "stop": "", "phase": string(p)}
		if segs, ok := rowSegment(row); ok {
			for _, k := range []string{"frames", "bytes"} {
				if v, has := segs[k]; has {
					seg[k] = v
				}
			}
		}
		rt.collector.addSegment(e, p, "", "", seg, providerArtifacts(row))
		return
	}
	row := readProviderRow(stateDir)
	seg := map[string]any{"start": h.StartedAt, "stop": h.StoppedAt, "phase": string(p)}
	if segs, ok := rowSegment(row); ok {
		for _, k := range []string{"frames", "bytes"} {
			if v, has := segs[k]; has {
				seg[k] = v
			}
		}
	}
	rt.collector.addSegment(e, p, h.StartedAt, h.StoppedAt, seg, providerArtifacts(row))
}

// rowSegment extracts the first segment element of a provider row (blind: only the
// frames/bytes scalar keys are copied, never capture-kind content).
func rowSegment(row map[string]any) (map[string]any, bool) {
	if row == nil {
		return nil, false
	}
	segs, ok := row["segment"].([]any)
	if !ok || len(segs) == 0 {
		return nil, false
	}
	sm, ok := segs[0].(map[string]any)
	return sm, ok
}

// providerArtifacts extracts the artifact list of a provider row (blind: paths + open kind
// words travel verbatim).
func providerArtifacts(row map[string]any) []any {
	if row == nil {
		return nil
	}
	arts, _ := row["artifact"].([]any)
	return arts
}

// readProviderRow loads the provider-written evidence row (row.json) from a session's state
// dir, if present — a bounded read (a provider row must not balloon the run state).
func readProviderRow(stateDir string) map[string]any {
	path := filepath.Join(stateDir, "row.json")
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() > 1<<20 {
		return nil
	}
	b := make([]byte, st.Size())
	if _, err := io.ReadFull(f, b); err != nil {
		return nil
	}
	var row map[string]any
	if err := json.Unmarshal(b, &row); err != nil {
		return nil
	}
	return row
}

// evidence.go — the check-run evidence envelope (Cutover A, A-task-4): ONE machine-written
// manifest (.check/<bed>/<calver>/evidence.yml) of #EvidenceRow rows — venue-keyed and
// sorted — beside summary.yml, referenced from it. The runner owns the envelope shape
// (spec/schema/instrument.cue #EvidenceRow) and ZERO capture-format knowledge: no
// mjpeg/gif/mp4 string appears here; artifact kinds and pipeline words travel as the OPEN
// words the model defines.
//
// Pipelines execute in the evidence phase BEFORE venue teardown (so in-venue exec words
// still resolve) by BLIND word dispatch through the registry: the runner injects the
// instrument primary artifact path into the word input (the #TranscodeInput contract) and
// records the executed word on the row.

package check

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/opencharly/spec/spec"
)

// evidenceRow mirrors spec/schema/instrument.cue #EvidenceRow (venue-keyed, emitted sorted).
// Segment elements are the flexible machine-written opens; the runner fills the run-known
// envelope fields and copies provider content blind.
type evidenceRow struct {
	Instrument string           `yaml:"instrument"`
	Origin     string           `yaml:"origin"`
	Verb       string           `yaml:"verb"`
	Venue      string           `yaml:"venue"`
	Phase      string           `yaml:"phase,omitempty"`
	Segment    []map[string]any `yaml:"segment,omitempty"`
	Artifact   []any            `yaml:"artifact,omitempty"`
	Pipeline   []pipelineWord   `yaml:"pipeline,omitempty"`
}

// evidenceCollector accumulates the run evidence rows keyed by the venue-scoped instrument
// id — ONE row per instrument, segments appended per phase bracket.
type evidenceCollector struct {
	rows map[string]*evidenceRow
}

func newEvidenceCollector() *evidenceCollector {
	return &evidenceCollector{rows: map[string]*evidenceRow{}}
}

// addSegment appends one capture segment to the entry row. start/stop are the session
// handle span; seg carries the machine-run facts (start/stop/phase + blind frames/bytes);
// artifacts ride the provider row verbatim. A missing handle (a venue-side transport) is
// still an honest segment: the bracket window the run observed.
func (c *evidenceCollector) addSegment(e *instrumentEntry, p instrumentPhase, start, stop string, seg map[string]any, artifacts []any) {
	row, ok := c.rows[e.ScopedID]
	if !ok {
		row = &evidenceRow{Instrument: e.ScopedID, Origin: "session", Verb: e.Verb, Venue: e.Venue}
		c.rows[e.ScopedID] = row
	}
	if row.Phase == "" {
		row.Phase = string(p)
	}
	if seg == nil {
		seg = map[string]any{"start": start, "stop": stop, "phase": string(p)}
	}
	row.Segment = append(row.Segment, seg)
	if len(artifacts) > 0 {
		row.Artifact = append(row.Artifact, artifacts...)
	}
}

// addPipeline records one executed pipeline word on the entry row (the evidence phase).
func (c *evidenceCollector) addPipeline(scopedID string, w pipelineWord) {
	if row, ok := c.rows[scopedID]; ok {
		row.Pipeline = append(row.Pipeline, w)
	}
}

// sorted returns the rows in deterministic venue-keyed order: (venue, instrument).
func (c *evidenceCollector) sorted() []*evidenceRow {
	out := make([]*evidenceRow, 0, len(c.rows))
	for _, r := range c.rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Venue != out[j].Venue {
			return out[i].Venue < out[j].Venue
		}
		return out[i].Instrument < out[j].Instrument
	})
	return out
}

// writeEvidence writes evidence.yml next to summary.yml — deterministic: the same rows
// produce byte-identical output in venue-keyed order.
func writeEvidence(dir string, rows []*evidenceRow) error {
	var b strings.Builder
	b.WriteString("evidence:\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "  - instrument: %s\n", yamlScalar(r.Instrument))
		fmt.Fprintf(&b, "    origin: %s\n", yamlScalar(r.Origin))
		fmt.Fprintf(&b, "    verb: %s\n", yamlScalar(r.Verb))
		fmt.Fprintf(&b, "    venue: %s\n", yamlScalar(r.Venue))
		if r.Phase != "" {
			fmt.Fprintf(&b, "    phase: %s\n", yamlScalar(r.Phase))
		}
		if len(r.Segment) > 0 {
			b.WriteString("    segment:\n")
			for _, s := range r.Segment {
				b.WriteString("      - ")
				b.WriteString(flowSequence(s))
				b.WriteString("\n")
			}
		}
		if len(r.Artifact) > 0 {
			b.WriteString("    artifact:\n")
			for _, a := range r.Artifact {
				b.WriteString("      - ")
				b.WriteString(flowSequence(asMap(a)))
				b.WriteString("\n")
			}
		}
		if len(r.Pipeline) > 0 {
			b.WriteString("    pipeline:\n")
			for _, pw := range r.Pipeline {
				b.WriteString("      - plugin: " + yamlScalar(pw.Plugin))
				if len(pw.Input) > 0 {
					b.WriteString("\n        plugin_input: ")
					b.WriteString(flowMap(pw.Input))
				}
				b.WriteString("\n")
			}
		}
	}
	path := filepath.Join(dir, "evidence.yml")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// yamlScalar renders a scalar with the minimal quoting YAML requires — deterministic.
// yamlScalarQuoteChars are the flow-YAML hazard characters (the Go backtick and the
// apostrophe are deliberately excluded — instrument ids, venue names and pipeline words
// never carry them, and the writer stays deterministic).
var yamlScalarQuoteChars = ":#{}[],&*!|>%@\n\t "

func yamlScalar(s string) string {
	if s == "" {
		return `""`
	}
	needQuote := strings.ContainsAny(s, yamlScalarQuoteChars) || s == "~" || s == "null" || s == "true" || s == "false"
	if needQuote {
		return strconv.Quote(s)
	}
	return s
}

// flowMap renders a small map as a single-line JSON-flow YAML scalar (deterministic: the
// map is sorted by key before rendering). Provider row content is re-emitted blind.
func flowMap(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, yamlScalar(k)+": "+jsonScalar(m[k]))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// flowSequence renders a segment/artifact element as one flow line. Maps render via
// flowMap; scalars verbatim.
func flowSequence(v any) string {
	if m, ok := v.(map[string]any); ok {
		return flowMap(m)
	}
	return jsonScalar(v)
}

// jsonScalar renders a scalar value deterministically (an int stays an int, a string stays
// a quoted string; a map renders via flowMap).
func jsonScalar(v any) string {
	if m, ok := v.(map[string]any); ok {
		return flowMap(m)
	}
	if b, err := json.Marshal(v); err == nil {
		return string(b)
	}
	return fmt.Sprintf("%v", v)
}

// asMap narrows an element to a map when possible (nil otherwise — never a crash path).
func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

// runInstrumentEvidencePhase executes the evidence phase: every pipeline word of every
// instrument dispatches BLIND through the registry (the same seam — the runner per-venue
// dispatchers), with the instrument primary artifact path injected into the input (the
// #TranscodeInput contract: the word provider picks the kind it understands). The executed
// word rides the evidence row.
func (rt *instrumentRuntime) runInstrumentEvidencePhase(ctx context.Context) error {
	if rt == nil || rt.pipelinesDone {
		return nil
	}
	rt.pipelinesDone = true
	var firstErr error
	// Pipelines run before the venue teardown (the plan's binding): an entry whose capture
	// only completes DURING the teardown bracket (phase teardown) has no segments yet here —
	// its pipeline dispatches at the every-terminal-path finalizer instead.
	for i := range rt.entries {
		e := &rt.entries[i]
		if len(e.Pipeline) == 0 {
			continue
		}
		if rt.collector.rows[e.ScopedID] == nil {
			// No capture completed yet (a teardown-bracket instrument): the finalizer
			// dispatches its pipeline once its segment lands.
			continue
		}
		disp, ok := rt.dispatchers[e.Venue]
		if !ok {
			if firstErr == nil {
				firstErr = fmt.Errorf("instrument %s: pipeline: no dispatcher for venue %s", e.ScopedID, e.Venue)
			}
			continue
		}
		for _, pw := range e.Pipeline {
			input := map[string]any{}
			for k, v := range pw.Input {
				input[k] = v
			}
			if art := rt.primaryArtifact(e.ScopedID); art != "" {
				// The entry's primary artifact is the pipeline's SOURCE: thread it
				// under both the generic artifact key (the shared validators'
				// contract) and the explicit source_artifact key (the transcode
				// verb's source contract — the provider reads the input first, the
				// check env second; the env is fixed at runner construction, so
				// the input is the only channel that can carry a per-entry path).
				if _, has := input["artifact"]; !has {
					input["artifact"] = art
				}
				if _, has := input["source_artifact"]; !has {
					input["source_artifact"] = art
				}
			}
			op := &spec.Op{ID: e.ScopedID + ".pipeline." + pw.Plugin, Plugin: pw.Plugin, PluginInput: input}
			if derr := disp.dispatch(ctx, op); derr != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("instrument %s pipeline %s: %w", e.ScopedID, pw.Plugin, derr)
				}
				continue
			}
			rt.collector.addPipeline(e.ScopedID, pw)
		}
	}
	return firstErr
}

// primaryArtifact returns the FIRST artifact path of the entry evidence rows — the
// deterministic pipeline input (venue-sorted row order). Provider-authored artifact paths
// travel verbatim; a missing path yields "".
func (rt *instrumentRuntime) primaryArtifact(scopedID string) string {
	row := rt.collector.rows[scopedID]
	if row == nil {
		return ""
	}
	for _, a := range row.Artifact {
		if m := asMap(a); m != nil {
			if p, ok := m["path"].(string); ok {
				return p
			}
		}
	}
	return ""
}

// writeRunEvidence writes the run evidence.yml (a no-op when no instrument produced rows).
func (rt *instrumentRuntime) writeRunEvidence() error {
	rows := rt.collector.sorted()
	if len(rows) == 0 {
		return nil
	}
	if err := writeEvidence(rt.logDir, rows); err != nil {
		return fmt.Errorf("writing %s: %w", filepath.Join(rt.logDir, "evidence.yml"), err)
	}
	return nil
}

// finalizeRunInstruments is the teardown-owner enforcement: stops every live capture
// session, runs the evidence phase (pipelines) and writes the envelope. Registered on EVERY
// terminal path of runCheckBed (the fail tail + the success tail); idempotent and
// best-effort — instrument failure must never mask the bed own verdict.
func (rt *instrumentRuntime) finalizeRunInstruments(ctx context.Context) {
	if rt == nil || rt.finalized {
		return
	}
	rt.finalized = true
	finalizeSessionDir(ctx, rt.logDir)
	// The evidence phase here covers the FAIL paths (the success path ran its pipelines
	// before venue teardown): teardown-bracket pipelines — whose captures complete only at
	// the cleanup tail — dispatch here, exactly once (the pipelinesDone guard).
	_ = rt.runInstrumentEvidencePhase(ctx)
	_ = rt.writeRunEvidence()
}

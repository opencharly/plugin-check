package check

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/opencharly/spec/spec"
)

// instrument_test.go — the deterministic lifecycle core: entry resolution (venue-scoped
// ids, verb sugar, phase defaults, members), the dispatch op contract (method session +
// the session envelope, provider-invisible transport), and the bracket start/stop flow
// against a recording fake dispatcher.

func resolvedNodeJSON(t *testing.T) []byte {
	t.Helper()
	node := map[string]any{
		"name":       "check-instrument-vm",
		"disposable": true,
		"instrument": []any{
			map[string]any{"id": "screen", "phase": []any{"live"}, "spice": map[string]any{"method": "session", "fps": 5}},
			map[string]any{"id": "term", "record": map[string]any{"method": "session"}},
		},
		"peer": map[string]any{
			"driver": map[string]any{
				"local":      map[string]any{"from": "driver-tpl"},
				"instrument": []any{map[string]any{"id": "probe", "phase": []any{"live", "update"}, "record": map[string]any{"method": "session"}}},
			},
		},
	}
	b, err := json.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestResolveInstrumentsVenueScoped(t *testing.T) {
	entries, err := resolveInstruments(resolvedNodeJSON(t), "check-instrument-vm")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	cases := []struct{ scoped, verb string }{
		{"check-instrument-vm.screen", "spice"},
		{"check-instrument-vm.term", "record"},
		{"check-instrument-vm.driver.probe", "record"},
	}
	for i, c := range cases {
		if entries[i].ScopedID != c.scoped || entries[i].Verb != c.verb {
			t.Errorf("entry %d = %q/%q, want %s/%s", i, entries[i].ScopedID, entries[i].Verb, c.scoped, c.verb)
		}
	}
	if !reflect.DeepEqual(entries[1].Phases, []string{"live"}) {
		t.Errorf("term phases = %v, want [live]", entries[1].Phases)
	}
	if !reflect.DeepEqual(entries[2].Phases, []string{"live", "update"}) {
		t.Errorf("driver phases = %v, want [live update]", entries[2].Phases)
	}
	if fps, _ := entries[0].Input["fps"].(int); fps != 5 {
		t.Errorf("screen fps = %v, want 5", fps)
	}
}

func TestResolveInstrumentsDuplicatesRejected(t *testing.T) {
	node := map[string]any{"instrument": []any{
		map[string]any{"id": "a", "spice": map[string]any{}},
		map[string]any{"id": "a", "record": map[string]any{}},
	}}
	b, _ := json.Marshal(node)
	if _, err := resolveInstruments(b, "bed"); err == nil {
		t.Fatal("duplicate venue-scoped id must error")
	}
}

func TestResolveInstrumentsVerbDiscipline(t *testing.T) {
	node := map[string]any{"instrument": []any{map[string]any{"id": "a", "spice": map[string]any{}, "record": map[string]any{}}}}
	b, _ := json.Marshal(node)
	if _, err := resolveInstruments(b, "bed"); err == nil {
		t.Fatal("two capture verbs must error")
	}
	node = map[string]any{"instrument": []any{map[string]any{"id": "a"}}}
	b, _ = json.Marshal(node)
	if _, err := resolveInstruments(b, "bed"); err == nil {
		t.Fatal("verbless instrument must error")
	}
	node = map[string]any{"instrument": []any{map[string]any{"id": "d", "plugin": "record", "plugin_input": map[string]any{"method": "session"}}}}
	b, _ = json.Marshal(node)
	entries, err := resolveInstruments(b, "bed")
	if err != nil {
		t.Fatalf("desugared entry: %v", err)
	}
	if entries[0].Verb != "record" {
		t.Errorf("desugared verb = %q, want record", entries[0].Verb)
	}
}

func TestResolveInstrumentPipelines(t *testing.T) {
	node := map[string]any{"instrument": []any{map[string]any{
		"id":       "screen",
		"spice":    map[string]any{"method": "session"},
		"pipeline": []any{map[string]any{"transcode": map[string]any{"to": "mp4"}}},
	}}}
	b, _ := json.Marshal(node)
	entries, err := resolveInstruments(b, "bed")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(entries[0].Pipeline) != 1 || entries[0].Pipeline[0].Plugin != "transcode" {
		t.Fatalf("pipeline = %+v", entries[0].Pipeline)
	}
	if to, _ := entries[0].Pipeline[0].Input["to"].(string); to != "mp4" {
		t.Errorf("pipeline input to = %v", to)
	}
}

// fakeInstrumentDispatcher records the ops a bracket dispatches and answers PASS.
type fakeInstrumentDispatcher struct {
	ops []*spec.Op
}

func (f *fakeInstrumentDispatcher) dispatch(_ context.Context, op *spec.Op) error {
	f.ops = append(f.ops, op)
	return nil
}

func TestInstrumentBracketDispatch(t *testing.T) {
	entries, err := resolveInstruments(resolvedNodeJSON(t), "check-instrument-vm")
	if err != nil {
		t.Fatal(err)
	}
	disp := &fakeInstrumentDispatcher{}
	rt := &instrumentRuntime{
		bed: "check-instrument-vm", logDir: t.TempDir(), entries: entries,
		collector: newEvidenceCollector(),
		dispatchers: map[string]instrumentDispatcher{
			"check-instrument-vm":        disp,
			"check-instrument-vm.driver": disp,
		},
	}

	if err := rt.runInstrumentBracket(context.Background(), phaseLive, "start"); err != nil {
		t.Fatal(err)
	}
	if err := rt.runInstrumentBracket(context.Background(), phaseLive, "stop"); err != nil {
		t.Fatal(err)
	}
	if len(disp.ops) != 6 {
		t.Fatalf("dispatched %d ops, want 6", len(disp.ops))
	}
	for i, op := range disp.ops {
		input := op.PluginInput
		if len(input) == 0 || input["method"] != "session" {
			t.Errorf("op %d missing method session: %+v", i, op.PluginInput)
		}
		want := "start"
		if i >= 3 {
			want = "stop"
		}
		if input["action"] != want {
			t.Errorf("op %d action = %v, want %s", i, input["action"], want)
		}
		if input["session_id"] == "" || input["state_dir"] == "" {
			t.Errorf("op %d missing session envelope (session_id/state_dir): %+v", i, input)
		}
	}
	rows := rt.collector.sorted()
	if len(rows) != 3 {
		t.Fatalf("evidence rows = %d, want 3", len(rows))
	}
	for _, r := range rows {
		if r.Origin != "session" {
			t.Errorf("row %s origin = %q", r.Instrument, r.Origin)
		}
		if len(r.Segment) != 1 {
			t.Errorf("row %s segments = %d, want 1", r.Instrument, len(r.Segment))
		}
	}
}

// TestInstrumentBracketPhaseGating: the build bracket touches only build-phase entries.
func TestInstrumentBracketPhaseGating(t *testing.T) {
	node := map[string]any{"instrument": []any{
		map[string]any{"id": "b", "phase": []any{"build"}, "record": map[string]any{}},
		map[string]any{"id": "l", "record": map[string]any{}},
	}}
	b, _ := json.Marshal(node)
	entries, _ := resolveInstruments(b, "bed")
	disp := &fakeInstrumentDispatcher{}
	rt := &instrumentRuntime{bed: "bed", logDir: t.TempDir(), entries: entries, collector: newEvidenceCollector(),
		dispatchers: map[string]instrumentDispatcher{"bed": disp}}
	if err := rt.runInstrumentBracket(context.Background(), phaseBuild, "start"); err != nil {
		t.Fatal(err)
	}
	if len(disp.ops) != 1 || disp.ops[0].ID != "bed.b" {
		t.Fatalf("build bracket dispatched %+v, want only bed.b", disp.ops)
	}
}

// TestBuildInstrumentOpShape: the authored input fields ride alongside the envelope.
func TestBuildInstrumentOpShape(t *testing.T) {
	e := &instrumentEntry{ScopedID: "bed.vm.screen", Verb: "spice", Input: map[string]any{"fps": 5}}
	op := buildInstrumentOp(e, "start", sessionEnvelope{ID: "bed.vm.screen", StateDir: "/s/capture/bed.vm.screen", LogDir: "/s"})
	input := op.PluginInput
	if input["fps"] != 5 {
		t.Errorf("authored input lost: %+v", input)
	}
	if op.Plugin != "spice" || op.ID != "bed.vm.screen" {
		t.Errorf("op = %+v", op)
	}
}

// TestProviderRowMerge: segment frames/bytes + artifacts travel blind into the row.
func TestProviderRowMerge(t *testing.T) {
	logDir := filepath.Join(t.TempDir(), ".check", "bed", "2026.999.9999")
	testFile := filepath.Join(logDir, "capture", "bed.screen", "row.json")
	if err := os.MkdirAll(filepath.Dir(testFile), 0o755); err != nil {
		t.Fatal(err)
	}
	row := map[string]any{
		"segment":  []any{map[string]any{"frames": 42, "bytes": 999}},
		"artifact": []any{map[string]any{"path": "/tmp/x.mjpeg", "kind": "mjpeg"}},
	}
	b, _ := json.Marshal(row)
	if err := os.WriteFile(testFile, b, 0o600); err != nil {
		t.Fatal(err)
	}
	entries, _ := resolveInstruments([]byte("{\"instrument\":[{\"id\":\"screen\",\"spice\":{}}]}"), "bed")
	disp := &fakeInstrumentDispatcher{}
	rt := &instrumentRuntime{bed: "bed", logDir: logDir, entries: entries, collector: newEvidenceCollector(),
		dispatchers: map[string]instrumentDispatcher{"bed": disp}}
	if err := rt.runInstrumentBracket(context.Background(), phaseLive, "start"); err != nil {
		t.Fatal(err)
	}
	if err := rt.runInstrumentBracket(context.Background(), phaseLive, "stop"); err != nil {
		t.Fatal(err)
	}
	rows := rt.collector.sorted()
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	seg := rows[0].Segment[0]
	// JSON unmarshal yields float64 numbers — the writer re-emits them as plain ints.
	if seg["frames"] != float64(42) || seg["bytes"] != float64(999) {
		t.Errorf("segment = %+v (frames/bytes not merged)", seg)
	}
	if len(rows[0].Artifact) != 1 {
		t.Errorf("artifacts = %+v", rows[0].Artifact)
	}
	if p := rt.primaryArtifact("bed.screen"); p != "/tmp/x.mjpeg" {
		t.Errorf("primary artifact = %q", p)
	}
}

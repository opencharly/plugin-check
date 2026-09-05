package check

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// evidence_test.go — the evidence envelope deterministic core: venue-keyed sorted rows
// and byte-identical output for identical inputs (the two-runs-identical promise).

func sampleRows() []*evidenceRow {
	return []*evidenceRow{
		{Instrument: "check-z.vm.term", Origin: "session", Verb: "record", Venue: "check-z", Phase: "live",
			Segment:  []map[string]any{{"start": "2026-01-01T00:00:00Z", "stop": "2026-01-01T00:00:10Z", "phase": "live", "frames": 10, "bytes": 100}},
			Artifact: []any{map[string]any{"path": "/tmp/z.cast", "kind": "cast"}},
			Pipeline: []pipelineWord{{Plugin: "transcode", Input: map[string]any{"to": "mp4"}}}},
		{Instrument: "check-a.vm.screen", Origin: "session", Verb: "spice", Venue: "check-a", Phase: "live",
			Segment: []map[string]any{{"start": "2026-01-01T00:00:00Z", "stop": "2026-01-01T00:00:05Z", "phase": "live"}}},
	}
}

// TestWriteEvidenceDeterministic: the same rows produce byte-identical files (the
// venue-keyed order is the sorting discipline).
func TestWriteEvidenceDeterministic(t *testing.T) {
	dir := t.TempDir()
	if err := writeEvidence(dir, sampleRows()); err != nil {
		t.Fatalf("write: %v", err)
	}
	b1, err := os.ReadFile(filepath.Join(dir, "evidence.yml"))
	if err != nil {
		t.Fatal(err)
	}
	dir2 := t.TempDir()
	if err := writeEvidence(dir2, sampleRows()); err != nil {
		t.Fatal(err)
	}
	b2, err := os.ReadFile(filepath.Join(dir2, "evidence.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatalf("evidence not deterministic:\n%s\n---\n%s", b1, b2)
	}
}

// TestWriteEvidenceVenueKeyedOrder: rows land sorted by (venue, instrument), NOT insertion
// order — the collector's sorted() feeds the writer (one sorter, R3).
func TestWriteEvidenceVenueKeyedOrder(t *testing.T) {
	dir := t.TempDir()
	c := newEvidenceCollector()
	for _, e := range []*instrumentEntry{
		{ScopedID: "check-z.vm.term", Verb: "record", Venue: "check-z"},
		{ScopedID: "check-a.vm.screen", Verb: "spice", Venue: "check-a"},
	} {
		c.addSegment(e, "live", "2026-01-01T00:00:00Z", "2026-01-01T00:00:05Z", nil, nil)
	}
	if err := writeEvidence(dir, c.sorted()); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "evidence.yml"))
	iA := bytes.Index(b, []byte("check-a.vm.screen"))
	iZ := bytes.Index(b, []byte("check-z.vm.term"))
	if iA < 0 || iZ < 0 || iA > iZ {
		t.Fatalf("expected check-a before check-z in venue-keyed order:\n%s", b)
	}
}

// TestWriteEvidenceShape: the emitted manifest carries the #EvidenceRow fields.
func TestWriteEvidenceShape(t *testing.T) {
	dir := t.TempDir()
	if err := writeEvidence(dir, sampleRows()); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "evidence.yml"))
	for _, want := range []string{"evidence:", "instrument:", "origin: session", "verb:", "venue:", "segment:", "artifact:", "pipeline:", "plugin:", "phase:"} {
		if !bytes.Contains(b, []byte(want)) {
			t.Errorf("evidence.yml missing %q:\n%s", want, b)
		}
	}
}

// TestCollectorSegmentAccumulation: a [live, update] instrument yields TWO segments in ONE
// row (the honest rebuild split).
func TestCollectorSegmentAccumulation(t *testing.T) {
	c := newEvidenceCollector()
	e := &instrumentEntry{ScopedID: "bed.vm.screen", Verb: "spice", Venue: "bed"}
	c.addSegment(e, "live", "2026-01-01T00:00:00Z", "2026-01-01T00:00:05Z", nil, nil)
	c.addSegment(e, "update", "2026-01-01T00:00:10Z", "2026-01-01T00:00:20Z", nil, nil)
	rows := c.sorted()
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if len(rows[0].Segment) != 2 {
		t.Fatalf("segments = %d, want 2", len(rows[0].Segment))
	}
	if rows[0].Phase != "live" {
		t.Errorf("row phase = %q, want first-phase live", rows[0].Phase)
	}
	if rows[0].Segment[0]["phase"] != "live" || rows[0].Segment[0]["start"] != "2026-01-01T00:00:00Z" {
		t.Errorf("segment 0 = %+v", rows[0].Segment[0])
	}
}

// TestWriteRunEvidenceNoRowsIsNoop: a bed without instruments writes nothing.
func TestWriteRunEvidenceNoRowsIsNoop(t *testing.T) {
	rt := &instrumentRuntime{bed: "bed", logDir: t.TempDir(), entries: nil, collector: newEvidenceCollector(),
		dispatchers: map[string]instrumentDispatcher{}}
	if err := rt.writeRunEvidence(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rt.logDir, "evidence.yml")); !os.IsNotExist(err) {
		t.Fatalf("evidence.yml must not exist without rows (err=%v)", err)
	}
}

// TestEvidencePipelineDispatch: pipelines dispatch blind through the SAME dispatcher seam,
// with the instrument primary artifact injected when not authored.
func TestEvidencePipelineDispatch(t *testing.T) {
	nodeJSON, _ := json.Marshal(map[string]any{"instrument": []any{map[string]any{
		"id":       "screen",
		"spice":    map[string]any{"method": "session"},
		"pipeline": []any{map[string]any{"transcode": map[string]any{"to": "mp4"}}},
	}}})
	entries, err := resolveInstruments(nodeJSON, "bed")
	if err != nil {
		t.Fatal(err)
	}
	disp := &fakeInstrumentDispatcher{}
	c := newEvidenceCollector()
	e := &entries[0]
	c.addSegment(e, "live", "2026-01-01T00:00:00Z", "2026-01-01T00:00:05Z", nil, nil)
	c.rows[e.ScopedID].Artifact = []any{map[string]any{"path": "/tmp/screen.mjpeg", "kind": "mjpeg"}}
	rt := &instrumentRuntime{bed: "bed", logDir: t.TempDir(), entries: entries, collector: c,
		dispatchers: map[string]instrumentDispatcher{"bed": disp}}
	if err := rt.runInstrumentEvidencePhase(context.Background()); err != nil {
		t.Fatalf("evidence phase: %v", err)
	}
	if len(disp.ops) != 1 || disp.ops[0].Plugin != "transcode" {
		t.Fatalf("pipeline ops = %+v", disp.ops)
	}
	input := disp.ops[0].PluginInput
	if input["artifact"] != "/tmp/screen.mjpeg" {
		t.Errorf("pipeline artifact not injected: %+v", input)
	}
	if to, _ := input["to"].(string); to != "mp4" {
		t.Errorf("pipeline input lost: %+v", input)
	}
	rows := c.sorted()
	if len(rows[0].Pipeline) != 1 || rows[0].Pipeline[0].Plugin != "transcode" {
		t.Errorf("executed pipeline not recorded: %+v", rows[0].Pipeline)
	}
}

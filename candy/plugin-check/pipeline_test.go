package check

import "testing"

// TestParsePipelinesDesugaredPair covers the B12 gap: the desugared pipeline stage
// (plugin + plugin_input) must be accepted as ONE verb - the wave's omarchy bed run
// surfaced the pre-fix rejection (exactly one verb per pipeline stage).
func TestParsePipelinesDesugaredPair(t *testing.T) {
	// desugared form (what the parse-time desugar emits for `- transcode: {to: mp4}`)
	raw := []any{
		map[string]any{"plugin": "transcode", "plugin_input": map[string]any{"to": "mp4"}},
	}
	got, err := parsePipelines(raw)
	if err != nil {
		t.Fatalf("desugared pair rejected: %v", err)
	}
	if len(got) != 1 || got[0].Plugin != "transcode" || got[0].Input["to"] != "mp4" {
		t.Fatalf("desugared pair misparsed: %+v", got)
	}
}

// TestParsePipelinesTwoSugarVerbsRejected keeps the exactly-one-verb discipline for the
// authored sugar form.
func TestParsePipelinesTwoSugarVerbsRejected(t *testing.T) {
	raw := []any{map[string]any{"a": map[string]any{}, "b": map[string]any{}}}
	if _, err := parsePipelines(raw); err == nil {
		t.Fatal("two sugar verbs must be rejected")
	}
}

package check

import (
	"os"
	"path/filepath"
	"testing"
)

// `--steps-file` is the AUTHORING surface, so it must accept the same sugar the plan docs
// teach. Before this, a sugar-authored file lost its VERB entirely ("check has no verb set" —
// the step exited 2 while proving nothing) and a matcher shorthand lost its OPERATOR
// ("unsupported matcher op \"\""). The committed G-8 assets hit both on 2026-09-12.
func TestParseStepsFileAcceptsAuthoringSugar(t *testing.T) {
	p := filepath.Join(t.TempDir(), "steps.yml")
	doc := "- check: the emulator is attached\n" +
		"  id: g8-devices-online\n" +
		"  adb:\n" +
		"    method: devices\n" +
		"  context:\n" +
		"    - runtime\n" +
		"  stdout:\n" +
		"    - contains: emulator-5554\n"
	if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	steps, err := parseStepsFile(p)
	if err != nil {
		t.Fatalf("a sugar-authored steps file must parse: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(steps))
	}
	s := steps[0]
	if s.Plugin != "adb" {
		t.Fatalf("Plugin = %q, want adb — the `<word>: <input>` verb sugar must desugar", s.Plugin)
	}
	if got := s.PluginInput["method"]; got != "devices" {
		t.Fatalf("PluginInput[method] = %v, want devices", got)
	}
	if len(s.Stdout) != 1 || s.Stdout[0].Op != "contains" {
		t.Fatalf("Stdout = %+v, want ONE matcher with op=contains (the shorthand needs the JSON decode path)", s.Stdout)
	}
	if s.ID != "g8-devices-online" {
		t.Fatalf("ID = %q, want g8-devices-online", s.ID)
	}
}

// The WIRE form — what the committed G-8 assets use — stays accepted, unchanged.
func TestParseStepsFileAcceptsWireForm(t *testing.T) {
	p := filepath.Join(t.TempDir(), "steps.yml")
	doc := "- check: wire form\n" +
		"  id: w1\n" +
		"  plugin: adb\n" +
		"  plugin_input:\n" +
		"    method: devices\n" +
		"  stdout:\n" +
		"    - op: contains\n" +
		"      value: emulator-5554\n"
	if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	steps, err := parseStepsFile(p)
	if err != nil {
		t.Fatalf("the wire form must parse: %v", err)
	}
	if len(steps) != 1 || steps[0].Plugin != "adb" {
		t.Fatalf("wire form lost its plugin: %+v", steps)
	}
	if len(steps[0].Stdout) != 1 || steps[0].Stdout[0].Op != "contains" {
		t.Fatalf("wire-form matcher = %+v, want op=contains", steps[0].Stdout)
	}
}

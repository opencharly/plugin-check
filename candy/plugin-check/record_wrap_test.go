package check

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/opencharly/spec/spec"
)

// record_wrap_test.go — the deterministic core of the whole-run recording wrap
// (deploy record: field + the --steps-file seam): the step-file generation and
// the argv builder. The venue path (record start/stop over the executor) is the
// R10 bed's job, per this repo's split.

func TestRecordWrapSteps(t *testing.T) {
	dir := t.TempDir()
	rec := map[string]any{"terminal": true, "record_name": "pr-flow", "artifact": "/tmp/pr-flow.cast"}
	startP, stopP, err := recordWrapSteps(dir, rec)
	if err != nil {
		t.Fatalf("recordWrapSteps: %v", err)
	}
	b, err := os.ReadFile(startP)
	if err != nil {
		t.Fatalf("read start: %v", err)
	}
	var steps []spec.Step
	if err := yamlDecode(b, &steps); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	if len(steps) != 1 || steps[0].Op.ID != "record-wrap-start" {
		t.Fatalf("start steps wrong: %+v", steps)
	}
	in := steps[0].Op.PluginInput
	if in["record_mode"] != "terminal" || in["record_name"] != "pr-flow" {
		t.Fatalf("start input wrong: %v", in)
	}
	b2, err := os.ReadFile(stopP)
	if err != nil {
		t.Fatalf("read stop: %v", err)
	}
	var steps2 []spec.Step
	if err := yamlDecode(b2, &steps2); err != nil {
		t.Fatalf("decode stop: %v", err)
	}
	if steps2[0].Op.PluginInput["method"] != "stop" || steps2[0].Op.PluginInput["artifact"] != "/tmp/pr-flow.cast" {
		t.Fatalf("stop input wrong: %v", steps2[0].Op.PluginInput)
	}
}

func TestRecordWrapStepsDesktopEnv(t *testing.T) {
	dir := t.TempDir()
	rec := map[string]any{"desktop": true, "fps": 10, "record_env": map[string]any{"XDG_RUNTIME_DIR": "/run/user/1000"}}
	startP, _, err := recordWrapSteps(dir, rec)
	if err != nil {
		t.Fatalf("recordWrapSteps: %v", err)
	}
	b, _ := os.ReadFile(startP)
	var steps []spec.Step
	if err := yamlDecode(b, &steps); err != nil {
		t.Fatalf("decode: %v", err)
	}
	in := steps[0].Op.PluginInput
	if in["record_mode"] != "desktop" || in["record_fps"] != 10 {
		t.Fatalf("desktop input wrong: %v", in)
	}
	env, ok := in["record_env"].(map[string]any)
	if !ok || env["XDG_RUNTIME_DIR"] != "/run/user/1000" {
		t.Fatalf("record_env wrong: %v", in["record_env"])
	}
}

func TestWrapLiveArgv(t *testing.T) {
	got := wrapLiveArgv("mybed", "/tmp/start.yml", []string{"--var", "k=v"})
	want := []string{"check", "live", "mybed", "--steps-file", "/tmp/start.yml", "--var", "k=v"}
	if len(got) != len(want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDeployRecordWrap(t *testing.T) {
	node := []byte(`{"deploy": {"record": {"terminal": true}}}`)
	rec, ok := deployRecordWrap(node)
	if !ok {
		t.Fatalf("deployRecordWrap found nothing")
	}
	if rec["terminal"] != true {
		t.Fatalf("wrong record block: %v", rec)
	}
	if _, ok := deployRecordWrap([]byte(`{"deploy": {}}`)); ok {
		t.Fatalf("empty deploy must yield nothing")
	}
}

func yamlDecode(b []byte, v any) error {
	return yaml.Unmarshal(b, v)
}

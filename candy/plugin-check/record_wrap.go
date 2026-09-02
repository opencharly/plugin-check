package check

// record_wrap.go — the whole-run recording wrap (deploy record: field). The
// bed runner, when the bed node's deploy map carries a record: block, spans a
// recording around the live phases: BEFORE the first live run it issues a live
// invocation that runs ONLY the injected record: start step (--steps-file seam),
// and AFTER the last live phase one that runs ONLY the record: stop step. The
// record verb executes on the venue and its session persists across invocations
// (package-global registry); the stop output (saved-N-bytes-to) feeds the
// recordings.yml manifest (already merged), where survived_teardown is inherent.

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/opencharly/spec/spec"
)

// deployRecordWrap extracts the deploy record: block from the bed node JSON.
// The node JSON is the bed ROOT FleetNode (spec.Deploy) serialized — a map whose
// TOP level IS the deploy spec (record, disposable, lifecycle, ...), with nested
// peer Members. Parse generically so a spec regen of the Go shape cannot break
// the seam.
func deployRecordWrap(nodeJSON []byte) (map[string]any, bool) {
	var node map[string]any
	if err := yaml.Unmarshal(nodeJSON, &node); err != nil || node == nil {
		return nil, false
	}
	// Canonical shape: record: at the TOP level of the FleetNode (the deploy spec).
	rw, ok := node["record"]
	if !ok {
		// Envelope shape: a wrapper map may carry the deploy spec under a "deploy" key.
		if dep, has := node["deploy"]; has {
			if dm, isMap := dep.(map[string]any); isMap {
				rw, ok = dm["record"]
			}
		}
	}
	if !ok || rw == nil {
		return nil, false
	}
	m, ok := rw.(map[string]any)
	return m, ok
}

// recordWrapSteps builds the start/stop step files for the wrap and returns their paths.
func recordWrapSteps(dir string, rec map[string]any) (startPath, stopPath string, err error) {
	name := "whole-run"
	if v, ok := rec["record_name"].(string); ok && v != "" {
		name = v
	}
	mode := "terminal"
	if v, ok := rec["desktop"].(bool); ok && v {
		mode = "desktop"
	}
	ext := ".cast"
	if mode == "desktop" {
		ext = ".mjpeg"
	}
	artifact := filepath.Join(dir, name+ext)
	if v, ok := rec["artifact"].(string); ok && v != "" {
		artifact = v
	}

	startIn := map[string]any{
		"method":      "start",
		"record_name": name,
		"record_mode": mode,
	}
	if v, ok := rec["fps"].(int); ok && v > 0 {
		startIn["record_fps"] = v
	}
	if env, ok := rec["record_env"].(map[string]any); ok && len(env) > 0 {
		extra := map[string]string{}
		for k, v := range env {
			if s, ok := v.(string); ok {
				extra[k] = s
			}
		}
		startIn["record_env"] = extra
	}
	start := []spec.Step{{
		Check: "whole-run recording starts",
		Op: spec.Op{
			ID:          "record-wrap-start",
			Context:     []spec.Context{"runtime"},
			PluginInput: startIn,
		},
	}}
	stop := []spec.Step{{
		Check: "whole-run recording stops",
		Op: spec.Op{
			ID:      "record-wrap-stop",
			Context: []spec.Context{"runtime"},
			PluginInput: map[string]any{
				"method":      "stop",
				"record_name": name,
				"artifact":    artifact,
			},
		},
	}}

	startPath = filepath.Join(dir, "record-wrap-start.yml")
	stopPath = filepath.Join(dir, "record-wrap-stop.yml")
	if err = writeStepsYAML(startPath, start); err != nil {
		return "", "", err
	}
	if err = writeStepsYAML(stopPath, stop); err != nil {
		return "", "", err
	}
	return startPath, stopPath, nil
}

func writeStepsYAML(path string, steps []spec.Step) error {
	b, err := yaml.Marshal(steps)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// wrapLiveArgv builds the check-live argv for one wrap invocation.
func wrapLiveArgv(ref, stepsFile string, vars []string) []string {
	argv := []string{"check", "live", ref, "--steps-file", stepsFile}
	argv = append(argv, vars...)
	return argv
}

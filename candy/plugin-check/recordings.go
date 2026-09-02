package check

// recordings.go — the recordings manifest (G5): after a disposable bed run, the
// runner scans the live-phase step logs for the record-verb stop outputs
// (plugin-record's "Recording stopped (mode: …); saved N bytes to <path>" and
// plugin-spice's "Recording stopped (name: …, frames: N, bytes: N)"), stats each
// artifact on the HOST, and writes .check/<bed>/<calver>/recordings.yml next to
// summary.yml. Because the write happens AFTER the teardown phases, a present
// artifact IS the copy-before-teardown survival assertion: the recording was
// pulled host-side during the live phases and survived the venue destroy.
//
// Scope: the manifest covers what the step logs carry (asciinema/cast artifacts
// fully — path+bytes+survival; spice video entries with name/frames/bytes and a
// resolved artifact when the log carries one). The full bed-level whole-run wrap
// (a deploy `record:` field starting a recording around the live phases) is the
// larger follow-up surface (spec + runner injection), tracked in the plan.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// recordStopRe matches plugin-record's record: stop output:
//
//	Recording stopped (mode: terminal); saved 914 bytes to /tmp/x.cast
var recordStopRe = regexp.MustCompile(`Recording stopped \(mode: (\S+)\); saved (\d+) bytes to (\S+)`)

// spiceStopRe matches plugin-spice's record: stop output:
//
//	Recording stopped (name: vm-walk, frames: 1, bytes: 112049)
var spiceStopRe = regexp.MustCompile(`Recording stopped \(name: (\S+), frames: (\d+), bytes: (\d+)\)`)

// recordingEntry is one recording in the manifest.
type recordingEntry struct {
	Name     string `yaml:"name"`
	Mode     string `yaml:"mode"`
	Bytes    int    `yaml:"bytes"`
	Frames   int    `yaml:"frames,omitempty"`
	Artifact string `yaml:"artifact,omitempty"`
	Survived bool   `yaml:"survived_teardown"`
}

// scanRecordingsLog extracts the record-verb stop outputs from one step log body.
// Deterministic core, unit-tested.
func scanRecordingsLog(body string) []recordingEntry {
	var out []recordingEntry
	for _, m := range recordStopRe.FindAllStringSubmatch(body, -1) {
		out = append(out, recordingEntry{
			Name:     filepath.Base(m[3]),
			Mode:     m[1],
			Bytes:    atoi(m[2]),
			Artifact: m[3],
		})
	}
	for _, m := range spiceStopRe.FindAllStringSubmatch(body, -1) {
		out = append(out, recordingEntry{
			Name:   m[1],
			Mode:   "desktop-mjpeg",
			Bytes:  atoi(m[3]),
			Frames: atoi(m[2]),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// writeRecordingsManifest scans the live-phase step logs in dir, stats the
// artifacts on the host (survival after teardown), and writes recordings.yml.
func writeRecordingsManifest(dir string) error {
	var entries []recordingEntry
	for _, name := range []string{"check-live.log", "check-live-rebuild.log", "feature-run.log", "feature-run-rebuild.log"} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		entries = append(entries, scanRecordingsLog(string(body))...)
	}
	if len(entries) == 0 {
		return nil // no recordings in this run — no manifest
	}
	var b strings.Builder
	fmt.Fprintln(&b, "recordings:")
	for i := range entries {
		e := &entries[i]
		if e.Artifact != "" {
			_, err := os.Stat(e.Artifact)
			e.Survived = err == nil
		}
		fmt.Fprintf(&b, "  - name: %s\n", e.Name)
		fmt.Fprintf(&b, "    mode: %s\n", e.Mode)
		fmt.Fprintf(&b, "    bytes: %d\n", e.Bytes)
		if e.Frames > 0 {
			fmt.Fprintf(&b, "    frames: %d\n", e.Frames)
		}
		if e.Artifact != "" {
			fmt.Fprintf(&b, "    artifact: %s\n", e.Artifact)
		}
		fmt.Fprintf(&b, "    survived_teardown: %t\n", e.Survived)
	}
	return os.WriteFile(filepath.Join(dir, "recordings.yml"), []byte(b.String()), 0o644)
}

// atoi is a tiny non-erroring int parser for regex digits.
func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

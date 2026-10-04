package check

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

// TestBuildCalVerFromLDFlags pins the parser against the flag strings a real build records. The
// stamp is written by scripts/bootstrap-charly.sh as `-ldflags "-X main.BuildCalVer=<calver>"`, and
// it sits beside other -X assignments there, so "the field after -X" is NOT the rule — matching the
// variable name by prefix is. Each case below is a way to get that wrong.
func TestBuildCalVerFromLDFlags(t *testing.T) {
	cases := []struct {
		name    string
		ldflags string
		want    string
	}{
		{"bootstrap form", "-X main.BuildCalVer=2026.277.1533", "2026.277.1533"},
		{"beside flags", "-s -w -X main.BuildCalVer=2026.277.1533", "2026.277.1533"},
		{"beside another -X", "-X main.Other=x -X main.BuildCalVer=2026.277.1533", "2026.277.1533"},
		{"another -X after it", "-X main.BuildCalVer=2026.277.1533 -X main.Other=x", "2026.277.1533"},
		{"unstamped", "", ""},
		{"unrelated stamp only", "-X main.Other=x", ""},
		{"empty value", "-X main.BuildCalVer=", ""},
		{"spaced flag list", "-s   -w  -X   main.BuildCalVer=2026.277.1533", "2026.277.1533"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildCalVerFromLDFlags(tc.ldflags); got != tc.want {
				t.Fatalf("buildCalVerFromLDFlags(%q) = %q, want %q", tc.ldflags, got, tc.want)
			}
		})
	}
}

// TestBuildVersionFrom pins the fallback order the summary depends on: the ldflags stamp is the
// value `charly version` reports, Main.Version is not. For a bootstrap-form build Main.Version is
// the string "(devel)", and reporting that instead of the CalVer is the specific mistake this
// ordering exists to prevent.
func TestBuildVersionFrom(t *testing.T) {
	cases := []struct {
		name        string
		settings    []debug.BuildSetting
		mainVersion string
		want        string
	}{
		{
			name:        "stamp wins over a devel main version",
			settings:    []debug.BuildSetting{{Key: "-ldflags", Value: "-X main.BuildCalVer=2026.277.1533"}},
			mainVersion: "(devel)",
			want:        "2026.277.1533",
		},
		{
			name:        "stamp wins over a module version",
			settings:    []debug.BuildSetting{{Key: "-ldflags", Value: "-X main.BuildCalVer=2026.277.1533"}},
			mainVersion: "v1.2.3",
			want:        "2026.277.1533",
		},
		{
			name:        "other settings ignored",
			settings:    []debug.BuildSetting{{Key: "CGO_ENABLED", Value: "1"}, {Key: "-ldflags", Value: "-s -w"}},
			mainVersion: "(devel)",
			want:        "unknown",
		},
		{
			name:        "devel with no stamp is unknown",
			settings:    []debug.BuildSetting{{Key: "GOARCH", Value: "amd64"}},
			mainVersion: "(devel)",
			want:        "unknown",
		},
		{
			name:        "no settings at all is unknown",
			mainVersion: "",
			want:        "unknown",
		},
		{
			name:        "module version is the last resort",
			mainVersion: "v0.2026277.1528",
			want:        "v0.2026277.1528",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildVersionFrom(tc.settings, tc.mainVersion); got != tc.want {
				t.Fatalf("buildVersionFrom(%v, %q) = %q, want %q", tc.settings, tc.mainVersion, got, tc.want)
			}
		})
	}
}

// TestCharlyBuildVersionOnTheRunningBinary is the live half: the value recorded for THIS process
// must be a real answer, never the empty string and never "(devel)". A run dir that records
// `driver_version: (devel)` names nothing, which is the defect #779 item 2 reported.
func TestCharlyBuildVersionOnTheRunningBinary(t *testing.T) {
	got := charlyBuildVersion()
	if got == "" {
		t.Fatal(`charlyBuildVersion() = "", want a version string or "unknown"`)
	}
	if got == "(devel)" {
		t.Fatalf(`charlyBuildVersion() = "(devel)", want the stamp or "unknown"`)
	}
}

// TestWriteBedSummaryRecordsTheDriverVersion pins the summary surface, including the negative: an
// unset version must NOT be emitted, so its absence keeps meaning "nothing recorded" rather than
// becoming a line that is always present and always empty.
func TestWriteBedSummaryRecordsTheDriverVersion(t *testing.T) {
	dir := t.TempDir()
	writeBedSummary(dir, &bedRunResult{
		Bed:    "check-foo",
		CalVer: "2026.275.1200",
		OK:     true,
		Driver: bedDriver{Path: "/usr/bin/charly", Version: "2026.277.1533"},
	})
	got := readSummary(t, dir)
	mustContain(t, got, "driver_version: 2026.277.1533", "driver version record")
	mustContain(t, got, "driver: /usr/bin/charly", "driver path record")

	dirUnset := t.TempDir()
	writeBedSummary(dirUnset, &bedRunResult{
		Bed:    "check-foo",
		CalVer: "2026.275.1200",
		OK:     true,
		Driver: bedDriver{Path: "/usr/bin/charly"},
	})
	if unset := readSummary(t, dirUnset); strings.Contains(unset, "driver_version:") {
		t.Fatalf("summary.yml = %q, want no driver_version: line when the version was not recorded", unset)
	}
}

// TestBedStderrCaptureMirrorsAndRestores is the live test for #779 item 3: bytes written to fd 2
// while the capture is installed land in the run dir, bytes written after it is torn down do not,
// and the operator's own stderr keeps receiving them throughout (it is a tee, not a redirection).
//
// It writes through os.Stderr deliberately. os.Stderr IS fd 2, which is the capture point — a
// reassignment of the os.Stderr VARIABLE would miss the Go runtime's own panic and fatal-error
// output, so a test that wrote to a re-pointed variable would be testing the wrong mechanism.
func TestBedStderrCaptureMirrorsAndRestores(t *testing.T) {
	dir := t.TempDir()
	c := startBedStderrCapture(dir)
	if c == nil {
		t.Fatal("startBedStderrCapture returned nil for a writable run dir")
	}
	const during = "charly-bed-stderr-mirror-sentinel-during\n"
	if _, err := os.Stderr.WriteString(during); err != nil {
		t.Fatalf("writing the sentinel to stderr: %v", err)
	}
	c.close()
	const after = "charly-bed-stderr-mirror-sentinel-after\n"
	if _, err := os.Stderr.WriteString(after); err != nil {
		t.Fatalf("writing to stderr after the capture: %v", err)
	}

	path := filepath.Join(dir, bedStderrLogName)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the mirror %s: %v", path, err)
	}
	got := string(b)
	mustContain(t, got, strings.TrimSpace(during), "the mirrored sentinel")
	if strings.Contains(got, strings.TrimSpace(after)) {
		t.Fatalf("the mirror %s still captured stderr after close():\n%s", path, got)
	}
}

// TestBedStderrCaptureRecordsAPanic pins the one bound the tee cannot cover itself. The runtime
// prints a panic only AFTER every deferred function has run, so by then fd 2 is restored and the
// mirror is closed — RecordPanic is what puts the stack in the run dir instead, and it must carry
// both the value and the goroutine trace or the record is useless for diagnosis.
func TestBedStderrCaptureRecordsAPanic(t *testing.T) {
	dir := t.TempDir()
	c := startBedStderrCapture(dir)
	if c == nil {
		t.Fatal("startBedStderrCapture returned nil for a writable run dir")
	}
	c.RecordPanic(errors.New("boom"))
	c.close()

	b, err := os.ReadFile(filepath.Join(dir, bedStderrLogName))
	if err != nil {
		t.Fatalf("reading the mirror: %v", err)
	}
	got := string(b)
	mustContain(t, got, "panic: boom", "the recovered panic value")
	mustContain(t, got, "goroutine ", "the stack trace")
}

// TestBedRunGuardRecordsAPanicWhileTheMirrorIsLive pins the recover→RecordPanic HAND-OFF, which
// the test above cannot see: that one calls RecordPanic directly, so it covers the record's
// FORMAT only. The mechanism is that runCheckBed's LAST-declared defer runs FIRST — while the
// capture is still live and before cap.close() restores fd 2 — and hands the recovered value to
// the mirror. This drives a REAL panic (no synthetic error) through the SAME function the bed run
// defers, in the SAME declaration order, so deleting the RecordPanic call from production fails
// HERE rather than passing on a test-local copy of the line.
func TestBedRunGuardRecordsAPanicWhileTheMirrorIsLive(t *testing.T) {
	dir := t.TempDir()
	cap := startBedStderrCapture(dir)
	if cap == nil {
		t.Fatal("startBedStderrCapture returned nil for a writable run dir")
	}
	state := beginBedVerdict(dir, "panic-bed", "2026.277.0001")
	var (
		res *bedRunResult
		err error
	)

	func() {
		// Absorbs the re-panic bedRunGuard deliberately raises. Declared FIRST so it runs LAST:
		// after the guard has recovered, and after the capture has been closed and synced.
		defer func() { _ = recover() }()
		// Declaration order mirrors runCheckBed exactly — cap.close() first, the guard LAST — so
		// the guard runs FIRST and the stack reaches the file while the capture is live.
		defer cap.close()
		defer bedRunGuard(cap, dir, "panic-bed", state, &res, &err)
		panic("boom-from-the-bed")
	}()

	b, rerr := os.ReadFile(filepath.Join(dir, bedStderrLogName))
	if rerr != nil {
		t.Fatalf("reading the mirror: %v", rerr)
	}
	got := string(b)
	mustContain(t, got, "panic: boom-from-the-bed", "the value recovered from the real panic")
	mustContain(t, got, "goroutine ", "the stack trace")
}

// TestBedStderrCaptureIsNilSafe pins that every entry point tolerates a capture that could not be
// installed. startBedStderrCapture returns nil rather than failing a run — a diagnostic aid must
// never be the reason a bed dies — so runCheckBed holds a possibly-nil capture and calls both
// methods unconditionally from its defers.
func TestBedStderrCaptureIsNilSafe(t *testing.T) {
	var c *bedStderrCapture
	c.RecordPanic(errors.New("boom"))
	c.close()
}

// TestBedStderrCaptureRefusesAnUnwritableDir pins the refusal path: a run dir that cannot be opened
// yields nil, and — because nothing was redirected on that path — the process's stderr is still
// intact afterwards. A capture that redirected fd 2 and THEN failed would leave the run mute.
func TestBedStderrCaptureRefusesAnUnwritableDir(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-dir")
	if c := startBedStderrCapture(missing); c != nil {
		c.close()
		t.Fatalf("startBedStderrCapture(%s) = %#v, want nil for a dir that does not exist", missing, c)
	}
	const sentinel = "charly-bed-stderr-still-attached\n"
	if _, err := os.Stderr.WriteString(sentinel); err != nil {
		t.Fatalf("stderr is unusable after a refused capture: %v", err)
	}
}

// TestWriteBedSummaryReferencesTheStderrMirror pins the summary side of item 3: when the mirror
// exists the summary names it, and when it does not the summary stays silent — the same
// truthful-reference rule as `evidence:`, because a run dir that names a file it did not write is
// worse than one that names nothing.
func TestWriteBedSummaryReferencesTheStderrMirror(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, bedStderrLogName), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("seeding the mirror: %v", err)
	}
	writeBedSummary(dir, &bedRunResult{Bed: "check-foo", CalVer: "2026.275.1200", OK: true})
	mustContain(t, readSummary(t, dir), "runner_stderr: "+bedStderrLogName, "stderr mirror reference")

	empty := t.TempDir()
	writeBedSummary(empty, &bedRunResult{Bed: "check-foo", CalVer: "2026.275.1200", OK: true})
	if got := readSummary(t, empty); strings.Contains(got, "runner_stderr:") {
		t.Fatalf("summary.yml = %q, want no runner_stderr: line when no mirror was written", got)
	}
}

// TestBedStderrCaptureCloseDoesNotHang pins the ORDER inside close(), which is the one thing in
// this file that deadlocks rather than degrades. fd 2 IS a duplicate of the pipe's WRITE end, so
// closing pw BEFORE restoring fd 2 leaves the pipe open by one reference, the drain's io.Copy
// never sees EOF, and close blocks forever.
//
// The defect was found exactly that way — the reversed order hung this package to its 600s panic
// timeout and surfaced as an unexplained FAIL, not as a readable assertion. So the property is
// pinned with its OWN bound: a future reversal fails here in seconds, naming the cause.
func TestBedStderrCaptureCloseDoesNotHang(t *testing.T) {
	dir := t.TempDir()
	c := startBedStderrCapture(dir)
	if c == nil {
		t.Fatal("startBedStderrCapture returned nil for a writable run dir")
	}
	done := make(chan struct{})
	go func() {
		c.close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("close() never returned: the drain is blocked on a write end fd 2 still holds — " +
			"restore fd 2 BEFORE closing pw")
	}
	// The clean path must NOT report a truncation it did not have.
	b, err := os.ReadFile(filepath.Join(dir, bedStderrLogName))
	if err != nil {
		t.Fatalf("reading the mirror: %v", err)
	}
	if strings.Contains(string(b), "drain did not finish") {
		t.Fatalf("mirror = %q, want no truncation note when nothing held fd 2 open", string(b))
	}
}

// TestBedStderrCaptureCloseIsBoundedWhenAChildInheritsFd2 pins the third stated bound, which is
// the reason close() waits on a DEADLINE rather than joining the drain.
//
// A bed spawns containers and VMs, and any of them that inherited fd 2 holds its OWN reference to
// the pipe's write end; close runs BEFORE the bed's teardown, so such a child can still be alive.
// Joining the drain would then hang the run for as long as that child lives. The `sleep` child
// here is that scenario, made deterministic: it inherits the pipe, outlives the capture, and must
// not be able to stall close().
func TestBedStderrCaptureCloseIsBoundedWhenAChildInheritsFd2(t *testing.T) {
	dir := t.TempDir()
	c := startBedStderrCapture(dir)
	if c == nil {
		t.Fatal("startBedStderrCapture returned nil for a writable run dir")
	}
	child := exec.Command("sleep", "30")
	child.Stderr = os.Stderr // inherits the pipe's write end — fd 2 IS the pipe right now
	if err := child.Start(); err != nil {
		t.Fatalf("starting the fd-2-holding child: %v", err)
	}
	defer func() {
		_ = child.Process.Kill()
		_, _ = child.Process.Wait()
	}()

	start := time.Now()
	c.close()
	if elapsed := time.Since(start); elapsed > bedStderrDrainGrace+15*time.Second {
		t.Fatalf("close() took %s, want it bounded near %s even with a child holding fd 2", elapsed, bedStderrDrainGrace)
	}
	b, err := os.ReadFile(filepath.Join(dir, bedStderrLogName))
	if err != nil {
		t.Fatalf("reading the mirror: %v", err)
	}
	// The truncation must be VISIBLE, not silent: a mirror that quietly lost its tail is the
	// diagnosability defect this whole file exists to remove.
	mustContain(t, string(b), "drain did not finish within", "the bounded-drain note")
}

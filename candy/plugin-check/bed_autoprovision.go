package check

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"time"
)

// missingGoldenRe matches the sdk clone-enforcement error and extracts the base vm.
// e.g.: vm "check-charly-omarchy-vm": snapshot "golden" does not exist; create with:
//
//	charly vm snapshot create check-charly-omarchy-vm golden
var missingGoldenRe = regexp.MustCompile(`vm "([^"]+)": snapshot "golden" does not exist`)

// logPathRe extracts the step log path from the runner's error summary
// (the missing-golden detail lives in the log file, not the summary).
var logPathRe = regexp.MustCompile(`log: (\S+)`)

// provisionBaseGoldenRun runs the base bed's FRESH lane (captures the golden).
// A var so a unit test can substitute a fake.
var provisionBaseGoldenRun = func(baseBed string) error {
	cmd := exec.Command("charly", "check", "run", baseBed)
	cmd.Env = append(cmd.Environ(), "CHARLY_BED_AUTOPROVISION=1")
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	var err error
	select {
	case err = <-done:
	case <-time.After(25 * time.Minute):
		_ = cmd.Process.Kill()
		return fmt.Errorf("auto-provision of base bed %s timed out", baseBed)
	}
	if err != nil {
		return err
	}
	// The base's keep_venue domain stays RUNNING and holds the golden as its live
	// backing — the retried clone vm-create would collide (measured: "Is another
	// process using the image"). Stop the base domain so the golden is free.
	// Best-effort; the next vm-build re-creates it.
	stop := exec.Command("charly", "vm", "stop", baseBed, "--domain", baseBed, "--force")
	_ = stop.Run()
	return nil
}

// autoProvisionBaseGolden inspects a vm-build error; when it is the missing-base-
// golden error, it auto-provisions the base and reports that a retry is warranted.
// visited guards against clone cycles. The runner's step error is the subcommand
// SUMMARY ("exited 1 …; log: <path>") — the missing-golden detail lives in the
// referenced log file, so the seam also scans that log.
func autoProvisionBaseGolden(err error, visited map[string]bool, baseIsDisposable func(string) bool) (retry bool, provisionErr error) {
	haystack := err.Error()
	if m := logPathRe.FindStringSubmatch(haystack); m != nil {
		if data, rerr := os.ReadFile(m[1]); rerr == nil {
			haystack += "\n" + string(data)
		}
	}
	m := missingGoldenRe.FindStringSubmatch(haystack)
	if m == nil {
		return false, nil
	}
	base := m[1]
	if visited[base] {
		return false, fmt.Errorf("auto-provision cycle detected at %q", base)
	}
	if baseIsDisposable != nil && !baseIsDisposable(base) {
		return false, fmt.Errorf("auto-provision refused: base bed %q is not disposable", base)
	}
	visited[base] = true
	if err := provisionBaseGoldenRun(base); err != nil {
		return false, fmt.Errorf("auto-provision of %q failed: %w", base, err)
	}
	return true, nil
}

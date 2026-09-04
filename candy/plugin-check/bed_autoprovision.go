package check

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
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
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	last := baseRunProgress(baseBed)
	var stall time.Time
	for {
		select {
		case err := <-done:
			return err // the base run concluded (its steps are individually bounded)
		case <-tick.C:
			p := baseRunProgress(baseBed)
			if p.After(last) {
				last = p
				stall = time.Time{}
				continue
			}
			if stall.IsZero() {
				stall = time.Now()
				continue
			}
			if time.Since(stall) > 2*time.Minute {
				_ = cmd.Process.Kill()
				return fmt.Errorf("auto-provision of base bed %s STALLED (no phase progress for 2m) — failing fast", baseBed)
			}
		}
	}
}

// baseRunProgress returns the newest phase-log mtime for a bed's run (durable
// stall detection for the auto-provision: the nested run must keep advancing
// phases; a stall beyond the bound fails fast instead of waiting on nothing).
// Also treats a freshly-created summary.yml as progress (the run concluded).
func baseRunProgress(bed string) time.Time {
	newest := time.Time{}
	entries, err := os.ReadDir(filepath.Join(".check", bed))
	if err != nil {
		return newest
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		logs, err := os.ReadDir(filepath.Join(".check", bed, e.Name()))
		if err != nil {
			continue
		}
		for _, l := range logs {
			if fi, err := l.Info(); err == nil && fi.ModTime().After(newest) {
				newest = fi.ModTime()
			}
		}
	}
	return newest
}

// baseIsProvablyBed is the REAL disposable guard predicate (B17): the auto-provision
// may only target a bed-shaped name (beds follow the project check-* convention);
// charly check run itself REFUSES non-disposable beds, so the subprocess is the
// enforceable backstop — the guard prevents even spawning it for non-beds.
func baseIsProvablyBed(name string) bool { return strings.HasPrefix(name, "check-") }

// buildVmWithProvisionRetry IS the production seam (bed_run.go calls this): parse +
// guard the missing-base-golden error, provision the parsed BASE bed (real name),
// then retry build once. Unit tests exercise THIS function.
func buildVmWithProvisionRetry(build func() error, provision func(string) error) error {
	if err := build(); err == nil {
		return nil
	} else {
		base, retry, perr := autoProvisionBaseGolden(err, map[string]bool{}, baseIsProvablyBed)
		if perr != nil || !retry {
			return err
		}
		if perr2 := provision(base); perr2 != nil {
			return perr2
		}
		return build()
	}
}

// autoProvisionBaseGolden inspects a vm-build error: when it is the missing-base-
// golden error it PARSEs the sdk-enforcement message (also scanning the referenced
// step log — the runner error is the subcommand SUMMARY) + applies the guards
// (cycle via visited, disposable via baseIsDisposable) and returns the parsed base
// for the caller to provision. It does NOT provision itself.
func autoProvisionBaseGolden(err error, visited map[string]bool, baseIsDisposable func(string) bool) (base string, retry bool, provisionErr error) {
	haystack := err.Error()
	if m := logPathRe.FindStringSubmatch(haystack); m != nil {
		if data, rerr := os.ReadFile(m[1]); rerr == nil {
			haystack += "\n" + string(data)
		}
	}
	m := missingGoldenRe.FindStringSubmatch(haystack)
	if m == nil {
		return "", false, nil
	}
	base = m[1]
	if visited[base] {
		return "", false, fmt.Errorf("auto-provision cycle detected at %q", base)
	}
	if baseIsDisposable != nil && !baseIsDisposable(base) {
		return "", false, fmt.Errorf("auto-provision refused: base bed %q is not disposable", base)
	}
	visited[base] = true
	return base, true, nil
}

package check

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)


// TestSpawnSetsidMergesSpawnEnv is the E-5 seam env-agreement guard: the setsid
// recorder spawn must carry the caller's env dict (e.g. plugin-appium's
// CHARLY_APPIUM_SESSION_FILE stamp) merged OVER os.Environ() — the detached
// recorder's process env is the ONLY channel between the plugin-serve's session
// contract and the recorder's poll loop. A regression here (dropping the dict, or
// replacing os.Environ) would make the recorder re-derive its own session-file
// path and reopen the E-5 zero-bracket defect.
func TestSpawnSetsidMergesSpawnEnv(t *testing.T) {
	forceSetsidTransport(t)
	dir := t.TempDir()
	logDir := filepath.Join(dir, ".check", "check-test", "2026.999.9999")
	outf := filepath.Join(dir, "env.out")
	h, err := spawnSession(context.Background(), sessionSpawnOpts{
		SessionID: "check-test.env-guard",
		Command:   []string{"sh", "-c", "echo $CHARLY_APPIUM_SESSION_FILE:$HOME > " + outf},
		LogDir:    logDir,
		Env:       map[string]string{"CHARLY_APPIUM_SESSION_FILE": "/stamped/sessions/check-android-emulator-pod.json"},
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	defer stopSession(context.Background(), h)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(outf); err == nil {
			got := string(b)
			if !strings.Contains(got, "/stamped/sessions/check-android-emulator-pod.json") {
				t.Fatalf("recorder env = %q: missing the stamped session-file var", got)
			}
			if strings.TrimSpace(got) == "" || strings.HasPrefix(got, ":") {
				t.Fatalf("recorder env = %q: HOME was dropped (os.Environ not merged)", got)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("recorder never wrote env.out (spawn did not run with the merged env)")
}

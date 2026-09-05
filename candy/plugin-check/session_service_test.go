package check

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// session_service_test.go — the background-session service's deterministic core: transport
// selection, the setsid spawn→stop lifecycle against a REAL detached recorder, handle
// round-trips, the orphan sweep, and idempotent stops. The systemd unit path is
// environment-dependent (RDD-2 proved it live), so the systemd side is covered by the
// selection test + the live bed R10; the full lifecycle here runs on the setsid transport,
// which is deterministic in any environment.

// forceSetsidTransport pins the transport probe so the setsid path is deterministic.
func forceSetsidTransport(t *testing.T) {
	t.Helper()
	old := systemdUserSessionAvailable
	systemdUserSessionAvailable = func() bool { return false }
	t.Cleanup(func() { systemdUserSessionAvailable = old })
}

func TestSpawnSessionSetsidLifecycle(t *testing.T) {
	forceSetsidTransport(t)
	dir := t.TempDir()
	logDir := filepath.Join(dir, ".check", "check-test", "2026.999.9999")
	h, err := spawnSession(context.Background(), sessionSpawnOpts{
		SessionID: "check-test.vm.screen",
		Command:   []string{"sh", "-c", "sleep 30"},
		LogDir:    logDir,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if h.Transport != sessionTransportSetsid {
		t.Fatalf("transport = %q, want setsid", h.Transport)
	}
	if h.Status != sessionStatusActive {
		t.Fatalf("status = %q, want active", h.Status)
	}
	if h.PID <= 0 {
		t.Fatalf("pid not recorded")
	}
	if _, err := os.Stat(h.Pidfile); err != nil {
		t.Fatalf("pidfile %s missing: %v", h.Pidfile, err)
	}
	_ = h

	// The handle must be on disk (the cross-invocation source of truth) and readable.
	disk, derr := SessionHandleFromDisk(h.StateDir)
	if derr != nil || disk == nil {
		t.Fatalf("handle from disk: %v %v", disk, derr)
	}
	if disk.SessionID != "check-test.vm.screen" || disk.Transport != sessionTransportSetsid {
		t.Fatalf("disk handle wrong: %+v", disk)
	}

	// Liveness: the process is alive before the stop, dead after.
	if !SessionLiveness(context.Background(), disk) {
		t.Fatal("session should be alive after spawn")
	}
	stopSession(context.Background(), disk)
	if SessionLiveness(context.Background(), disk) {
		t.Fatal("session should be dead after stop")
	}
	if disk.Status != sessionStatusStopped {
		t.Fatalf("status = %q after stop, want stopped", disk.Status)
	}
	// Idempotent: stopping again is a no-op, never an error.
	stopSession(context.Background(), disk)
}

func TestSpawnSessionRejectsBadInput(t *testing.T) {
	forceSetsidTransport(t)
	_, err := spawnSession(context.Background(), sessionSpawnOpts{SessionID: "x", LogDir: t.TempDir()})
	if err == nil {
		t.Fatal("empty command must error")
	}
	_, err = spawnSession(context.Background(), sessionSpawnOpts{Command: []string{"sh", "-c", "true"}, LogDir: t.TempDir()})
	if err == nil {
		t.Fatal("empty session id must error")
	}
}

// TestOrphanSweepReapsStaleSession plants a REAL detached recorder under a "previous run"
// calver and proves the bedSetup sweep finalizes it (the crashed-run recovery path).
func TestOrphanSweepReapsStaleSession(t *testing.T) {
	forceSetsidTransport(t)
	base := t.TempDir()
	oldCalver := filepath.Join(base, "check-orphan-bed", "2026.100.0100")
	h, err := spawnSession(context.Background(), sessionSpawnOpts{
		SessionID: "check-orphan-bed.vm.screen",
		Command:   []string{"sh", "-c", "sleep 30"},
		LogDir:    oldCalver,
	})
	if err != nil {
		t.Fatalf("plant stale session: %v", err)
	}

	sweepStaleSessions(context.Background(), filepath.Join(base, "check-orphan-bed"))

	disk, _ := SessionHandleFromDisk(h.StateDir)
	if disk == nil || disk.Status != sessionStatusStopped {
		t.Fatalf("stale session not finalized: %+v", disk)
	}
	if SessionLiveness(context.Background(), disk) {
		t.Fatal("stale recorder process still alive after sweep")
	}
}

// TestSweepIgnoresFreshStateDir: a capture dir with no handle (or a stopped handle) must
// not trip the sweep (the rc=5/already-gone tolerance).
func TestSweepToleratesHandlelessStateDir(t *testing.T) {
	forceSetsidTransport(t)
	base := t.TempDir()
	orphanDir := filepath.Join(base, "check-x", "2026.101.0101", "capture", "some-id")
	if err := os.MkdirAll(orphanDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sweepStaleSessions(context.Background(), filepath.Join(base, "check-x")) // must not panic/error
	// A stopped handle: the sweep skips it silently.
	logDir := filepath.Join(base, "check-x", "2026.102.0102")
	h, err := spawnSession(context.Background(), sessionSpawnOpts{
		SessionID: "check-x.vm.t",
		Command:   []string{"sh", "-c", "sleep 30"},
		LogDir:    logDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	stopSession(context.Background(), h)
	sweepStaleSessions(context.Background(), filepath.Join(base, "check-x"))
	disk, _ := SessionHandleFromDisk(h.StateDir)
	if disk == nil || disk.Status != sessionStatusStopped {
		t.Fatalf("stopped handle disturbed by sweep: %+v", disk)
	}
}

// TestFinalizeSessionDir stops live sessions under ONE run dir (the teardown-owner path).
func TestFinalizeSessionDir(t *testing.T) {
	forceSetsidTransport(t)
	logDir := filepath.Join(t.TempDir(), ".check", "check-f", "2026.103.0103")
	h, err := spawnSession(context.Background(), sessionSpawnOpts{
		SessionID: "check-f.vm.live",
		Command:   []string{"sh", "-c", "sleep 30"},
		LogDir:    logDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	finalizeSessionDir(context.Background(), logDir)
	disk, _ := SessionHandleFromDisk(h.StateDir)
	if disk == nil || disk.Status != sessionStatusStopped {
		t.Fatalf("finalize left session live: %+v", disk)
	}
	if SessionLiveness(context.Background(), disk) {
		t.Fatal("recorder still alive after finalize")
	}
}

// TestSessionEnvPairsDeterministic: the recorder env must be sorted (reproducible spawns).
func TestSessionEnvPairsDeterministic(t *testing.T) {
	got := envPairs(map[string]string{"Z": "1", "A": "2", "M": "3"})
	want := []string{"A=2", "M=3", "Z=1"}
	if len(got) != len(want) {
		t.Fatalf("envPairs = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("envPairs = %v, want %v", got, want)
		}
	}
}

// TestSessionStateDirShape: the state dir lives under the run's capture root.
func TestSessionStateDirShape(t *testing.T) {
	got := sessionStateDir("/a/.check/b/2026.1.2", "b.vm.screen")
	want := filepath.Join("/a/.check/b/2026.1.2", "capture", "b.vm.screen")
	if got != want {
		t.Fatalf("state dir = %q, want %q", got, want)
	}
}

// TestSpawnSessionStopWithinTimeout: a stop must return within the bounded window even for
// a recorder that ignores SIGTERM (the SIGKILL escalation ladder ends it).
func TestSpawnSessionStopEscalates(t *testing.T) {
	forceSetsidTransport(t)
	h, err := spawnSession(context.Background(), sessionSpawnOpts{
		SessionID: "check-e.vm.stuck",
		// Ignore SIGTERM (trap "") — only the SIGKILL escalation can end it.
		Command: []string{"sh", "-c", "trap '' TERM; while true; do sleep 1; done"},
		LogDir:  filepath.Join(t.TempDir(), ".check", "check-e", "2026.104.0104"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	stopSession(ctx, h)
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("stop took %v — the bounded escalation ladder is broken", d)
	}
	if SessionLiveness(context.Background(), h) {
		t.Fatal("stuck recorder still alive")
	}
}

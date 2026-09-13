package check

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/spec/lock"
)

// bed_lock_test.go — the regression guard for the PROJECT-INDEPENDENT bed lock. The defect it
// pins: the duplicate-run guard keyed on `.check/<bed>/.lock`, a path derived from the invoking
// project directory, so two runs of the SAME bed from two directories saw no contention while
// claiming the SAME container/volume names in the shared podman store. The measured shape (check
// run 2026.255.2323): the run went green through [start], then its pod container stopped existing
// before its probes ran — 67 probes at exit-125 (`no container with name or ID … found`), 28 lines
// carrying `container state improper`, 15 lines carrying exit=255, and nothing in the run attributing the disappearance
// to itself, i.e. it came from outside. The peer-lane SIGTERM attribution is that campaign's
// report (distro-cachyos#83).

// TestBedRunLockPath_IsProjectIndependent is the failing-then-passing half: with the old
// project-relative key, the path CHANGED with the working directory and this test fails.
func TestBedRunLockPath_IsProjectIndependent(t *testing.T) {
	const bed = "check-githubrunner-pod"

	dirA, dirB := t.TempDir(), t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(cwd) }()

	if err := os.Chdir(dirA); err != nil {
		t.Fatalf("chdir A: %v", err)
	}
	pathA, err := bedRunLockPath(bed)
	if err != nil {
		t.Fatalf("bedRunLockPath A: %v", err)
	}
	if err := os.Chdir(dirB); err != nil {
		t.Fatalf("chdir B: %v", err)
	}
	pathB, err := bedRunLockPath(bed)
	if err != nil {
		t.Fatalf("bedRunLockPath B: %v", err)
	}
	if pathA != pathB {
		t.Fatalf("bed lock path depends on the project directory: %q (in %s) vs %q (in %s) — two runs of one bed from two directories would not contend while sharing container/volume names in the podman store", pathA, dirA, pathB, dirB)
	}

	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("UserCacheDir: %v", err)
	}
	if wantDir := filepath.Join(cache, "charly", "locks"); filepath.Dir(pathA) != wantDir {
		t.Fatalf("bed lock dir = %q; want the user cache %q (beside the image build locks)", filepath.Dir(pathA), wantDir)
	}
	if !strings.HasPrefix(filepath.Base(pathA), "bed-") {
		t.Fatalf("bed lock file = %q; want the bed-<sha8>.lock shape", filepath.Base(pathA))
	}

	// A DIFFERENT bed must take a DIFFERENT lock — the guard scopes to one bed, not to all beds.
	other, err := bedRunLockPath("check-versa-pod")
	if err != nil {
		t.Fatalf("bedRunLockPath other: %v", err)
	}
	if other == pathA {
		t.Fatalf("two different beds share one lock path %q — the guard would serialize unrelated beds", pathA)
	}
}

// TestBedRunLockPath_GoldenKey pins the key derivation: `bed-` + sha256(bed)[:8]. Not decoration —
// the path IS the contract between the guard (bed_session.go) and `charly check stop`
// (stop_cmd.go), so a silent change to it would make both quietly disagree.
func TestBedRunLockPath_GoldenKey(t *testing.T) {
	const bed = "check-githubrunner-pod"
	sum := sha256.Sum256([]byte(bed))
	want := "bed-" + hex.EncodeToString(sum[:8]) + ".lock"
	got, err := bedRunLockPath(bed)
	if err != nil {
		t.Fatalf("bedRunLockPath: %v", err)
	}
	if filepath.Base(got) != want {
		t.Fatalf("bed lock key = %q, want %q — changing the key silently decouples the run guard from charly check stop", filepath.Base(got), want)
	}
}

// TestBedRunLock_ContendsOnTheSameBed pins the MECHANISM the guard relies on: the same bed's lock
// contends (a second non-blocking acquire returns ErrLockBusy — exactly what bed_session.go
// turns into the loud refusal), while another bed's lock does not.
func TestBedRunLock_ContendsOnTheSameBed(t *testing.T) {
	bedPath, err := bedRunLockPath("check-githubrunner-pod")
	if err != nil {
		t.Fatalf("bedRunLockPath: %v", err)
	}
	release, err := lock.AcquireFileLock(bedPath, false)
	if err != nil {
		t.Fatalf("acquire the bed lock (first run): %v", err)
	}
	defer func() { _ = release() }()

	if _, err := lock.AcquireFileLock(bedPath, false); !errors.Is(err, lock.ErrLockBusy) {
		t.Fatalf("a second acquire of the same bed's lock returned %v; want ErrLockBusy — without contention the guard cannot refuse a concurrent run", err)
	}

	otherPath, err := bedRunLockPath("check-versa-pod")
	if err != nil {
		t.Fatalf("bedRunLockPath other: %v", err)
	}
	otherRelease, err := lock.AcquireFileLock(otherPath, false)
	if err != nil {
		t.Fatalf("another bed's lock must not be blocked by this one: %v", err)
	}
	_ = otherRelease()
}

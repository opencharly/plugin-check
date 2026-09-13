package check

// bed_lock.go — the PROJECT-INDEPENDENT identity of "this bed is running".
//
// The duplicate-run guard already existed (bed_session.go) but keyed on `.check/<bed>/.lock`,
// derived from the INVOKING PROJECT DIRECTORY. So two runs of the SAME bed started from two
// project directories — a scratch clone, a second checkout, two CI lanes — took DIFFERENT lock
// files, saw no contention, and both proceeded. What they collide on is not the project: a bed
// run claims the container/volume names derived from its deploy identity in the USER's shared
// podman store, so the second run reclaimed the first run's containers.
//
// From check run 2026.255.2323's OWN output: it ran green through [start] (01:29:12), then its
// pod container `charly-check-githubrunner-pod` stopped existing before its probes ran (01:29:34)
// — 67 probes failed with exit-125 (`no container with name or ID … found`), 28 lines carry
// `container state improper`, and 15 lines carry exit=255. Nothing in that run attributes the disappearance to
// itself, i.e. it came from OUTSIDE the run; the peer-lane SIGTERM is that campaign's report
// (distro-cachyos#83), not something this run's log shows. A project-scoped guard cannot see an
// outside run, which is the whole point of the user-scoped key below.
//
// The image build lock made exactly this move for exactly this reason (opencharly/charly
// CHANGELOG 2026.189.0437, "image build lock is user-scoped on the full ref (cross-project
// race)"): the key belongs to the resource that COLLIDES, never to the directory the command
// happened to run from. A bed's colliding resource is its deploy identity, so the key is the BED
// NAME, in the user cache beside the image locks — and `charly check stop <bed>` (stop_cmd.go),
// which resolves the SAME path to find the runner, becomes project-independent with it.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// bedRunLockPath returns the user-scoped lock file a bed run holds for its whole lifetime, and
// that `charly check stop <bed>` resolves to find the holder. Hash of the BED NAME, `bed-<sha8>`,
// in `$XDG_CACHE_HOME/charly/locks` beside `image-<sha8>.lock`.
func bedRunLockPath(bed string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("check bed lock: %w", err)
	}
	dir := filepath.Join(cache, "charly", "locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("check bed lock dir: %w", err)
	}
	sum := sha256.Sum256([]byte(bed))
	return filepath.Join(dir, "bed-"+hex.EncodeToString(sum[:8])+".lock"), nil
}

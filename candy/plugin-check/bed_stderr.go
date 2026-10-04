package check

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"syscall"
	"time"
)

// dupOntoFd points newfd at the same open file description as oldfd, replacing whatever newfd had.
//
// Dup3 rather than Dup2, and this is measured, not stylistic: `GOOS=linux GOARCH=arm64 go build`
// fails on syscall.Dup2 with "undefined: syscall.Dup2" — that wrapper exists for the arches whose
// kernel has the dup2 syscall and NOT for arm64, which has only dup3. Dup3 is present on every
// linux arch the project builds for, so ONE code path serves them all and no build tag is needed.
//
// EINVAL when oldfd == newfd is a dup3 property, not a hazard here: both call sites pass a
// descriptor this file just obtained (a fresh dup, or a pipe end), and fd 2 is open throughout, so
// neither can BE 2.
func dupOntoFd(oldfd int, newfd int) error {
	return syscall.Dup3(oldfd, newfd, 0)
}

// bedStderrLogName is the mirror's file name inside the run dir. It is a constant because TWO
// places must agree on it — the writer here and the `runner_stderr:` reference writeBedSummary
// emits — and a run dir that names a file it did not write is worse than one that names nothing.
const bedStderrLogName = "runner.stderr.log"

// bedStderrCapture mirrors the running process's stderr into the run dir, so a message the
// runner never gets a chance to HANDLE — a `fatal error:` raised by the runtime, a panic, a
// signal — is visible in the run dir afterwards instead of only on a terminal nobody kept
// (opencharly/charly#779, item 3: "capture the runner's stderr into the run dir").
//
// fd 2 is the capture point, not os.Stderr, because the Go runtime writes a panic and a fatal
// error straight to fd 2: a reassignment of the os.Stderr VARIABLE would miss both, and on the
// fatal path every deferred function is skipped, so no in-process hook can catch it either.
//
// It is a TEE, not a redirection. A bed run is tens of minutes long and its progress stream is
// the operator's only live view of it; sending that to a file would trade a diagnosability
// defect for an observability one. Every captured chunk is written to the saved original stderr
// AND to the file.
//
// THREE BOUNDS, stated rather than implied — this type cannot close any of them:
//
//   - A PANIC'S STACK TRACE does not come through the tee. The runtime prints a panic only
//     AFTER every deferred function has run, and this capture is torn down by one of them, so
//     by the time the stack is printed fd 2 is already restored. runCheckBed therefore records
//     debug.Stack() itself, through RecordPanic, while the capture is still live.
//   - The LAST bytes before an abrupt death may not be drained: a SIGKILL (uncatchable, and
//     this plugin's own stop escalation) or a fatal error that exits mid-copy. The mirror is
//     best-effort by construction; the verdict itself is the part that is not.
//   - The drain is BOUNDED, not joined indefinitely. A process that inherited fd 2 while the
//     capture was live holds its own reference to the write end, and close runs BEFORE the bed's
//     teardown, so such a child can still be alive when the run ends. Waiting on it would hang
//     the run for as long as that child lives. Past the grace the mirror stops waiting, says so
//     in the run dir, and leaves the descriptor open (see close).
type bedStderrCapture struct {
	mu      sync.Mutex
	file    *os.File
	orig    *os.File // a dup of the original fd 2, kept for the operator's live view
	pr      *os.File
	pw      *os.File
	done    chan struct{}
	closing bool // close has been entered; a second call is a no-op
	closed  bool // fully torn down; Write no longer touches file/orig
}

// bedStderrDrainGrace bounds close's wait for the drain. Generous by orders of magnitude for
// what it covers — the pipe holds at most 64 KiB and the drain is a memory-to-disk copy — and
// short enough that a leaked descriptor can never stall a bed run.
const bedStderrDrainGrace = 2 * time.Second

// startBedStderrCapture redirects fd 2 into <dir>/runner.stderr.log and returns the live
// capture, or nil when the mirror could not be installed.
//
// A nil return is never fatal: the capture is a diagnostic aid, and a run must not fail because
// a diagnostic aid could not be opened. Each failure says why on the (still original) stderr.
func startBedStderrCapture(dir string) *bedStderrCapture {
	path := filepath.Join(dir, bedStderrLogName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "charly check run: cannot open %s for the stderr mirror: %v\n", path, err)
		return nil
	}
	// Dup BEFORE the redirect: this descriptor is the operator's stderr from here on, and it is
	// also what fd 2 is restored to when the run ends.
	origFd, err := syscall.Dup(2)
	if err != nil {
		fmt.Fprintf(os.Stderr, "charly check run: cannot duplicate stderr for the mirror: %v\n", err)
		_ = file.Close()
		return nil
	}
	orig := os.NewFile(uintptr(origFd), "stderr")
	if orig == nil {
		fmt.Fprintf(os.Stderr, "charly check run: cannot wrap the duplicated stderr fd\n")
		_ = syscall.Close(origFd)
		_ = file.Close()
		return nil
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "charly check run: cannot create the stderr mirror pipe: %v\n", err)
		_ = orig.Close()
		_ = file.Close()
		return nil
	}
	if err := dupOntoFd(int(pw.Fd()), 2); err != nil {
		fmt.Fprintf(os.Stderr, "charly check run: cannot redirect stderr into the mirror: %v\n", err)
		_ = pr.Close()
		_ = pw.Close()
		_ = orig.Close()
		_ = file.Close()
		return nil
	}
	c := &bedStderrCapture{file: file, orig: orig, pr: pr, pw: pw, done: make(chan struct{})}
	go c.mirror()
	return c
}

// mirror drains the pipe for the lifetime of the capture. Every chunk written to fd 2 by
// anything in this process — the runner's own progress lines, the runtime, a forked child that
// inherited the descriptor — arrives here.
func (c *bedStderrCapture) mirror() {
	defer close(c.done)
	if _, err := io.Copy(c, c.pr); err != nil {
		// A clean end is EOF when the write end closes; a real read error would silently lose
		// the mirror, so it is recorded in the very file the mirror exists to produce.
		c.mu.Lock()
		fmt.Fprintf(c.file, "bed stderr mirror ended early: %v\n", err)
		c.mu.Unlock()
	}
}

// Write mirrors one chunk to the operator's stderr and to the run-dir file. It is the pipe's
// drain target; mu is what keeps it from interleaving with RecordPanic's own write.
func (c *bedStderrCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		// Only reachable if a writer raced the teardown. The chunk is already lost, so report it
		// consumed rather than failing a writer that has nothing to do with the mirror.
		return len(p), nil
	}
	_, _ = c.orig.Write(p)
	return c.file.Write(p)
}

// RecordPanic writes a recovered panic's stack into the mirror. runCheckBed calls it from the
// deferred writer, before that defer tears the capture down: the runtime's OWN panic print
// happens after every defer has run, so the tee cannot see it (see the type comment).
func (c *bedStderrCapture) RecordPanic(r any) {
	if c == nil {
		return
	}
	_, _ = c.Write([]byte(fmt.Sprintf("panic: %v\n%s", r, debug.Stack())))
}

// close restores fd 2 and flushes the mirror. Safe on a nil capture, and safe to call once.
func (c *bedStderrCapture) close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.closing {
		c.mu.Unlock()
		return
	}
	c.closing = true
	c.mu.Unlock()

	// ORDER IS THE WHOLE MECHANISM HERE, and the wrong order is a DEADLOCK rather than a leak —
	// measured, not reasoned: with the two steps swapped the drain never ends and the test binary
	// hangs to its 600s timeout. fd 2 IS a duplicate of the pipe's WRITE end, so closing pw alone
	// leaves the pipe open by one reference and the drain's io.Copy never sees EOF, which makes
	// <-c.done wait on a stream that cannot end. Restoring fd 2 first removes that reference;
	// only then does closing pw actually end the stream. orig is closed below and fd 2 keeps the
	// same open file description alive, so the operator's stderr survives the run.
	if err := dupOntoFd(int(c.orig.Fd()), 2); err != nil {
		c.mu.Lock()
		fmt.Fprintf(c.file, "bed stderr mirror: restoring stderr: %v\n", err)
		c.mu.Unlock()
	}
	_ = c.pw.Close()

	// BOUNDED, because the child that got away is real: anything that inherited fd 2 while the
	// capture was live holds its OWN reference to the write end, and this close runs BEFORE the
	// bed's teardown, so such a child can still be alive right now. Joining the drain would hang
	// the run for as long as that child lives.
	select {
	case <-c.done:
	case <-time.After(bedStderrDrainGrace):
		// Stop waiting, and record why in the very file the mirror exists to produce. Everything
		// already mirrored is synced below, so the record is truthful about the gap. The
		// descriptors stay OPEN on purpose: closing them would free their numbers for reuse while
		// the blocked io.Copy still reads through them, and a later write landing in an unrelated
		// file is far worse than a descriptor the process releases at exit anyway.
		c.mu.Lock()
		fmt.Fprintf(c.file, "bed stderr mirror: drain did not finish within %s — the last bytes may be missing (a process that inherited fd 2 is still alive)\n", bedStderrDrainGrace)
		c.mu.Unlock()
		_ = c.file.Sync()
		return
	}

	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	_ = c.orig.Close()
	_ = c.pr.Close()
	// The file is a crash-diagnostic artifact, so the bytes belong on disk rather than in the
	// page cache: a reader that arrives after an abrupt death still sees them.
	_ = c.file.Sync()
	_ = c.file.Close()
}

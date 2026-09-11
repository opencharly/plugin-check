package check

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// bed_readiness_test.go — G-3c unit gate for the bounded post-restart readiness wait. The
// probe is injected (waitVenueReady's own seam), so the deadline contract is exercised without
// an SSH venue; the real probe (vmSshReadyProbe) is covered by the live bed run.

func TestWaitVenueReady_ReadyReturnsPromptly(t *testing.T) {
	calls := 0
	probe := func(context.Context) error { calls++; return nil }
	start := time.Now()
	if err := waitVenueReady(context.Background(), 5*time.Second, "check-x", probe); err != nil {
		t.Fatalf("ready probe: unexpected error %v", err)
	}
	if calls != 1 {
		t.Fatalf("probe calls = %d, want exactly 1 (the deadline must not be waited out)", calls)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("ready probe took %s — a satisfied readiness wait must return immediately", elapsed)
	}
}

func TestWaitVenueReady_NeverReadyFailsAtTheDeadlineWithTheNamedCondition(t *testing.T) {
	// The lease-less guest: sshd never answers, so the poll only ever observes the deadline.
	probe := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	start := time.Now()
	err := waitVenueReady(context.Background(), 50*time.Millisecond, "check-x", probe)
	if err == nil {
		t.Fatal("never-ready probe: want a deadline error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error %v does not wrap context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("deadline error took %s — the total deadline must bound the wait", elapsed)
	}
	for _, want := range []string{"check-x", "50ms", "not ready", "IPv4 lease", "STATE:20"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("deadline error %q does not name %q", err.Error(), want)
		}
	}
}

func TestWaitVenueReady_ProbeFailureIsNotReportedAsAReadinessTimeout(t *testing.T) {
	probe := func(context.Context) error { return errors.New("host key verification failed") }
	err := waitVenueReady(context.Background(), time.Second, "check-x", probe)
	if err == nil {
		t.Fatal("failing probe: want an error, got nil")
	}
	if !strings.Contains(err.Error(), "host key verification failed") {
		t.Fatalf("error %v does not carry the probe failure", err)
	}
	if strings.Contains(err.Error(), "IPv4 lease") {
		t.Errorf("a permanent probe failure must not be reported as a readiness timeout: %v", err)
	}
}

func TestWaitVenueReady_ParentCancellationPropagates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	probe := func(c context.Context) error {
		<-c.Done()
		return c.Err()
	}
	err := waitVenueReady(ctx, time.Minute, "check-x", probe)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v does not wrap context.Canceled", err)
	}
	if strings.Contains(err.Error(), "IPv4 lease") {
		t.Errorf("a cancelled run must not be reported as a lease-less guest: %v", err)
	}
}

func TestRebuildReadyDeadline_IsTighterThanTheCapItBounds(t *testing.T) {
	// The RCA's stall was the 30-minute cap-only readiness window
	// (spec/poll's readinessAbsoluteCapFallback). The bound must stay strictly inside it, and
	// stay long enough that a healthy reboot under a parallel suite cannot be misclassified.
	const capOnlyWindow = 30 * time.Minute
	if rebuildReadyDeadline <= 0 || rebuildReadyDeadline >= capOnlyWindow {
		t.Fatalf("rebuildReadyDeadline = %s, want (0, %s)", rebuildReadyDeadline, capOnlyWindow)
	}
	if rebuildReadyDeadline < 3*time.Minute {
		t.Fatalf("rebuildReadyDeadline = %s, too tight for a loaded host to be told apart from a setup defect", rebuildReadyDeadline)
	}
}

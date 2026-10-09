package check

import (
	"context"
	"strings"
	"testing"

	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// resolve_endpoint_test.go — pins the drain-after-Invoke ordering invariant team-lead flagged as
// the correctness rider for #55 W3 B7's relocation: forwards opened DURING a verb's Invoke must
// stay open (tracked, never closed early) until the LATER drain signal arrives, and must then
// close in LIFO order (last opened, first closed — mirroring the former core-side
// hostVerbResolver.endpointCleanups drain this replaces). checkhost.EndpointForVenue's ssh-venue
// branch spawns a REAL `ssh -NT -L` subprocess, unsuitable for a fast in-memory unit test — this
// exercises pendingEndpointCleanups' own append/drain mechanics directly (the gate-1 teeth
// pattern), recording fake cleanup calls instead of opening a real tunnel.

// resetPendingEndpointCleanups clears package state between test cases (tests share the
// package-level pendingEndpointCleanups var).
func resetPendingEndpointCleanups(t *testing.T) {
	t.Helper()
	pendingEndpointCleanupsMu.Lock()
	pendingEndpointCleanups = nil
	pendingEndpointCleanupsMu.Unlock()
}

func TestDrainEndpointCleanups_LIFOOrder(t *testing.T) {
	resetPendingEndpointCleanups(t)
	var closedOrder []string

	// Simulate THREE resolveEndpointForHost calls within one verb's Invoke, each tracking its own
	// forward's cleanup — exactly what resolveEndpointForHost appends to pendingEndpointCleanups.
	pendingEndpointCleanupsMu.Lock()
	pendingEndpointCleanups = append(pendingEndpointCleanups,
		func() { closedOrder = append(closedOrder, "first") },
		func() { closedOrder = append(closedOrder, "second") },
		func() { closedOrder = append(closedOrder, "third") },
	)
	pendingEndpointCleanupsMu.Unlock()

	// Nothing closes until the drain signal arrives — the correctness invariant: a forward opened
	// during resolve must outlive the resolve call itself (the verb dials through it AFTER
	// resolving, before the drain).
	if len(closedOrder) != 0 {
		t.Fatalf("cleanups fired before drain: %v", closedOrder)
	}

	if _, err := drainEndpointCleanupsForHost(context.Background(), &pb.InvokeRequest{}); err != nil {
		t.Fatalf("drainEndpointCleanupsForHost: %v", err)
	}

	want := []string{"third", "second", "first"} // LIFO — last opened, first closed
	if len(closedOrder) != len(want) {
		t.Fatalf("closedOrder = %v, want %v", closedOrder, want)
	}
	for i := range want {
		if closedOrder[i] != want[i] {
			t.Errorf("closedOrder[%d] = %q, want %q (full: %v)", i, closedOrder[i], want[i], closedOrder)
		}
	}

	pendingEndpointCleanupsMu.Lock()
	remaining := len(pendingEndpointCleanups)
	pendingEndpointCleanupsMu.Unlock()
	if remaining != 0 {
		t.Errorf("pendingEndpointCleanups not reset after drain: %d entries remain", remaining)
	}
}

// TestDrainEndpointCleanups_EmptyIsNoOp proves a drain with nothing pending is a safe no-op (the
// common case — most verb Invokes never call ResolveEndpoint at all).
func TestDrainEndpointCleanups_EmptyIsNoOp(t *testing.T) {
	resetPendingEndpointCleanups(t)
	if _, err := drainEndpointCleanupsForHost(context.Background(), &pb.InvokeRequest{}); err != nil {
		t.Fatalf("drainEndpointCleanupsForHost: %v", err)
	}
}

// TestDrainEndpointCleanups_SequentialInvokesDoNotLeak proves a SECOND verb's Invoke (its own
// resolve → drain cycle) never sees a leftover cleanup from a PRIOR Invoke that already drained —
// the sequential per-Invoke isolation resolve_endpoint.go's header claims (mirroring the former
// core-side "reset per-Invoke so a leftover from a prior op never leaks in").
func TestDrainEndpointCleanups_SequentialInvokesDoNotLeak(t *testing.T) {
	resetPendingEndpointCleanups(t)
	var calls int

	pendingEndpointCleanupsMu.Lock()
	pendingEndpointCleanups = append(pendingEndpointCleanups, func() { calls++ })
	pendingEndpointCleanupsMu.Unlock()
	if _, err := drainEndpointCleanupsForHost(context.Background(), &pb.InvokeRequest{}); err != nil {
		t.Fatalf("first drainEndpointCleanupsForHost: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls after first drain = %d, want 1", calls)
	}

	// A second Invoke's drain, with nothing newly tracked, must NOT re-fire the first Invoke's
	// (already-drained) cleanup.
	if _, err := drainEndpointCleanupsForHost(context.Background(), &pb.InvokeRequest{}); err != nil {
		t.Fatalf("second drainEndpointCleanupsForHost: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls after second (empty) drain = %d, want still 1 (no leak/re-fire)", calls)
	}
}

// TestImageLabelRefusal pins the two reasons the label lookup refuses, and the fact that they are
// DIFFERENT — the defect in opencharly/plugin-check#96 was reporting both as one false third thing
// ("container for %s is not running") about a container that was up.
func TestImageLabelRefusal(t *testing.T) {
	ctr := func(name string) *CheckVenue {
		return &CheckVenue{Kind: "container", Name: name, Descriptor: spec.VenueDescriptor{Kind: "container"}}
	}
	cases := []struct {
		name      string
		venue     *CheckVenue
		box       string
		mustMatch []string
		mustMiss  []string
	}{
		{"a plain container is readable", ctr("charly-app"), "app", nil, []string{"not a container", "NESTED"}},
		{"a nested container names the limitation, not a missing process",
			ctr("charly-ns_app"), "ns.app", []string{"NESTED", "charly-ns_app", "not readable"}, []string{"is not running"}},
		{"a non-container venue says so", &CheckVenue{Kind: "ssh"}, "app", []string{"not a container"}, []string{"is not running", "NESTED"}},
		{"a nil venue says so too", nil, "app", []string{"not a container"}, []string{"is not running"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := imageLabelRefusal(tc.venue, tc.box)
			for _, want := range tc.mustMatch {
				if !strings.Contains(got, want) {
					t.Errorf("refusal = %q, want it to contain %q", got, want)
				}
			}
			for _, bad := range tc.mustMiss {
				if strings.Contains(got, bad) {
					t.Errorf("refusal = %q, must NOT say %q", got, bad)
				}
			}
		})
	}
}

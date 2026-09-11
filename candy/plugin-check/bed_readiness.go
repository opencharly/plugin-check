package check

// bed_readiness.go — the bed runner's BOUNDED post-restart venue-readiness wait (G-3c).
//
// RCA (routed from eval-omarchy PR #69's validator — the update-bed deadlock): the
// restart-only update_gate power-cycles the bed's venue (`vm stop --force` → `vm start`,
// anchored.go's updateGateSteps) and the bed then RECONNECTS to the rebooted guest. That
// reconnect went through specexec.WaitForVmSshReady — a best-effort gate that is SILENT on
// timeout and drives the readiness poll through poll.WaitCapped(PollRemote, cap=0), i.e. the
// 30-minute readinessAbsoluteCapFallback, with NO progress watchdog and NO caller-visible
// deadline. A rebooted guest whose NetworkManager holds no IPv4 lease (STATE:20 unavailable;
// ssh "timed out during banner exchange") is therefore indistinguishable from a slow one: the
// reconnect never bounds, and the bed stalls its whole suite (observed: the update bed held a
// 16-lane suite 29+ minutes with no summary.yml) instead of failing.
//
// The CONTAINER arm does not share the defect: WaitForContainerReady drives a MONOTONIC poll
// whose 90-second no-progress watchdog early-outs a stalled venue. The VM arm is the cap-only
// one, so the bound below applies to the VM reconnect — the venue class the RCA observed —
// while the container arm keeps its own self-bounding gate.
//
// The deadline is deliberately a POST-RESTART policy, not a general one: the INITIAL connect
// legitimately needs the generous window (a first boot provisions the image — pacman/AUR/
// cloud-init), which is exactly why the bound lives in the bed runner — the only component
// that knows the venue has just been RESTARTED — rather than in the shared check-live
// readiness gate, which cannot tell a reboot from a first boot.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/opencharly/sdk/kit"
)

// rebuildReadyDeadline bounds the post-restart reconnect of a VM bed (the fresh-rebuild
// restart-only gate and the anchored revert-and-start). A rebooted, already-provisioned clone
// reaches SSH readiness in well under a minute, so 5 minutes is ~5x the observed healthy
// reboot — generous enough that a heavily-loaded host running a parallel suite cannot
// misclassify a slow-but-healthy boot as a setup defect, while a lease-less guest fails the
// bed in 1/6 of the 30-minute cap-only window it previously consumed silently.
const rebuildReadyDeadline = 5 * time.Minute

// waitVenueReady runs a venue readiness probe under a TOTAL deadline and turns a deadline
// expiry into a named, time-bounded failure. The probe receives the deadline-bounded context,
// so the underlying readiness poll (spec/poll.PollUntil) returns as soon as the deadline
// elapses — no timer, no retry bomb, no sleep: the bound IS the context deadline. The error
// names the observed setup-defect class rather than reporting a bare timeout, so a lease-less
// guest is diagnosable from the bed's summary.yml alone.
func waitVenueReady(ctx context.Context, deadline time.Duration, venue string, probe func(context.Context) error) error {
	rctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	err := probe(rctx)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("venue %s not ready within %s of the post-restart reconnect: %w — the venue came back but never became reachable; the likely setup defect is a guest booted WITHOUT an IPv4 lease (NetworkManager STATE:20 unavailable, ssh \"timed out during banner exchange\"), not a slow venue", venue, deadline, err)
	default:
		return fmt.Errorf("venue %s readiness probe: %w", venue, err)
	}
}

// vmSshReadyProbe is the VM arm's readiness probe: the canonical SSHExecutor readiness gate
// (sshd answering), bounded per attempt by ConnectTimeout and in TOTAL by waitVenueReady's
// deadline context. WaitForCloudInit is deliberately NOT part of the reconnect condition — it
// is the deploy-time race guard (so deploy-add never races a still-running first-boot pacman),
// which the downstream check-live gate still enforces; requiring it here would spend the whole
// deadline on a settled-by-then signal.
func vmSshReadyProbe(domainID string) func(context.Context) error {
	return func(ctx context.Context) error {
		gate := &kit.SSHExecutor{Host: kit.VmSshAlias(domainID), ConnectTimeout: 5}
		return gate.WaitForSSH(ctx)
	}
}

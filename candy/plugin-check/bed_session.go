package check

// bed_session.go — the check-bed session, now fully plugin-side (#55 W3 B2-full, dissolving the
// former "check-bed" HostBuild seam entirely). Every piece of former core state this session
// managed turned out to be reachable without a host round-trip:
//
//   - the bed flock + per-domain flocks: spec/lock is plugin-importable fabric (not sdk-mechanism
//     private) — a compiled-in plugin can hold the returned unlock funcs itself, same as core did.
//   - the preempt lease: candy/plugin-vm/vm_arbiter_shim.go already proves a plugin reaches
//     verb:arbiter directly via InvokeProvider, bypassing core's former arbiterProxy entirely —
//     mirrored here (arbiterAcquire/arbiterRelease).
//   - the repo-override / deploy-config / preempt-lease vars: carried as EXPLICIT per-invocation
//     data on a spec.RunEnv threaded through ctx (and to forked children via spec.CliRequest.Env),
//     never via os.Setenv — so the session is placement-invariant AND safe under a concurrent
//     in-process roster (plan §4.2 / F8).
//
// No package-level session MAP is needed (unlike the former core session, which had to survive
// separate HostBuild round-trips across a process-reentry boundary): the whole bed run is now ONE
// in-process Go call graph — runCheckBed (bed_run.go) holds the *bedSession its own bedSetup call
// returns in a local variable for the run's whole lifetime, threading it explicitly into
// bedTeardown at the end. Members-up/-down call sdk/deploykit.BringUpMembers/TearDownMembers
// directly (#55 W3 A4) using data already in the caller's *spec.CheckBedReply — no session lookup.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/deploy"
	"github.com/opencharly/spec/hostenv"
	"github.com/opencharly/spec/lock"
	"github.com/opencharly/spec/proc"
	"github.com/opencharly/spec/spec"
)

// envPreemptLeaseHeld is set by the OUTERMOST claim-bringing `charly` invocation (a check-bed run,
// or a standalone `charly vm create`/`charly start`) so the nested `charly` subprocesses it spawns
// do NOT independently acquire/release the lease — the owner manages it. Mirrors the
// identically-named, identically-valued const that lived in charly/preempt.go (deleted, K-wave 2
// cone CONTESTED — the surviving core copy is the op="remove" release chain in
// host_build_pod_lifecycle_dispatch.go; both sides of the SAME process-env contract; not an alias
// — a plugin cannot import charly package main).
const envPreemptLeaseHeld = "CHARLY_PREEMPT_LEASE"

// bedSession holds the live host handles ONE bed run owns across its lifecycle — from bedSetup's
// return to bedTeardown's call, all within the SAME in-process Go call graph.
//
// The per-bed deploy-config dir and repo override are NOT session fields that get
// os.Setenv/os.Unsetenv'd: they live in the run's spec.RunEnv (bedSetup builds it, threads it on
// ctx, and hands children via spec.CliRequest.Env). The session keeps only what teardown must
// physically undo (temp-dir removal) plus the verdict-surfacing override pair.
type bedSession struct {
	bedUnlock func() error
	domUnlock []func() error

	leaseClaimant string // "" ⇒ acquireLease never ran (a GPU-prereq skip returns before it)
	leaseActive   bool

	// runEnv is the bed's per-invocation env map (shared by reference with the run ctx); the
	// preempt-lease marker is set/cleared on it as the lease is acquired/released.
	runEnv     spec.RunEnv
	cfgDir     string // per-bed temp config dir; teardown RemoveAll ("" when an outer override is honored)
	repoOvPair string // the auto-added `<repo>=<dir>` pair, surfaced in the verdict
}

// release unwinds a session's acquired handles in REVERSE order (lease → env → domain locks →
// bed lock). ok controls the lease disposition. Nil-safe on every field so it doubles as the
// bedSetup rollback (release whatever was acquired so far on a later-step failure).
func (s *bedSession) release(ctx context.Context, ex *sdk.Executor, ok bool) {
	if s == nil {
		return
	}
	if s.leaseClaimant != "" && s.leaseActive {
		// Mirrors the deleted charly/preempt.go's Lease.Release/ReleaseFailed early-out on
		// !active — a lease this session never actually claimed (because an outer orchestrator
		// already held it, or the claimant declared no requires_exclusive/requires_shared) needs
		// no release call.
		_ = arbiterRelease(ctx, ex, s.leaseClaimant, ok)
		delete(s.runEnv, envPreemptLeaseHeld)
	}
	if s.cfgDir != "" {
		// An ephemeral registration is TWO artifacts — the state in this overlay and an armed
		// systemd TTL timer — and this teardown used to destroy only the first. The timer then
		// fired later against an overlay that no longer held the entity: it could neither resolve
		// it nor verify its identity, so the VM it was registered to reap leaked permanently and
		// the unit accumulated as a failure nobody reads. Measured 2026-08-15: ten armed units for
		// one reused entity name, all firing at once, none able to reap.
		//
		// Cancel BEFORE removing the state, so a cancellation failure leaves the overlay intact and
		// the timer still able to work — the safe ordering of two operations that must both happen.
		cancelBedEphemeralTimers(ctx, ex)
		_ = os.RemoveAll(s.cfgDir)
	}
	for i := len(s.domUnlock) - 1; i >= 0; i-- {
		_ = s.domUnlock[i]()
	}
	if s.bedUnlock != nil {
		_ = s.bedUnlock()
	}
}

// arbiterInvoke resolves verb:arbiter and Invokes it with an action-tagged input — the SAME
// direct-InvokeProvider(verb,"arbiter") pattern candy/plugin-vm/vm_arbiter_shim.go already proves
// bypasses core's former arbiterProxy entirely.
//
// It is a package var so a test can drive arbiterAcquire/arbiterRelease without a live provider
// (the arbiterAcquire lease-marker path is otherwise only reachable through a real provider).
var arbiterInvoke = func(ctx context.Context, ex *sdk.Executor, in spec.ArbiterInvokeInput) (spec.ArbiterInvokeReply, error) {
	params, err := json.Marshal(in)
	if err != nil {
		return spec.ArbiterInvokeReply{}, err
	}
	out, err := ex.InvokeProvider(ctx, "verb", "arbiter", sdk.OpRun, params, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return spec.ArbiterInvokeReply{}, fmt.Errorf("arbiter %s: %w", in.Action, err)
	}
	var reply spec.ArbiterInvokeReply
	if len(out) > 0 {
		if uerr := json.Unmarshal(out, &reply); uerr != nil {
			return spec.ArbiterInvokeReply{}, uerr
		}
	}
	if reply.Error != "" {
		return spec.ArbiterInvokeReply{}, errors.New(reply.Error)
	}
	return reply, nil
}

// arbiterAcquire acquires the appropriate lease for a claimant: EXCLUSIVE when it declares
// requires_exclusive, otherwise SHARED (a no-op inside the arbiter when the claimant declares no
// requires_shared AND implies no GPU consumption — the arbiter auto-promotes an implied GPU
// consumer, K-wave W3a A2). Skips entirely when an outer orchestrator already owns the lease
// (envPreemptLeaseHeld) — mirrors the deleted charly/preempt.go's acquireResourceForClaimant/
// acquireExclusiveForClaimant/acquireSharedForClaimant/acquireDispatch chain exactly (that chain
// was DEAD in core — zero production callers — and DELETED at K-wave 2 cone CONTESTED; its
// per-caller copies are what survive), restore guarantees included: the arbiter's
// crash-safety/lease-ledger/poison-marker/liveness state lives ENTIRELY plugin-side in
// candy/plugin-preempt already, keyed by Claimant — so `charly preempt restore` reconciles
// identically regardless of which caller acquired the lease, AS LONG AS the ArbiterInvokeInput
// shape matches the former core acquireDispatch field-for-field (verified below:
// Action/Claimant/Tokens/ClaimAddr/Transient/IsPodMember/SecurityDevices, the same field set in
// the same shapes). is_group is left Go-zero since the group-kind cutover (spec #105): a bed
// claimant is always a primary substrate node now — the former targetless group shape cannot
// exist, and the wire field itself is a spec#105 residual kept for plugin-preempt.
func arbiterAcquire(ctx context.Context, ex *sdk.Executor, claimant string, node spec.DeployNode, transient bool, env spec.RunEnv) (active bool, err error) {
	// The lease-held marker is per-invocation data (env), never process env: a roster running
	// many beds in one process must not let bed A's claim suppress bed B's acquire.
	if env[envPreemptLeaseHeld] != "" {
		return false, nil
	}
	action := spec.ArbiterActionAcquireShared
	tokens := spec.DedupeNonEmpty(node.RequiredShared())
	if len(node.RequiredExclusive()) > 0 {
		action = spec.ArbiterActionAcquireExclusive
		tokens = spec.DedupeNonEmpty(node.RequiredExclusive())
	}
	var secDevices []string
	if node.Security != nil {
		secDevices = node.Security.Devices
	}
	r, ierr := arbiterInvoke(ctx, ex, spec.ArbiterInvokeInput{
		Action:          action,
		Claimant:        claimant,
		Tokens:          tokens,
		ClaimAddr:       deploy.HolderAddrFor(claimant, node),
		Transient:       transient,
		IsPodMember:     deploy.IsContainerVenue(&node),
		SecurityDevices: secDevices,
	})
	if ierr != nil {
		return false, ierr
	}
	if r.Active {
		env[envPreemptLeaseHeld] = claimant
	}
	return r.Active, nil
}

// arbiterRelease restores the holders a claimant's lease stopped + removes the lease. ok controls
// the disposition: true → the SAME "Release" (claim succeeded) semantics core's Lease.Release
// applied, false → "ReleaseFailed" (on-success holders stay stopped). The caller (bedSession.
// release) only invokes this when leaseActive is true — a lease this session never actually
// claimed (an outer orchestrator already held it, or the claimant declared neither
// requires_exclusive nor requires_shared) needs no release call, mirroring core's
// Lease.Release/ReleaseFailed early-out on !active.
func arbiterRelease(ctx context.Context, ex *sdk.Executor, claimant string, ok bool) error {
	r, err := arbiterInvoke(ctx, ex, spec.ArbiterInvokeInput{Action: spec.ArbiterActionRelease, Claimant: claimant, Success: ok})
	if err != nil {
		return err
	}
	if r.Error != "" {
		return errors.New(r.Error)
	}
	return nil
}

// bedGpuPrereqCheck reaches the ONE narrow HostBuild seam surviving check-bed's dissolution — GPU
// host-DETECTION is the project's explicitly operator-dropped exception (see
// charly/host_build_check_bed_gpu_prereq.go's header). tokens is the claimant's RAW
// (RequiredExclusive ++ RequiredShared) list — the fenced core function re-dedupes internally.
func bedGpuPrereqCheck(ctx context.Context, ex *sdk.Executor, tokens []string) (missing bool, token, vendor string, err error) {
	reqJSON, err := json.Marshal(spec.CheckBedGpuPrereqRequest{Tokens: tokens})
	if err != nil {
		return false, "", "", err
	}
	out, err := ex.HostBuild(ctx, "check-bed-gpu-prereq", reqJSON)
	if err != nil {
		return false, "", "", err
	}
	var reply spec.CheckBedGpuPrereqReply
	if uerr := json.Unmarshal(out, &reply); uerr != nil {
		return false, "", "", uerr
	}
	return reply.Missing, reply.Token, reply.Vendor, nil
}

// bedCheckLevel resolves the acceptance-depth rung for a bed from its box's authored check_level
// (none → DefaultCheckLevel). VM/local beds carry no box image, so they always run at the default
// rung. Ported from charly/check_bed_run.go — uf.ProjectConfig() is a plain spec.UnifiedFile
// method, no core-only coupling.
func bedCheckLevel(uf *spec.UnifiedFile, node spec.DeployNode) string {
	if node.Image == "" {
		return spec.DefaultCheckLevel
	}
	bc, _, ok := uf.ProjectConfig().ResolveBoxRef(node.Image)
	if !ok {
		return spec.DefaultCheckLevel
	}
	return spec.ResolveCheckLevel(bc.CheckLevel)
}

// bedMemberDescriptors projects a bed root's deploy-level (alongside) members into the descriptor
// the plugin drives its per-member image-build loop from, in AUTHORED tree order (the ordered
// member tree replaces the former sorted map keys). Ported from charly/host_build_check_bed.go,
// using deploy.IsVmVenue instead of the former core-private isVmMember (same Descent-stamped read).
func bedMemberDescriptors(members []*spec.Member) []spec.CheckBedMember {
	var out []spec.CheckBedMember
	for _, m := range members {
		if m.Node == nil {
			continue
		}
		out = append(out, spec.CheckBedMember{Key: m.Name, IsVM: deploy.IsVmVenue(m.Node), Image: m.Node.Image, From: m.Node.From, FromSnapshot: m.Node.FromSnapshot})
	}
	return out
}

// bedRunImageTag is the per-RUN bed-scoped image tag every `charly box build` + deploy step in a
// bed run passes as --tag: <bed-root-name>-<runCalver> (#75). Ported unchanged from
// charly/host_build_check_bed.go — pure string concat, no core coupling.
func bedRunImageTag(bed, calver string) string {
	if bed == "" || calver == "" {
		return ""
	}
	return bed + "-" + calver
}

// bedLocalChildKeys is the HOST-ROOTED (kind:local) subset of a node's in-substrate members, in
// authored tree order (the ordered member tree replaces the former sorted map keys) — the set a
// VM root deploys host-side. Ported from charly/host_build_check_bed.go, using deploy.HostRooted
// instead of the former core-private nodeTraits(child).HostRooted read (same Descent-stamped
// predicate, already promoted #55 U4).
func bedLocalChildKeys(members []*spec.Member) []string {
	var out []string
	for _, m := range members {
		if m.Node != nil && deploy.HostRooted(m.Node) {
			out = append(out, m.Name)
		}
	}
	return out
}

// memberNames projects a member list to its tree keys, in authored order.
func memberNames(members []*spec.Member) []string {
	var out []string
	for _, m := range members {
		out = append(out, m.Name)
	}
	return out
}

// bedSetup opens the bed session — mirroring the former host_build_check_bed.go's
// bedSessionSetup acquire order (GPU-prereq fail-fast, bed flock, per-domain flocks,
// repo-override env, deploy-config isolation, preempt lease, libvirt) — then returns the
// BedDescriptor runCheckBed drives the sequence from. Transactional: any acquire failure rolls
// back every handle taken so far via the returned session's release (the caller must call it on
// any non-nil-session error path too — see bed_run.go's runCheckBed).
// bedSetup opens the bed session — mirroring the former host_build_check_bed.go's
// bedSessionSetup acquire order (GPU-prereq fail-fast, bed flock, per-domain flocks,
// repo-override env, deploy-config isolation, preempt lease, libvirt) — then returns the
// BedDescriptor runCheckBed drives the sequence from. Transactional: any acquire failure rolls
// back every handle taken so far via the returned session's release (the caller must call it on
// any non-nil-session error path too — see bed_run.go's runCheckBed).
//
// ISOLATION IS EXPLICIT DATA, NOT PROCESS ENV (plan §4.2 / F8). The per-bed deploy-config path,
// repo override, and preempt-lease marker are carried on a spec.RunEnv threaded through ctx (and
// to forked children via spec.CliRequest.Env) — NEVER via os.Setenv. os.Setenv is process-global,
// so a roster running many beds as goroutines in one process would let bed A's value be read by
// bed B (and by bed B's children): two concurrent beds then contend on each other's deploy-config
// lock and HANG. The returned ctx is the isolated one; runCheckBed adopts it for the whole run.
func bedSetup(ctx context.Context, ex *sdk.Executor, bed, dir string) (spec.CheckBedReply, *bedSession, context.Context, error) {
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}
	// Build this bed's per-invocation RunEnv. The repo override must be live BEFORE the
	// self-load below (on a fresh cache the pinned @github refs have already failed by the time a
	// later override would apply) — so it is threaded on the load ctx, not set in the process env.
	env := spec.RunEnv{}
	var cfgDir string
	pair := proc.SelfSuperprojectOverridePair(dir)
	if pair != "" {
		// A pre-existing outer override (an operator RDD override) is preserved as the base; the
		// auto superproject pair is merged on top (matching the legacy os.Setenv merge).
		existing := ""
		if v, ok := os.LookupEnv(proc.RepoOverrideEnv); ok {
			existing = v
		}
		env[proc.RepoOverrideEnv] = proc.MergeRepoOverrides(existing, pair)
	}
	// Per-bed deploy-config isolation: a private temp overlay so a disposable run never touches
	// the operator's real ~/.config/charly/charly.yml, and two concurrent beds never share one.
	// An OUTER operator override in the process env (an explicit CHARLY_DEPLOY_CONFIG) is honored
	// as-is — it cannot be a sibling bed's value any more, because beds no longer os.Setenv, so
	// this reads only the operator's own intent. Otherwise this bed gets its own temp overlay.
	if outer, ok := os.LookupEnv(spec.DeployConfigEnv); ok && outer != "" {
		env[spec.DeployConfigEnv] = outer
		cfgDir = "" // no temp dir to own/remove; teardown must not delete the operator's overlay
	} else {
		d, mkErr := os.MkdirTemp("", "charly-bed-cfg-"+bed+"-")
		if mkErr != nil {
			return spec.CheckBedReply{}, nil, nil, fmt.Errorf("check-bed setup: creating per-bed config dir: %w", mkErr)
		}
		cfgDir = d
		env[spec.DeployConfigEnv] = filepath.Join(cfgDir, "charly.yml")
	}
	// The temp dir is created BEFORE the session exists, so every pre-session early return below
	// (a load error, no charly.yml, not-a-bed, a GPU-prereq skip) would leak it: only the session's
	// release (and the bedSession's own cfgDir) removes it. Guard those paths with a cleanup that
	// the session ADOPTS once it owns cfgDir — after that, teardown is the sole remover.
	cfgOwned := false
	defer func() {
		if !cfgOwned && cfgDir != "" {
			_ = os.RemoveAll(cfgDir)
		}
	}()

	// The isolated load ctx: every in-process loader/deploy-config read below resolves THIS bed's
	// values from the ctx RunEnv (spec.DefaultDeployConfigPath(ctx) / spec.RunEnvGet(ctx, …)).
	bedCtx := spec.WithRunEnv(ctx, env)

	uf, ok, err := loaderkit.LoadUnifiedViaExecutor(bedCtx, ex, dir)
	if err != nil {
		return spec.CheckBedReply{}, nil, nil, err
	}
	if !ok || uf == nil {
		return spec.CheckBedReply{}, nil, nil, fmt.Errorf("check-bed setup: no charly.yml in %s", dir)
	}
	// Resolve the bed with every namespace-LOCAL cross-ref (from:/image:, and the
	// same on each member, recursively) rewritten to the ROOT-qualified form. A
	// namespaced bed (`ns.check-foo`) lives in its own namespace, where its bare
	// `image:`/`from:` resolves; the run sequence drives `charly box build <image>`,
	// `charly deploy add <name> <image>`, and `charly vm build <from>` from the
	// project ROOT, where a bare namespaced ref does NOT resolve (measured:
	// `charly.check-sidecar-pod` failed `box build` with `unknown box
	// "check-k8s-deploy-app"`). ResolveBedForRoot is the ONE qualifier — a local bed
	// (no namespace) is returned unchanged. The copy leaves the stored tree intact,
	// so `charly box validate` still reads the authored refs.
	node, isBed := uf.ResolveBedForRoot(bed)
	if !isBed {
		return spec.CheckBedReply{}, nil, nil, fmt.Errorf("check-bed setup: %q is not a disposable check bed", bed)
	}

	// CalVer and logDir are single-sourced for both normal runs and prerequisite skips.
	calver := spec.ComputeCalVer()
	logDir := filepath.Join(".check", bed, calver)

	// Host-prerequisite fail-fast (BEFORE any acquire): a clean SKIP (exit 3), not a failure.
	// Acquires NOTHING, so no session handles exist and no teardown is needed.
	tokens := append(append([]string{}, node.RequiredExclusive()...), node.RequiredShared()...)
	if missing, tok, vendor, gerr := bedGpuPrereqCheck(ctx, ex, tokens); gerr == nil && missing {
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			return spec.CheckBedReply{}, nil, nil, fmt.Errorf("creating %s: %w", logDir, err)
		}
		return spec.CheckBedReply{
			Calver: calver,
			LogDir: logDir,
			PrereqSkip: &spec.CheckBedPrereqSkip{
				Token:  tok,
				Vendor: vendor,
				Reason: fmt.Sprintf("no GPU matching vendor %s on this host (bed requires resource %q)", vendor, tok),
			},
		}, nil, bedCtx, nil
	}

	bedDomain := spec.VmDomainIdentity(bed)
	imageTag := bedRunImageTag(bed, calver)
	s := &bedSession{runEnv: env, cfgDir: cfgDir}
	cfgOwned = true // the session now owns the temp dir; its release removes it
	if pair != "" {
		s.repoOvPair = pair
		fmt.Fprintf(os.Stderr, "charly check run %s: testing LOCAL candies (%s += %s)\n", bed, proc.RepoOverrideEnv, pair)
	}
	rolledBack := false
	rollback := func() {
		if !rolledBack {
			rolledBack = true
			s.release(ctx, ex, true) // clean rollback — the bed never ran
		}
	}

	// Per-bed exclusive lock — fail-fast on a duplicate concurrent run of the SAME bed, from ANY
	// project directory. The key is user-scoped (bed_lock.go): what a second run would steal is the
	// first run's CONTAINERS AND VOLUMES in the shared podman store, not anything under the project
	// directory, so a project-relative key let two runs of one bed proceed side by side and reclaim
	// each other's containers (measured: check run 2026.255.2323 — green through [start], then its
	// pod container gone before the probes ran: 67 probes at exit-125, 28 lines carrying
	// `container state improper` (15 lines carry exit=255), and
	// nothing in that run attributing the disappearance to itself).
	bedLock, bedLockErr := bedRunLockPath(bed)
	if bedLockErr != nil {
		rollback()
		return spec.CheckBedReply{}, nil, nil, fmt.Errorf("resolving the check bed lock for %q: %w", bed, bedLockErr)
	}
	bedUnlock, lockErr := lock.AcquireFileLock(bedLock, false)
	if lockErr != nil {
		rollback()
		if errors.Is(lockErr, lock.ErrLockBusy) {
			return spec.CheckBedReply{}, nil, nil, fmt.Errorf("check bed %q is already running — refusing a concurrent run: another charly process holds the bed lock %s. Two runs of one bed share the container/volume names of its deploy identity in the podman store, so the second would reclaim the first run's containers and volumes instead of testing them. Wait for that run to finish, or stop it: charly check stop %s", bed, bedLock, bed)
		}
		return spec.CheckBedReply{}, nil, nil, fmt.Errorf("locking check bed %q: %w", bed, lockErr)
	}
	s.bedUnlock = bedUnlock
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		rollback()
		return spec.CheckBedReply{}, nil, nil, fmt.Errorf("creating %s: %w", logDir, err)
	}

	// Per-DOMAIN serialization for VM beds (sorted → no deadlock across a multi-domain bed).
	domains := lock.BedVmDomains(bed, node)
	for _, domain := range domains {
		du, derr := lock.AcquireVmDomainLock(domain)
		if derr != nil {
			rollback()
			return spec.CheckBedReply{}, nil, nil, fmt.Errorf("locking vm domain %s for bed %q: %w", domain, bed, derr)
		}
		s.domUnlock = append(s.domUnlock, du)
	}

	// Isolate this bed's EPHEMERAL deploy state to its per-bed config file (cfgDir, created up
	// front and threaded on the run env/ctx) so CONCURRENT beds never share the operator's
	// ~/.config/charly/charly.yml, nor one another's overlay.

	// Resource arbitration (the preemptible axis): acquire a lease for the bed's requires_exclusive
	// / requires_shared claim.
	active, lerr := arbiterAcquire(bedCtx, ex, bed, node, true, env)
	if lerr != nil {
		rollback()
		return spec.CheckBedReply{}, nil, nil, fmt.Errorf("acquiring resources for %s: %w", bed, lerr)
	}
	s.leaseClaimant = bed
	s.leaseActive = active

	isVM := deploy.IsVmVenue(&node)
	isLocal := deploy.HostRooted(&node)
	isExternal := deploy.ExternalInPlaceVenue(&node)

	// VM beds need the libvirt user-session daemon (probes + the backend resolver). Best-effort.
	// (The former group arm is gone with the group kind — spec #105; a bed root is always a
	// primary substrate node now, and only the VM substrate needs the session daemon.)
	if isVM {
		hostenv.StartLibvirtUserSession()
	}

	level := bedCheckLevel(uf, node)
	nodeJSON, _ := json.Marshal(node)
	return spec.CheckBedReply{
		Calver:         calver,
		LogDir:         logDir,
		IsVM:           isVM,
		IsLocal:        isLocal,
		IsExternal:     isExternal,
		NodeJSON:       nodeJSON,
		Image:          node.Image,
		HasAddCandy:    len(node.AddCandy) > 0,
		VMTemplate:     node.From,
		FromSnapshot:   node.FromSnapshot,
		BedDomain:      bedDomain,
		ImageTag:       imageTag,
		LocalRef:       node.From,
		VMDomains:      domains,
		CheckLiveRefs:  deploy.BedCheckLiveRefs(bed, &node),
		ChildKeys:      memberNames(node.InSubstrateMembers()),
		LocalChildKeys: bedLocalChildKeys(node.InSubstrateMembers()),
		Members:        bedMemberDescriptors(node.DeployLevelMembers()),
		RunBuild:       spec.CheckLevelReaches(level, spec.CheckLevelBuild),
		RunRuntime:     spec.CheckLevelReaches(level, spec.CheckLevelNoAgent),
		RunAgent:       spec.CheckLevelReaches(level, spec.CheckLevelAgent),
	}, s, bedCtx, nil
}

// bedTeardown closes the bed session — releasing every handle in reverse order. Idempotent-safe
// via bedSession.release's nil-checks; ok controls the preempt-lease disposition.
func bedTeardown(ctx context.Context, ex *sdk.Executor, sess *bedSession, ok bool) {
	sess.release(ctx, ex, ok)
}

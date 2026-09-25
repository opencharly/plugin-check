package check

// bed_session_env_test.go — the placement-invariance / roster-isolation contract (plan §4.2 /
// F8). The defect this closes: bed runs isolated themselves with process-global os.Setenv, so a
// concurrent in-process roster let bed A's CHARLY_DEPLOY_CONFIG / CHARLY_REPO_OVERRIDE reach bed
// B (and bed B's forked children) — two beds then contended on one deploy-config lock and hung.
// The fix carries the values as EXPLICIT per-invocation data on ctx (spec.RunEnv) and threads
// them to children via spec.CliRequest.Env. These tests pin the data path, not the live bed.

import (
	"context"
	"testing"

	"github.com/opencharly/spec/proc"
	"github.com/opencharly/spec/spec"
)

// TestChildEnvForReq_RunEnvThreadedToChild proves a bed's forked child receives the SAME explicit
// env the bed carries on its ctx — never an inherited process value.
func TestChildEnvForReq_RunEnvThreadedToChild(t *testing.T) {
	t.Setenv(spec.DeployConfigEnv, "/process/real.yml") // the operator's real overlay, must NOT leak
	env := spec.RunEnv{
		spec.DeployConfigEnv: "/tmp/bed-a/charly.yml",
		proc.RepoOverrideEnv: "o/r=/local/a",
	}
	ctx := spec.WithRunEnv(context.Background(), env)
	got := childEnvForReq(ctx, nil)
	if got[spec.DeployConfigEnv] != "/tmp/bed-a/charly.yml" {
		t.Fatalf("child env deploy-config = %q, want the bed's /tmp/bed-a/charly.yml", got[spec.DeployConfigEnv])
	}
	if got[proc.RepoOverrideEnv] != "o/r=/local/a" {
		t.Fatalf("child env repo-override = %q, want o/r=/local/a", got[proc.RepoOverrideEnv])
	}
	if got[spec.DeployConfigEnv] == "/process/real.yml" {
		t.Fatal("child env leaked the process-global deploy-config")
	}
}

// TestChildEnvForReq_ConcurrentBedsIsolated is the headline regression: two beds' ctx values are
// read concurrently and each child sees ONLY its own bed's env. A shared-process os.Setenv could
// never pass this — it is the exact cross-bed contamination that deadlocked the full roster.
func TestChildEnvForReq_ConcurrentBedsIsolated(t *testing.T) {
	bedEnv := func(bed string) context.Context {
		return spec.WithRunEnv(context.Background(), spec.RunEnv{
			spec.DeployConfigEnv: "/tmp/" + bed + "/charly.yml",
			proc.RepoOverrideEnv: "o/r=/local/" + bed,
		})
	}
	ctxA, ctxB := bedEnv("bed-a"), bedEnv("bed-b")

	const n = 200
	errs := make(chan string, n*2)
	done := make(chan struct{})
	for i := 0; i < n; i++ {
		go func() {
			if got := childEnvForReq(ctxA, nil)[spec.DeployConfigEnv]; got != "/tmp/bed-a/charly.yml" {
				errs <- "bed-a saw " + got
			}
			done <- struct{}{}
		}()
		go func() {
			if got := childEnvForReq(ctxB, nil)[spec.DeployConfigEnv]; got != "/tmp/bed-b/charly.yml" {
				errs <- "bed-b saw " + got
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < n*2; i++ {
		<-done
	}
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}

// TestChildEnvForReq_NoRunEnvInherits proves the legacy single-invocation path is unchanged: a ctx
// with no RunEnv passes no explicit env, so os/exec inherits the process environment verbatim.
func TestChildEnvForReq_NoRunEnvInherits(t *testing.T) {
	if got := childEnvForReq(context.Background(), nil); got != nil {
		t.Fatalf("no RunEnv should yield a nil child env (inherit), got %v", got)
	}
}

// TestChildEnvForReq_ExplicitWins proves a caller-provided map is authoritative and is not
// overwritten by the ctx RunEnv.
func TestChildEnvForReq_ExplicitWins(t *testing.T) {
	ctx := spec.WithRunEnv(context.Background(), spec.RunEnv{spec.DeployConfigEnv: "/ctx.yml"})
	got := childEnvForReq(ctx, map[string]string{spec.DeployConfigEnv: "/explicit.yml"})
	if got[spec.DeployConfigEnv] != "/explicit.yml" {
		t.Fatalf("explicit env = %q, want /explicit.yml (the ctx RunEnv must not override it)", got[spec.DeployConfigEnv])
	}
}

// TestArbiterLeaseMarkerIsPerInvocation proves the preempt-lease marker is per-invocation DATA,
// not process env: bed A's claim sets it on bed A's map only, so bed B still acquires its own
// lease. A process-global marker made bed A's claim suppress bed B's acquire (or vice versa).
func TestArbiterLeaseMarkerIsPerInvocation(t *testing.T) {
	a := spec.RunEnv{}
	b := spec.RunEnv{}
	a[envPreemptLeaseHeld] = "claimant-a"
	if b[envPreemptLeaseHeld] != "" {
		t.Fatal("bed B's lease marker was contaminated by bed A's claim")
	}
	delete(a, envPreemptLeaseHeld)
	if b[envPreemptLeaseHeld] != "" {
		t.Fatal("releasing bed A's claim altered bed B's marker")
	}
}

package check

// roster_helpers.go — small host-free helpers for the check-roster runner.

import (
	"context"
	"runtime"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/loaderkit"
)

// numCPU reports the host CPU count (isolated here so roster.go stays testable
// without importing runtime at its call sites).
func numCPU() int { return runtime.NumCPU() }

// isRosterEntity reports whether name is a `check-roster` entity in the project.
// Best-effort: a load failure returns false (the caller then takes the bed/iterate
// path, which reports the real error). Reads PluginKinds["check-roster"] from the
// plugin-side loader — the SAME load the roster runner uses.
func isRosterEntity(ex *sdk.Executor, ctx context.Context, name, dir string) bool {
	if ex == nil {
		return false
	}
	uf, ok, err := loaderkit.LoadUnifiedViaExecutor(ctx, ex, dir)
	if err != nil || !ok || uf == nil {
		return false
	}
	_, isRoster := uf.PluginKinds["check-roster"][name]
	return isRoster
}

package check

// roster_helpers.go — small host-free helpers for the check-roster runner.

import (
	"bufio"
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/loaderkit"
)

// numCPU reports the host CPU count (isolated here so roster.go stays testable
// without importing runtime at its call sites).
func numCPU() int { return runtime.NumCPU() }

// rosterLaneMemBudget is the per-lane host-memory allowance the roster sizes its
// concurrency against. Measured on a 61 GB host: a 128-bed roster at lanes:16
// reached `memory.peak` 59 GB and was OOM-killed (cgroup `oom_kill` 14) — ~3.7 GB
// per concurrently-building lane (each lane can hold a buildah tree + a VM qcow2
// copy). This budget is that measurement with headroom; a lane is a whole bed
// (build + deploy + checks) at once, so the ceiling must be conservative.
const rosterLaneMemBudget = 6 << 30 // 6 GiB

// memTotalBytes reads the host's total RAM from /proc/meminfo (Linux). Returns
// (0,false) when unavailable (non-Linux), so callers fall back to CPU-only sizing.
func memTotalBytes() (uint64, bool) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		// `MemTotal:       65210112 kB`
		if !strings.HasPrefix(sc.Text(), "MemTotal:") {
			continue
		}
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			break
		}
		kb, perr := strconv.ParseUint(fields[1], 10, 64)
		if perr != nil {
			break
		}
		return kb * 1024, true
	}
	return 0, false
}

// clampRosterLanes bounds a roster's concurrency so it cannot OOM the host. An
// explicit `lanes:` in the roster body is respected as an UPPER bound, but never
// above what host RAM can hold at rosterLaneMemBudget per lane; the CPU count and
// bed count are also upper bounds. The result is at least 1.
//
// Root cause (plan RCA-I): lanes:16 OOM-killed a 61 GB host during a 128-bed roster
// (peak 59 GB, oom_kill 14), while lanes:2 was stable at ~15 GB. Sizing by memory,
// not just CPUs, is what makes "run all beds" safe on a real workstation.
func clampRosterLanes(requested, bedCount int) int {
	lanes := requested
	if lanes < 1 {
		lanes = defaultRosterLanes(bedCount)
	}
	if cpus := numCPU(); lanes > cpus {
		lanes = cpus
	}
	if lanes > bedCount {
		lanes = bedCount
	}
	if total, ok := memTotalBytes(); ok && total > 0 {
		if byMem := int(total / rosterLaneMemBudget); byMem > 0 && lanes > byMem {
			lanes = byMem
		}
	}
	if lanes < 1 {
		lanes = 1
	}
	return lanes
}

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

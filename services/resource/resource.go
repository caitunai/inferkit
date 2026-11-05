// Package resource discovers the CPU and memory budget available to this process.
package resource

import (
	"errors"
	"runtime"
	"runtime/debug"

	"github.com/rs/zerolog/log"
)

const fallbackMemory = 512 << 20

// ErrProbe identifies unavailable operating-system resource information.
var ErrProbe = errors.New("read process resource budget failed")

// Budget describes effective CPU parallelism and a total memory ceiling.
// Memory includes native allocations, unlike the Go heap's memory limit.
type Budget struct {
	Memory int64
	CPUs   int
}

// Detect respects GOMAXPROCS, CPU affinity, Linux cgroup quotas and memory limits.
// The caller snapshots this budget when creating native model sessions.
func Detect() Budget {
	b, err := platformBudget()
	if err != nil {
		log.Debug().Err(err).Msg("using fallback process memory budget")
	}
	b.CPUs = max(1, min(runtime.NumCPU(), runtime.GOMAXPROCS(0), positiveCPU(b.CPUs)))
	if b.Memory <= 0 {
		b.Memory = fallbackMemory
	}
	if goLimit := debug.SetMemoryLimit(-1); goLimit > 0 {
		b.Memory = min(b.Memory, goLimit)
	}
	return b
}

func positiveCPU(cpus int) int {
	if cpus < 1 {
		return int(^uint(0) >> 1)
	}
	return cpus
}

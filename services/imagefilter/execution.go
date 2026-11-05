package imagefilter

import (
	"github.com/caitun/inferkit/services/imagepipeline"
	"github.com/caitun/inferkit/services/resource"
)

const (
	modelMemoryReserve int64 = 512 << 20
	// Includes resize intermediates and a native activation allowance per run.
	runMemoryReserve int64 = 64 << 20
	// Reserve a small shared operator pool while leaving most CPUs for callers.
	callersPerOperatorThread = 8
	operatorSpinMicros       = 1000
)

type executionPlan struct {
	memory           int64
	cpus             int
	threads          int
	concurrency      int
	requests         int
	spinMicros       int
	dynamicBlockBase int
}

// automaticPlan budgets concurrent callers together with the one shared ORT
// operator pool. A session with N threads owns N-1 additional worker threads.
func automaticPlan(c Config, budget resource.Budget) executionPlan {
	cpus := max(1, budget.CPUs)
	available := max(1, budget.Memory/2-modelMemoryReserve)
	encoded := max(1, 2*c.MaxBytes)
	working := c.MaxPixels*imagepipeline.WorkingBytesPerPixel + inputLength*4 + runMemoryReserve
	operatorThreads := min(cpus, max(2, (cpus-1)/callersPerOperatorThread+1))
	callerBudget := cpus - operatorThreads + 1
	workers := int(max(1, min(int64(callerBudget), available/max(1, working+2*encoded))))
	remaining := max(0, available-int64(workers)*(working+encoded))
	waiting := int(max(1, min(int64(cpus), remaining/encoded)))
	plan := executionPlan{memory: budget.Memory, cpus: cpus, threads: cpus - workers + 1, concurrency: workers, requests: workers + waiting}
	if plan.threads > 1 {
		plan.spinMicros = operatorSpinMicros
	}
	return plan
}

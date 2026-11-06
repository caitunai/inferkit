package imagefilter

import (
	"testing"

	"github.com/spf13/viper"

	"github.com/caitun/inferkit/services/resource"
)

func TestAutomaticExecutionBudget(t *testing.T) {
	t.Parallel()
	c := Configuration(viper.New())
	for _, expected := range [][3]int{{1, 1, 1}, {2, 2, 1}, {4, 4, 1}, {10, 4, 7}, {32, 4, 29}, {128, 16, 113}} {
		cpus, threads, callers := expected[0], expected[1], expected[2]
		plan := automaticPlan(c, resource.Budget{CPUs: cpus, Memory: 512 << 30})
		if plan.cpus != cpus || plan.concurrency != callers || plan.threads != threads || plan.requests != callers+cpus {
			t.Fatalf("CPU capacity did not scale: %+v", plan)
		}
	}
}

func TestAutomaticExecutionMemoryBudget(t *testing.T) {
	t.Parallel()
	c := Configuration(viper.New())
	for _, memory := range []int64{512 << 20, 4 << 30} {
		plan := automaticPlan(c, resource.Budget{CPUs: 32, Memory: memory})
		if plan.concurrency < 1 || plan.concurrency >= plan.cpus || plan.threads <= 1 || plan.threads+plan.concurrency-1 > plan.cpus {
			t.Fatalf("memory or CPU limit ignored: %+v", plan)
		}
		if plan.requests <= plan.concurrency || plan.requests > plan.concurrency+plan.cpus {
			t.Fatalf("invalid admission: %+v", plan)
		}
	}
}

func TestLegacyExecutionKeysAreIgnored(t *testing.T) {
	t.Parallel()
	v := viper.New()
	want := Configuration(v)
	for _, key := range []string{"concurrency", "maxRequests"} {
		v.Set("image.filter."+key, -1)
	}
	if got := Configuration(v); got != want {
		t.Fatalf("legacy keys still affect execution: %+v", got)
	}
}

func TestExplicitThreadsShareCPUAndMemoryBudget(t *testing.T) {
	t.Parallel()
	c := Configuration(viper.New())
	c.Threads = 4
	for _, cpus := range []int{1, 2, 10, 32} {
		for _, memory := range []int64{1 << 30, 512 << 30} {
			plan := automaticPlan(c, resource.Budget{CPUs: cpus, Memory: memory})
			if plan.threads != min(cpus, 4) || plan.threads+plan.concurrency-1 > cpus {
				t.Fatalf("explicit threads or caller budget ignored: %+v", plan)
			}
		}
	}
}

func TestExecutionConfiguration(t *testing.T) {
	t.Parallel()
	for _, value := range []any{automaticThreads, 0, 4, -1, "invalid"} {
		v := viper.New()
		v.Set("image.filter.threads", value)
		v.Set("image.filter.model", "test-model")
		c := Configuration(v)
		wantValid := value == automaticThreads || value == 0 || value == 4
		if (c.validate() == nil) != wantValid {
			t.Fatalf("incorrect validation for %v: %+v", value, c)
		}
	}
	v := viper.New()
	v.Set("image.filter.warmupRuns", 0)
	v.Set("image.filter.coremlCache", false)
	if c := Configuration(v); c.WarmupRuns != 0 || c.CoreMLCache {
		t.Fatal("startup optimization rollback settings ignored")
	}
}

func TestCoreMLKeepsSmallAutomaticPool(t *testing.T) {
	t.Parallel()
	c := Configuration(viper.New())
	c.Provider = coreMLProvider
	budget := resource.Budget{CPUs: 10, Memory: 32 << 30}
	plan := automaticPlan(c, budget)
	if plan.threads != 2 || plan.concurrency != 9 {
		t.Fatalf("CPU heuristic changed CoreML resources: %+v", plan)
	}
	c.Threads = 4
	plan = automaticPlan(c, budget)
	if plan.threads != 4 || plan.concurrency != 7 {
		t.Fatalf("explicit CoreML thread budget ignored: %+v", plan)
	}
}

func TestWarmupConfigurationBounds(t *testing.T) {
	t.Parallel()
	for _, value := range []any{-1, 11, "invalid"} {
		v := viper.New()
		v.Set("image.filter.model", "test-model")
		v.Set("image.filter.warmupRuns", value)
		if c := Configuration(v); c.validate() == nil {
			t.Fatalf("invalid startup work accepted: %v", value)
		}
	}
}

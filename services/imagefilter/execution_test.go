package imagefilter

import (
	"testing"

	"github.com/spf13/viper"

	"github.com/caitun/inferkit/services/resource"
)

func TestAutomaticExecutionBudget(t *testing.T) {
	t.Parallel()
	c := Configuration(viper.New())
	for _, expected := range [][3]int{{1, 1, 1}, {2, 2, 1}, {4, 2, 3}, {10, 2, 9}, {32, 4, 29}, {128, 16, 113}} {
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
		if plan.concurrency < 1 || plan.concurrency >= plan.cpus || plan.threads <= 1 || plan.threads+plan.concurrency-1 != plan.cpus {
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
	for _, key := range []string{"threads", "concurrency", "maxRequests"} {
		v.Set("image.filter."+key, -1)
	}
	if got := Configuration(v); got != want {
		t.Fatalf("legacy keys still affect execution: %+v", got)
	}
}

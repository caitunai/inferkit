package resource

import (
	"errors"
	"testing"
	"testing/fstest"
)

func kernelFS(files map[string]string) fstest.MapFS {
	result := fstest.MapFS{}
	for name, data := range files {
		result[name] = &fstest.MapFile{Data: []byte(data)}
	}
	return result
}

func TestCgroupV2HierarchyAndFractionalCPU(t *testing.T) {
	t.Parallel()
	system := kernelFS(map[string]string{
		meminfoPath:                             "MemTotal: 16777216 kB\n",
		cgroupPath:                              "0::/work/service\n",
		mountinfoPath:                           "30 20 0:27 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n",
		"sys/fs/cgroup/memory.max":              "max\n",
		"sys/fs/cgroup/cpu.max":                 "max 100000\n",
		"sys/fs/cgroup/work/memory.max":         "1073741824\n",
		"sys/fs/cgroup/work/cpu.max":            "150000 100000\n",
		"sys/fs/cgroup/work/service/memory.max": "2147483648\n",
		"sys/fs/cgroup/work/service/cpu.max":    "50000 100000\n",
	})
	got, err := linuxBudget(system)
	if err != nil {
		t.Fatal(err)
	}
	if got.CPUs != 1 || got.Memory != 1<<30 {
		t.Fatalf("parent limits or fractional quota ignored: %+v", got)
	}
}

func TestCgroupV1SplitControllersAndMountRoot(t *testing.T) {
	t.Parallel()
	system := kernelFS(map[string]string{
		meminfoPath:                   "MemTotal: 16777216 kB\n",
		cgroupPath:                    "4:cpu,cpuacct:/docker/task/service\n5:memory:/docker/task/service\n",
		mountinfoPath:                 "30 20 0:27 /docker/task /custom/cpu rw - cgroup cpu rw,cpu,cpuacct\n31 20 0:28 /docker/task /custom/memory rw - cgroup memory rw,memory\n",
		"custom/cpu/cpu.cfs_quota_us": "200000\n", "custom/cpu/cpu.cfs_period_us": "100000\n",
		"custom/cpu/service/cpu.cfs_quota_us": "-1\n", "custom/cpu/service/cpu.cfs_period_us": "100000\n",
		"custom/memory/memory.limit_in_bytes":         "2147483648\n",
		"custom/memory/service/memory.limit_in_bytes": "9223372036854771712\n",
	})
	got, err := linuxBudget(system)
	if err != nil {
		t.Fatal(err)
	}
	if got.CPUs != 2 || got.Memory != 2<<30 {
		t.Fatalf("v1 parent limits ignored: %+v", got)
	}
}

func TestCgroupNamespaceAndEscapedMount(t *testing.T) {
	t.Parallel()
	system := kernelFS(map[string]string{
		meminfoPath:               "MemTotal: 8388608 kB\n",
		cgroupPath:                "0::/\n",
		mountinfoPath:             `30 20 0:27 /docker/task /custom\040group rw - cgroup2 cgroup rw`,
		"custom group/memory.max": "536870912\n", "custom group/cpu.max": "100000 100000\n",
	})
	got, err := linuxBudget(system)
	if err != nil {
		t.Fatal(err)
	}
	if got.CPUs != 1 || got.Memory != 512<<20 {
		t.Fatalf("namespace mount root ignored: %+v", got)
	}
}

func TestHostBudgetWithoutCgroupAndMalformedData(t *testing.T) {
	t.Parallel()
	got, err := linuxBudget(kernelFS(map[string]string{meminfoPath: "MemTotal: 8388608 kB\n"}))
	if err != nil || got.Memory != 8<<30 || got.CPUs != 0 {
		t.Fatalf("host budget: %+v %v", got, err)
	}
	for _, memory := range []string{"", "MemTotal: -1 kB", "MemTotal: 9223372036854775807 kB", "MemTotal: 100 GB"} {
		if _, err = linuxBudget(kernelFS(map[string]string{meminfoPath: memory})); !errors.Is(err, ErrProbe) {
			t.Fatalf("malformed memory accepted: %q %v", memory, err)
		}
	}
	if _, err = linuxBudget(fstest.MapFS{}); !errors.Is(err, ErrProbe) {
		t.Fatalf("missing procfs: %v", err)
	}
}

func TestDetectedBudgetIsUsable(t *testing.T) {
	b := Detect()
	if b.CPUs < 1 || b.Memory <= 0 {
		t.Fatalf("invalid detected budget: %+v", b)
	}
	t.Logf("CPU=%d memory=%d", b.CPUs, b.Memory)
}

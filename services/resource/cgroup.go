package resource

import (
	"errors"
	"io/fs"
	"math"
	"path"
	"strconv"
	"strings"
)

const (
	controllerMemory = "memory"
	controllerCPU    = "cpu"
	unifiedCgroup    = "cgroup2"
	meminfoPath      = "proc/meminfo"
	cgroupPath       = "proc/self/cgroup"
	mountinfoPath    = "proc/self/mountinfo"
)

type cgroupMount struct{ root, base, controller string }

func linuxBudget(system fs.FS) (Budget, error) {
	data, err := fs.ReadFile(system, meminfoPath)
	if err != nil {
		return Budget{}, errors.Join(ErrProbe, err)
	}
	b := Budget{Memory: memoryTotal(string(data))}
	groups := readOptional(system, cgroupPath)
	mounts := readOptional(system, mountinfoPath)
	paths := cgroupPaths(string(groups))
	for _, mount := range cgroupMounts(string(mounts)) {
		group, ok := paths[mount.controller]
		if !ok {
			continue
		}
		applyHierarchy(system, mount, group, &b)
	}
	if b.Memory <= 0 {
		return b, ErrProbe
	}
	return b, nil
}

func memoryTotal(data string) int64 {
	for line := range strings.SplitSeq(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] != "MemTotal:" || fields[2] != "kB" {
			continue
		}
		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || value <= 0 || value > math.MaxInt64/1024 {
			return 0
		}
		return value * 1024
	}
	return 0
}

func cgroupPaths(data string) map[string]string {
	result := make(map[string]string)
	for line := range strings.SplitSeq(data, "\n") {
		fields := strings.SplitN(line, ":", 3)
		if len(fields) != 3 || !strings.HasPrefix(fields[2], "/") {
			continue
		}
		registerControllers(result, fields[1], fields[2])
	}
	return result
}

func cgroupMounts(data string) []cgroupMount {
	result := make([]cgroupMount, 0)
	for line := range strings.SplitSeq(data, "\n") {
		before, after, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		left, right := strings.Fields(before), strings.Fields(after)
		if len(left) < 6 || len(right) < 3 {
			continue
		}
		result = append(result, mountControllers(left[3], left[4], right)...)
	}
	return result
}

func mountControllers(root, base string, fields []string) []cgroupMount {
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	root, base = unescape.Replace(root), unescape.Replace(base)
	if fields[0] == unifiedCgroup {
		return []cgroupMount{{root: root, base: base, controller: unifiedCgroup}}
	}
	if fields[0] != "cgroup" {
		return nil
	}
	result := make([]cgroupMount, 0, 2)
	for controller := range strings.SplitSeq(fields[2], ",") {
		if controller == controllerCPU || controller == controllerMemory {
			result = append(result, cgroupMount{root: root, base: base, controller: controller})
		}
	}
	return result
}

func applyHierarchy(system fs.FS, mount cgroupMount, group string, budget *Budget) {
	current := mount.base
	root := strings.TrimRight(mount.root, "/")
	if group == root || strings.HasPrefix(group, root+"/") {
		current = path.Join(mount.base, strings.TrimPrefix(strings.TrimPrefix(group, root), "/"))
	}
	base := path.Clean(mount.base)
	for current == base || strings.HasPrefix(current, base+"/") {
		applyGroup(system, current, mount.controller, budget)
		if current == base {
			break
		}
		current = path.Dir(current)
	}
}

func applyGroup(system fs.FS, directory, controller string, budget *Budget) {
	if controller == unifiedCgroup {
		budget.Memory = smallerMemory(budget.Memory, readPositive(system, directory, "memory.max"))
		data := readOptional(system, strings.TrimPrefix(path.Join(directory, "cpu.max"), "/"))
		fields := strings.Fields(string(data))
		if len(fields) == 2 {
			applyQuota(budget, positiveInteger(fields[0]), positiveInteger(fields[1]))
		}
		return
	}
	if controller == controllerMemory {
		budget.Memory = smallerMemory(budget.Memory, readPositive(system, directory, "memory.limit_in_bytes"))
		return
	}
	applyQuota(budget, readPositive(system, directory, "cpu.cfs_quota_us"), readPositive(system, directory, "cpu.cfs_period_us"))
}

func applyQuota(budget *Budget, quota, period int64) {
	if quota <= 0 || period <= 0 {
		return
	}
	cpus := int(min((quota-1)/period+1, int64(math.MaxInt)))
	if budget.CPUs == 0 || cpus < budget.CPUs {
		budget.CPUs = cpus
	}
}

func readPositive(system fs.FS, directory, name string) int64 {
	data := readOptional(system, strings.TrimPrefix(path.Join(directory, name), "/"))
	return positiveInteger(strings.TrimSpace(string(data)))
}

func positiveInteger(value string) int64 {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func smallerMemory(current, limit int64) int64 {
	if limit > 0 && (current <= 0 || limit < current) {
		return limit
	}
	return current
}

func registerControllers(result map[string]string, controllers, group string) {
	if controllers == "" {
		result[unifiedCgroup] = group
		return
	}
	for controller := range strings.SplitSeq(controllers, ",") {
		if controller == controllerCPU || controller == controllerMemory {
			result[controller] = group
		}
	}
}

func readOptional(system fs.FS, name string) []byte {
	data, err := fs.ReadFile(system, name)
	if err != nil {
		return nil
	}
	return data
}

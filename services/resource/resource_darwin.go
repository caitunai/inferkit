//go:build darwin

package resource

import (
	"errors"
	"math"

	"golang.org/x/sys/unix"
)

func platformBudget() (Budget, error) {
	memory, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return Budget{}, errors.Join(ErrProbe, err)
	}
	return Budget{Memory: int64(min(memory, math.MaxInt64))}, nil
}

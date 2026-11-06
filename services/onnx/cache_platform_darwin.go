package onnx

import (
	"errors"

	"golang.org/x/sys/unix"
)

func compilationCachePlatform() (string, error) {
	build, err := unix.Sysctl("kern.osversion")
	if err != nil {
		return "", errors.Join(ErrCache, err)
	}
	cpu, err := unix.Sysctl("machdep.cpu.brand_string")
	if err != nil {
		return "", errors.Join(ErrCache, err)
	}
	return build + "/" + cpu, nil
}

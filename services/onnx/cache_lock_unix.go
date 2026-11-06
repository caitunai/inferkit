//go:build linux || darwin

package onnx

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockCacheFile(root *os.Root, name string, exclusive bool) (*os.File, error) {
	file, err := root.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, errors.Join(ErrCache, err)
	}
	mode := unix.LOCK_SH | unix.LOCK_NB
	if exclusive {
		mode = unix.LOCK_EX | unix.LOCK_NB
	}
	if err = unix.Flock(int(file.Fd()), mode); err != nil {
		return nil, errors.Join(ErrCache, err, file.Close())
	}
	return file, nil
}

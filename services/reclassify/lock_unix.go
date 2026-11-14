//go:build linux || darwin

package reclassify

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lock(root *os.Root) (*os.File, error) {
	file, err := root.OpenFile("worker.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, errors.Join(ErrCheckpoint, err)
	}
	if err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, errors.Join(ErrBusy, err, file.Close())
	}
	return file, nil
}

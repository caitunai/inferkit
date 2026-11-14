//go:build !linux && !darwin

package reclassify

import "os"

func lock(_ *os.Root) (*os.File, error) { return nil, ErrUnavailable }

//go:build !linux && !darwin

package onnx

import "os"

func lockCacheFile(_ *os.Root, _ string, _ bool) (*os.File, error) { return nil, ErrCache }

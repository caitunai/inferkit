//go:build !darwin

package onnx

func compilationCachePlatform() (string, error) { return "", ErrCache }

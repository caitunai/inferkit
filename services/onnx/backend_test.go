package onnx

import (
	"errors"
	"runtime"
	"testing"
)

func TestBackendSelection(t *testing.T) {
	t.Parallel()
	auto := providerCPU
	if runtime.GOOS == platformDarwin && runtime.GOARCH == "arm64" {
		auto = providerCoreML
	}
	for name, want := range map[string]string{"": providerCPU, providerCPU: providerCPU, providerCoreML: providerCoreML, providerAuto: auto, providerOpenVINO: providerOpenVINO} {
		b := Backend{Provider: name}
		if err := b.Validate(); err != nil {
			t.Fatal(err)
		}
		if b.PreferredProvider() != want {
			t.Fatal("wrong platform backend")
		}
	}
}

func TestUnknownBackendOptions(t *testing.T) {
	t.Parallel()
	for _, b := range []Backend{{Provider: "unknown"}, {ComputeUnits: "unknown"}, {Provider: providerOpenVINO, Threads: -1}, {Provider: providerOpenVINO, Threads: 1, Streams: 2}} {
		if !errors.Is(b.Validate(), ErrConfig) {
			t.Fatal("unknown backend option accepted")
		}
	}
}

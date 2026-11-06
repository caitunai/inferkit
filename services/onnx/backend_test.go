package onnx

import (
	"errors"
	"runtime"
	"testing"
)

func TestBackendSelection(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", providerCPU, providerCoreML, providerAuto} {
		b := Backend{Provider: name}
		if err := b.Validate(); err != nil {
			t.Fatal(err)
		}
		want := providerCPU
		if name == providerCoreML || name == providerAuto && runtime.GOOS == platformDarwin && runtime.GOARCH == "arm64" {
			want = providerCoreML
		}
		if b.PreferredProvider() != want {
			t.Fatal("wrong platform backend")
		}
	}
}

func TestUnknownBackendOptions(t *testing.T) {
	t.Parallel()
	for _, b := range []Backend{{Provider: "unknown"}, {ComputeUnits: "unknown"}} {
		if !errors.Is(b.Validate(), ErrConfig) {
			t.Fatal("unknown backend option accepted")
		}
	}
}

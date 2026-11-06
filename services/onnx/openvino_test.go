package onnx

import (
	"encoding/json/v2"
	"errors"
	"runtime"
	"strconv"
	"testing"
)

func TestOpenVINOFloatingPointAndThreadBudget(t *testing.T) {
	t.Parallel()
	for _, budget := range [][2]int{{8, 1}, {6, 6}, {1, 1}} {
		b := Backend{Provider: providerOpenVINO, Threads: budget[0], Streams: budget[1]}
		cpu := openVINOTestConfig(t, b)
		if cpu["INFERENCE_PRECISION_HINT"] != "f32" {
			t.Fatal("OpenVINO must use CPU FP32 rather than automatic reduced precision")
		}
		if cpu["INFERENCE_NUM_THREADS"] != strconv.Itoa(budget[0]) || cpu["NUM_STREAMS"] != strconv.Itoa(budget[1]) {
			t.Fatal("OpenVINO ignored the joint thread/stream budget")
		}
	}
}

func openVINOTestConfig(t *testing.T, b Backend) map[string]string {
	t.Helper()
	options, err := b.openVINOOptions()
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]map[string]string
	if err = json.Unmarshal([]byte(options["load_config"]), &config); err != nil {
		t.Fatal(err)
	}
	if len(config) != 1 {
		t.Fatal("unexpected OpenVINO devices configured")
	}
	return config["CPU"]
}

func TestOpenVINORejectsUnsupportedHost(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		t.Skip("Linux amd64 plugin loading is verified in the release image")
	}
	if _, err := openVINODevices(); !errors.Is(err, ErrOpenVINO) {
		t.Fatalf("unsupported host silently accepted OpenVINO: %v", err)
	}
}

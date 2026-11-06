package onnx

import (
	"cmp"
	"encoding/json/v2"
	"errors"
	"os"
	"runtime"
	"slices"
	"strconv"

	"github.com/rs/zerolog/log"
	ort "github.com/yalue/onnxruntime_go"
)

const (
	openVINORegistration = "inferkit_openvino_cpu"
	openVINOName         = "OpenVINOExecutionProvider"
	openVINOLibrary      = "/opt/openvino/lib/libonnxruntime_providers_openvino_plugin.so"
)

// ErrOpenVINO identifies unavailable or ambiguous OpenVINO CPU deployments.
var ErrOpenVINO = errors.New("OpenVINO CPU FP32 unavailable; requires Linux amd64, an Intel AVX2 CPU and the CPU-only OpenVINO plugin")

// The bundled plugin has only the CPU device plugin. Refuse ambiguous device
// lists rather than silently selecting a GPU: the Go binding lacks EP metadata.
func openVINODevices() ([]ort.EpDevice, error) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return nil, ErrOpenVINO
	}
	environment.mu.Lock()
	defer environment.mu.Unlock()
	if environment.users < 1 {
		return nil, errors.Join(ErrOpenVINO, ErrRuntime)
	}
	if len(environment.openvino) > 0 {
		return environment.openvino, nil
	}
	if err := registerOpenVINO(); err != nil {
		return nil, err
	}
	devices, err := ort.GetEpDevices()
	if err != nil {
		return nil, errors.Join(ErrOpenVINO, err)
	}
	devices = slices.DeleteFunc(devices, func(d ort.EpDevice) bool { return d.EpName() != openVINOName })
	if len(devices) != 1 {
		return nil, ErrOpenVINO
	}
	environment.openvino = devices
	return devices, nil
}

// Caller holds environment.mu. Plugin lifetime is bounded by environment leases.
func registerOpenVINO() error {
	if environment.pluginRegistered {
		return nil
	}
	path := cmp.Or(os.Getenv("INFERKIT_OPENVINO_LIBRARY"), openVINOLibrary)
	if err := ort.RegisterExecutionProviderLibrary(openVINORegistration, path); err != nil {
		return errors.Join(ErrOpenVINO, err)
	}
	environment.pluginRegistered = true
	log.Info().Str("library", path).Msg("OpenVINO CPU plugin registered")
	return nil
}

type openVINOCPU struct {
	Precision string `json:"INFERENCE_PRECISION_HINT"`
	Threads   string `json:"INFERENCE_NUM_THREADS"`
	Streams   string `json:"NUM_STREAMS"`
}

func (b Backend) openVINOOptions() (map[string]string, error) {
	config := struct {
		CPU openVINOCPU `json:"CPU"`
	}{CPU: openVINOCPU{Precision: "f32", Threads: strconv.Itoa(max(1, b.Threads)), Streams: strconv.Itoa(max(1, b.Streams))}}
	data, err := json.Marshal(config)
	if err != nil {
		return nil, errors.Join(ErrConfig, err)
	}
	return map[string]string{"load_config": string(data)}, nil
}

func (b Backend) appendOpenVINO(options *ort.SessionOptions) error {
	devices, err := openVINODevices()
	if err != nil {
		return err
	}
	settings, err := b.openVINOOptions()
	if err != nil {
		return err
	}
	// Explicit OpenVINO selection must execute with this EP, rather than report
	// success after silently placing unsupported compute on the default CPU EP.
	if err = options.AddSessionConfigEntry("session.disable_cpu_ep_fallback", "1"); err != nil {
		return errors.Join(ErrOpenVINO, err)
	}
	// Let OpenVINO optimize the original graph. Precision is explicitly f32,
	// independent of reduced-precision defaults on newer Intel CPUs.
	if err = options.SetGraphOptimizationLevel(ort.GraphOptimizationLevelDisableAll); err == nil {
		err = options.AppendExecutionProviderV2(devices, settings)
	}
	if err != nil {
		return errors.Join(ErrOpenVINO, err)
	}
	log.Info().Str("execution_provider", providerOpenVINO).Str("precision", "fp32").
		Int("openvino_threads", max(1, b.Threads)).Int("openvino_streams", max(1, b.Streams)).Msg("OpenVINO CPU session configured")
	return nil
}

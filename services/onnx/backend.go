package onnx

import (
	"errors"
	"runtime"
	"slices"

	"github.com/rs/zerolog/log"
	ort "github.com/yalue/onnxruntime_go"
)

const (
	providerCPU    = "cpu"
	providerCoreML = "coreml"
	providerAuto   = "auto"
	platformDarwin = "darwin"
	computeAll     = "ALL"
)

// Backend selects an execution provider. The zero value preserves CPU execution.
type Backend struct {
	Provider       string
	ComputeUnits   string
	CacheDirectory string
}

// Validate rejects unknown providers and CoreML options before loading a model.
func (b Backend) Validate() error {
	if !slices.Contains([]string{"", providerCPU, providerAuto, providerCoreML}, b.Provider) ||
		!slices.Contains([]string{"", computeAll, "CPUOnly", "CPUAndGPU", "CPUAndNeuralEngine"}, b.ComputeUnits) {
		return ErrConfig
	}
	return nil
}

// PreferredProvider resolves platform selection before any initialization fallback.
func (b Backend) PreferredProvider() string {
	if b.Provider == providerCoreML || b.Provider == providerAuto && runtime.GOOS == platformDarwin && runtime.GOARCH == "arm64" {
		return providerCoreML
	}
	return providerCPU
}

// Create registers the selected EP; ORT retains CPU execution for unsupported
// nodes. Only auto retries failed initialization, using fresh CPU options.
func (b Backend) Create(data []byte, inputs, outputs []string, configure func(*ort.SessionOptions) error) (*ort.DynamicAdvancedSession, string, error) {
	if err := b.Validate(); err != nil {
		return nil, "", err
	}
	provider := b.PreferredProvider()
	session, err := b.create(data, inputs, outputs, configure, provider)
	if err != nil && provider == providerCoreML && b.Provider == providerAuto {
		log.Warn().Err(err).Msg("CoreML session initialization failed; retrying CPU")
		provider = providerCPU
		session, err = b.create(data, inputs, outputs, configure, provider)
	}
	return session, provider, err
}

func (b Backend) create(data []byte, inputs, outputs []string, configure func(*ort.SessionOptions) error, provider string) (session *ort.DynamicAdvancedSession, returnErr error) {
	options, err := ort.NewSessionOptions()
	if err != nil {
		return nil, errors.Join(ErrRuntime, err)
	}
	defer func() {
		if closeErr := options.Destroy(); closeErr != nil {
			returnErr = errors.Join(ErrRuntime, returnErr, closeErr)
			if session != nil {
				returnErr = errors.Join(returnErr, session.Destroy())
				session = nil
			}
		}
	}()
	if err = configure(options); err == nil && provider == providerCoreML {
		err = b.appendCoreML(options)
	}
	if err != nil {
		return nil, errors.Join(ErrRuntime, err)
	}
	session, err = ort.NewDynamicAdvancedSessionWithONNXData(data, inputs, outputs, options)
	if err != nil {
		return nil, errors.Join(ErrRuntime, err)
	}
	return session, nil
}

func (b Backend) appendCoreML(options *ort.SessionOptions) error {
	if runtime.GOOS != platformDarwin {
		return ErrConfig
	}
	settings := b.coreMLOptions()
	if b.CacheDirectory != "" {
		settings["ModelCacheDirectory"] = b.CacheDirectory
	}
	if err := options.AppendExecutionProviderCoreMLV2(settings); err != nil {
		return errors.Join(ErrRuntime, err)
	}
	return nil
}

func (b Backend) coreMLOptions() map[string]string {
	units := b.ComputeUnits
	if units == "" {
		units = computeAll
	}
	return map[string]string{
		"ModelFormat": "MLProgram", "MLComputeUnits": units,
		"RequireStaticInputShapes": "0", "EnableOnSubgraphs": "0",
		"AllowLowPrecisionAccumulationOnGPU": "0",
	}
}

// Package ocr runs bounded local text detection and recognition with ONNX Runtime.
package ocr

import (
	"errors"
	"math"
	"time"

	"github.com/caitun/inferkit/services/onnx"
	"github.com/spf13/viper"
)

var (
	// ErrSource identifies failed image loading or decoding.
	ErrSource = errors.New("load OCR image failed")
	// ErrConfig identifies invalid model paths or resource limits.
	ErrConfig = errors.New("invalid OCR configuration")
	// ErrModel identifies incompatible models, dictionaries or checksums.
	ErrModel = errors.New("invalid OCR model package")
	// ErrLineRecognition classifies a failed parallel line group while retaining input errors.
	ErrLineRecognition = errors.New("OCR line recognition failed")
	// ErrInference identifies failed inference or malformed model output.
	ErrInference = errors.New("OCR inference failed")
	// ErrBusy identifies exhausted request capacity.
	ErrBusy = errors.New("OCR capacity exhausted")
	// ErrClosed identifies calls after shutdown starts.
	ErrClosed = errors.New("OCR service is closed")
)

// Config specifies local model files and bounds memory, CPU and request duration.
// SHA256 fields are optional deployment pins; when set they must match exactly.
type Config struct {
	DetectionModel      string
	RecognitionModel    string
	Dictionary          string
	DetectionSHA256     string
	RecognitionSHA256   string
	DictionarySHA256    string
	Library             string
	Provider            string
	CoreMLComputeUnits  string
	Timeout             time.Duration
	MaxBytes            int64
	MaxPixels           int64
	DetectionMaxSide    int
	RecognitionHeight   int
	RecognitionMaxWidth int
	MaxCandidates       int
	MaxLines            int
	DetectionThreshold  float64
	BoxThreshold        float64
	UnclipRatio         float64
	MinConfidence       float64
}

// Configuration defaults to the PP-OCRv6 medium deployment layout. Enabling the
// HTTP service is controlled separately by image.ocr.enabled.
func Configuration(v *viper.Viper) Config {
	c := Config{
		DetectionModel: "models/pp-ocrv6-medium/det.onnx", RecognitionModel: "models/pp-ocrv6-medium/rec.onnx",
		Dictionary: "models/pp-ocrv6-medium/dict.txt", Timeout: 60 * time.Second, MaxBytes: 10 << 20, MaxPixels: 20_000_000,
		DetectionMaxSide: 960, RecognitionHeight: 48, RecognitionMaxWidth: 2048,
		MaxCandidates: 1000, MaxLines: 1000, DetectionThreshold: 0.2, BoxThreshold: 0.45, UnclipRatio: 1.4, MinConfidence: 0.5,
	}
	readPaths(v, &c)
	readLimits(v, &c)
	return c
}

func readPaths(v *viper.Viper, c *Config) {
	paths := map[string]*string{
		"detectionModel": &c.DetectionModel, "recognitionModel": &c.RecognitionModel,
		"dictionary": &c.Dictionary, "detectionSHA256": &c.DetectionSHA256, "recognitionSHA256": &c.RecognitionSHA256,
		"dictionarySHA256": &c.DictionarySHA256, "library": &c.Library,
		"provider": &c.Provider, "coremlComputeUnits": &c.CoreMLComputeUnits,
	}
	for key, dst := range paths {
		if v.IsSet("image.ocr." + key) {
			*dst = v.GetString("image.ocr." + key)
		}
	}
}

func readLimits(v *viper.Viper, c *Config) {
	ints := map[string]*int{
		"detectionMaxSide": &c.DetectionMaxSide, "recognitionHeight": &c.RecognitionHeight,
		"recognitionMaxWidth": &c.RecognitionMaxWidth, "maxCandidates": &c.MaxCandidates, "maxLines": &c.MaxLines,
	}
	for key, dst := range ints {
		if v.IsSet("image.ocr." + key) {
			*dst = v.GetInt("image.ocr." + key)
		}
	}
	for key, dst := range map[string]*int64{"maxBytes": &c.MaxBytes, "maxPixels": &c.MaxPixels} {
		if v.IsSet("image.ocr." + key) {
			*dst = v.GetInt64("image.ocr." + key)
		}
	}
	for key, dst := range map[string]*float64{"detectionThreshold": &c.DetectionThreshold, "boxThreshold": &c.BoxThreshold, "unclipRatio": &c.UnclipRatio, "minConfidence": &c.MinConfidence} {
		if v.IsSet("image.ocr." + key) {
			*dst = v.GetFloat64("image.ocr." + key)
		}
	}
	if v.IsSet("image.ocr.timeout") {
		c.Timeout = v.GetDuration("image.ocr.timeout")
	}
}

func (c Config) validate() error {
	if c.DetectionModel == "" || c.RecognitionModel == "" || c.Dictionary == "" {
		return ErrConfig
	}
	return errors.Join(c.validateResources(), c.validateShapes(), c.validateThresholds(), (onnx.Backend{Provider: c.Provider, ComputeUnits: c.CoreMLComputeUnits}).Validate())
}

func (c Config) validateResources() error {
	bounds := [][3]int64{
		{int64(c.Timeout), 1, int64(5 * time.Minute)},
		{c.MaxBytes, 1, 100 << 20},
		{c.MaxPixels, 1, 100_000_000},
	}
	return validateBounds(bounds)
}

func (c Config) validateShapes() error {
	if c.DetectionMaxSide%32 != 0 {
		return ErrConfig
	}
	bounds := [][3]int64{
		{int64(c.DetectionMaxSide), 32, 2048},
		{int64(c.RecognitionHeight), 16, 128},
		{int64(c.RecognitionMaxWidth), 320, 4096},
		{int64(c.MaxCandidates), 1, 3000},
		{int64(c.MaxLines), 1, int64(c.MaxCandidates)},
	}
	return validateBounds(bounds)
}

func validateBounds(bounds [][3]int64) error {
	for _, bound := range bounds {
		if bound[0] < bound[1] || bound[0] > bound[2] {
			return ErrConfig
		}
	}
	return nil
}

func (c Config) validateThresholds() error {
	for _, v := range []float64{c.DetectionThreshold, c.BoxThreshold, c.MinConfidence} {
		if math.IsNaN(v) || v < 0 || v > 1 {
			return ErrConfig
		}
	}
	if math.IsNaN(c.UnclipRatio) || c.UnclipRatio <= 0 || c.UnclipRatio > 5 {
		return ErrConfig
	}
	return nil
}

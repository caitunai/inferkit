package ocr

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/caitun/inferkit/services/imageinput"
	"github.com/caitun/inferkit/services/onnx"
	"github.com/rs/zerolog/log"
)

// readArtifact accepts only deployment-selected local files, never request paths.
func readArtifact(path, pin string, limit int64) (data []byte, returnErr error) {
	f, err := os.Open(path) //nolint:gosec // Paths come only from trusted startup configuration.
	if err != nil {
		return nil, errors.Join(ErrModel, err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			returnErr = errors.Join(ErrModel, returnErr, closeErr)
		}
	}()
	data, err = imageinput.ReadLimited(f, limit, ErrModel)
	if err != nil {
		return nil, errors.Join(ErrModel, err)
	}
	if pin != "" {
		hash := sha256.Sum256(data)
		if !strings.EqualFold(pin, hex.EncodeToString(hash[:])) {
			return nil, ErrModel
		}
	}
	return data, nil
}

func loadDictionary(path, pin string) ([]string, error) {
	data, err := readArtifact(path, pin, 4<<20)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(data) {
		return nil, ErrModel
	}
	// Paddle dictionaries omit the CTC blank (index 0) and trailing space.
	tokens := []string{""}
	for line := range strings.SplitSeq(strings.TrimSuffix(string(data), "\n"), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || len(tokens) > 65535 {
			return nil, ErrModel
		}
		tokens = append(tokens, line)
	}
	if len(tokens) < 2 {
		return nil, ErrModel
	}
	return append(tokens, " "), nil
}

type model struct {
	session  *ort.DynamicAdvancedSession
	managed  *onnx.Session
	provider string
	input    ort.Shape
	output   ort.Shape
	pool     tensorPool
}

type modelSettings struct {
	path, pin      string
	role           string
	input, output  ort.Shape
	backend        onnx.Backend
	threads, slots int
	cache          bool
}

func loadModel(c modelSettings) (*model, error) {
	data, err := readArtifact(c.path, c.pin, 512<<20)
	if err != nil {
		return nil, err
	}
	inputs, outputs, err := ort.GetInputOutputInfoWithONNXData(data)
	if err != nil {
		return nil, errors.Join(ErrModel, err)
	}
	if len(inputs) != 1 || len(outputs) != 1 {
		return nil, ErrModel
	}
	if !validTensor(inputs[0], c.input) || !validTensor(outputs[0], c.output) {
		return nil, ErrModel
	}
	start := time.Now()
	session, err := c.backend.Open(data, []string{inputs[0].Name}, []string{outputs[0].Name}, c.cache, func(options *ort.SessionOptions) error { return onnx.Configure(options, c.threads) })
	if err != nil {
		return nil, errors.Join(ErrModel, err)
	}
	elapsed := time.Since(start)
	fingerprint := strings.ToLower(c.pin)
	if fingerprint == "" {
		hash := sha256.Sum256(data)
		fingerprint = hex.EncodeToString(hash[:])
	}
	log.Info().Str("stage", c.role).Str("model_sha256", fingerprint).Str("execution_provider", session.Provider()).
		Bool("coreml_cache", session.CacheDirectory() != "").Float64("session_create_ms", float64(elapsed)/float64(time.Millisecond)).Msg("OCR model initialized")
	return &model{provider: session.Provider(), session: session.Native(), managed: session, input: inputs[0].Dimensions, output: outputs[0].Dimensions, pool: tensorPool{limit: c.slots}}, nil
}

func (m *model) close() error { return errors.Join(m.pool.close(), m.managed.Close()) }

func validTensor(info ort.InputOutputInfo, expected ort.Shape) bool {
	if info.OrtValueType != ort.ONNXTypeTensor || info.DataType != ort.TensorElementDataTypeFloat || len(info.Dimensions) != len(expected) {
		return false
	}
	for i, want := range expected {
		if want > 0 && info.Dimensions[i] > 0 && info.Dimensions[i] != want {
			return false
		}
	}
	return true
}

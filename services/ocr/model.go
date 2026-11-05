package ocr

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"unicode/utf8"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/caitun/inferkit/services/imageinput"
	"github.com/caitun/inferkit/services/onnx"
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
	hash := sha256.Sum256(data)
	if pin != "" && !strings.EqualFold(pin, hex.EncodeToString(hash[:])) {
		return nil, ErrModel
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
	provider string
	input    ort.Shape
}

func loadModel(path, pin string, threads int, backend onnx.Backend, expectedInput, expectedOutput ort.Shape) (*model, error) {
	data, err := readArtifact(path, pin, 512<<20)
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
	if !validTensor(inputs[0], expectedInput) || !validTensor(outputs[0], expectedOutput) {
		return nil, ErrModel
	}
	session, provider, err := createSession(data, threads, inputs[0].Name, outputs[0].Name, backend)
	if err != nil {
		return nil, err
	}
	return &model{provider: provider, session: session, input: inputs[0].Dimensions}, nil
}

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

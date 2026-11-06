package ocr

import (
	"strings"

	ort "github.com/yalue/onnxruntime_go"
)

func decodeCTC(values []float32, shape ort.Shape, dictionary []string) (string, float64, error) {
	if !validRecognitionOutput(values, shape, len(dictionary)) {
		return "", 0, ErrInference
	}
	classes := len(dictionary)
	tokens := make([]string, 0)
	previous, count, total := -1, 0, float64(0)
	for step := range len(values) / classes {
		row := values[step*classes : (step+1)*classes]
		best, valid := probabilityArgmax(row)
		if !valid {
			return "", 0, ErrInference
		}
		if best != 0 && best != previous {
			tokens = append(tokens, dictionary[best])
			count++
			total += float64(row[best])
		}
		previous = best
	}
	if count == 0 {
		return "", 0, nil
	}
	return strings.Join(tokens, ""), total / float64(count), nil
}

func validRecognitionOutput(values []float32, shape ort.Shape, classes int) bool {
	return len(shape) == 3 && shape[0] == 1 && shape[1] > 0 && classes > 0 && shape[2] == int64(classes) && len(values)%classes == 0 && int64(len(values)/classes) == shape[1]
}

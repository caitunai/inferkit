package imagefilter

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/spf13/viper"
)

func TestCanceledStartup(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if service, err := NewContext(ctx, Configuration(viper.New())); service != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled startup created a service: %v", err)
	}
}

func TestInvalidWarmupOutput(t *testing.T) {
	t.Parallel()
	for _, values := range [][]float32{nil, {0.1, 0.2}, {-1, 2}, {float32(math.NaN()), 1}, {float32(math.Inf(1)), 0}} {
		if !errors.Is(validateProbabilities(values), ErrInference) {
			t.Fatalf("invalid probabilities accepted: %v", values)
		}
	}
}

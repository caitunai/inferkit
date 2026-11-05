package imagefilter

import (
	"errors"
	"strconv"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/caitun/inferkit/services/onnx"
)

func configureSession(options *ort.SessionOptions, plan executionPlan) error {
	if err := onnx.Configure(options, plan.threads); err != nil {
		return errors.Join(ErrRuntime, err)
	}
	settings := [][2]string{{"session.dynamic_block_base", strconv.Itoa(plan.dynamicBlockBase)}}
	if plan.spinMicros > 0 {
		settings = append(settings,
			[2]string{"session.intra_op.allow_spinning", "1"},
			[2]string{"session.intra_op.spin_duration_us", strconv.Itoa(plan.spinMicros)},
			[2]string{"session.intra_op.spin_backoff_max", "8"},
			[2]string{"session.force_spinning_stop", "1"})
	}
	for _, setting := range settings {
		if err := options.AddSessionConfigEntry(setting[0], setting[1]); err != nil {
			return errors.Join(ErrRuntime, err)
		}
	}
	return nil
}

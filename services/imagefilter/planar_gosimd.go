//go:build goexperiment.simd && !purego

package imagefilter

import "github.com/caitun/inferkit/services/internal/vector"

func planarBackendName() string {
	if !vector.Enabled() {
		return backendGo
	}
	return "go_simd"
}

func writeRGBTensor(src []byte, dst []float32) {
	if !vector.Enabled() {
		writeRGBTensorScalar(src, dst)
		return
	}
	plane := len(src) / channels
	red, green, blue := dst[:plane], dst[plane:2*plane], dst[2*plane:]
	i := vector.NormalizeRGB(src, red, green, blue)
	writeRGBChannels(src[i*channels:], red[i:], green[i:], blue[i:])
}

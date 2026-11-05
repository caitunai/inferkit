//go:build goexperiment.simd && (arm64 || amd64) && !purego

package imagefilter

import "simd/archsimd"

func planarBackendName() string {
	if !planarSIMDSupported() {
		return backendGo
	}
	return "go_simd"
}

func writeRGBTensor(src []byte, dst []float32) {
	if !planarSIMDSupported() {
		writeRGBTensorScalar(src, dst)
		return
	}
	plane := len(src) / channels
	red, green, blue := dst[:plane], dst[plane:2*plane], dst[2*plane:]
	divisor := archsimd.BroadcastFloat32x4(pixelMax)
	ri := archsimd.LoadUint8x16([]byte{0, 3, 6, 9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	gi := archsimd.LoadUint8x16([]byte{1, 4, 7, 10, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	bi := archsimd.LoadUint8x16([]byte{2, 5, 8, 11, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	i := 0
	for ; i*channels+16 <= len(src); i += 4 {
		pixels := archsimd.LoadUint8x16(src[i*channels:])
		normalizeFour(rgbGather(pixels, ri), divisor, red[i:])
		normalizeFour(rgbGather(pixels, gi), divisor, green[i:])
		normalizeFour(rgbGather(pixels, bi), divisor, blue[i:])
	}
	writeRGBChannels(src[i*channels:], red[i:], green[i:], blue[i:])
}

func normalizeFour(bytes archsimd.Uint8x16, divisor archsimd.Float32x4, dst []float32) {
	bytes.ExtendLo8ToUint16().ExtendLo4ToUint32().BitsToInt32().ConvertToFloat32().Div(divisor).Store(dst)
}

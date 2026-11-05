//go:build goexperiment.simd && arm64 && !purego

package imagefilter

import (
	"encoding/binary"
	"simd/archsimd"
)

func resizeBackendName() string { return "go_simd" }

// The Go 1.27 experiment uses the same guarded coefficients and arithmetic as
// the stable SIMD backend. It is opt-in and never adds a CGO call per pixel.
func (p *resamplePlan) renderSIMDRow(row int) {
	k := p.kernel
	for x := range p.target.Bounds().Dx() {
		index, source, step := x, row*p.source.Stride, 4
		if p.vertical {
			index, source, step = row, x*4, p.source.Stride
		}
		source += int(k.starts[index]) * step
		coefficients := k.coefficients[k.offsets[index]:k.offsets[index+1]]
		destination := row*p.target.Stride + x*4
		filterGoSIMD(p.source.Pix, p.target.Pix[destination:][:4], source, step, coefficients)
	}
}

func filterGoSIMD(src, dst []byte, source, step int, coefficients []int32) {
	sum := archsimd.BroadcastInt32x4(1 << (coefficientBits - 1))
	for k, coefficient := range coefficients {
		pixel := src[source+k*step:][:4]
		packed := archsimd.BroadcastUint32x4(binary.LittleEndian.Uint32(pixel)).ReshapeToUint8s()
		channels := packed.ExtendLo8ToUint16().ExtendLo4ToUint32().BitsToInt32()
		sum = channels.MulAdd(archsimd.BroadcastInt32x4(coefficient), sum)
	}
	dst[0] = filteredByte(int(sum.GetElem(0)))
	dst[1] = filteredByte(int(sum.GetElem(1)))
	dst[2] = filteredByte(int(sum.GetElem(2)))
	dst[3] = filteredByte(int(sum.GetElem(3)))
}

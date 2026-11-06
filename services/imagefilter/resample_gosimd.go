//go:build goexperiment.simd && !purego

package imagefilter

import "github.com/caitun/inferkit/services/internal/vector"

func resizeBackendName() string { return planarBackendName() }

// The Go 1.27 experiment uses the same guarded coefficients and arithmetic as
// the scalar backend. The standard simd package chooses vector width and hardware.
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
	sum := vector.FilterRGBA(src, source, step, coefficients, 1<<(coefficientBits-1))
	dst[0] = filteredByte(int(sum[0]))
	dst[1] = filteredByte(int(sum[1]))
	dst[2] = filteredByte(int(sum[2]))
	dst[3] = filteredByte(int(sum[3]))
}

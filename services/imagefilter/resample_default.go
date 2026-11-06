//go:build !goexperiment.simd || purego

package imagefilter

func resizeBackendName() string { return backendGo }

func (p *resamplePlan) renderSIMDRow(row int) { p.renderScalarRow(row) }

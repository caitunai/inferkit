//go:build !goexperiment.simd || (!arm64 && !amd64) || purego

package imagefilter

func resizeBackendName() string { return backendGo }

func (p *resamplePlan) renderSIMDRow(row int) { p.renderScalarRow(row) }

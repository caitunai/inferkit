//go:build purego || ((!cgo || (!arm64 && !amd64)) && (!goexperiment.simd || !arm64))

package imagefilter

func resizeBackendName() string { return backendGo }

func (p *resamplePlan) renderSIMDRow(row int) { p.renderScalarRow(row) }

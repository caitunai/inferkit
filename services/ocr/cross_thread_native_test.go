package ocr

import (
	"bytes"
	"testing"

	"github.com/caitun/inferkit/services/imageinput"
)

// Different ORT intra-op settings may change floating-point accumulation.
// Text, coordinates and order must still agree; batching uses the stricter
// same-thread confidence comparison in TestNativeAutomaticExecutionParity.
func TestNativeCrossThreadReadingOrder(t *testing.T) {
	c, dir := nativeConfiguration(t)
	serial := newNativeServiceWithPlan(t, c, legacyPlan(4))
	parallel := newNativeService(t, c)
	data := nativeRead(t, dir, "sample.png")
	want, err := serial.Recognize(t.Context(), imageinput.Source{Reader: bytes.NewReader(data)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := parallel.Recognize(t.Context(), imageinput.Source{Reader: bytes.NewReader(data)})
	if err != nil {
		t.Fatal(err)
	}
	assertResultLayout(t, got, want)
}

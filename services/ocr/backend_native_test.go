package ocr

import (
	"bytes"
	"os"
	"runtime"
	"testing"

	"github.com/caitun/inferkit/services/imageinput"
)

func TestNativeCoreMLRecognition(t *testing.T) {
	if !coreMLTestHost() || os.Getenv("OCR_TEST_COREML") != "1" {
		t.Skip("set OCR_TEST_COREML=1 on macOS ARM to exercise CoreML OCR")
	}
	c, dir := nativeConfiguration(t)
	cpu := newNativeService(t, c)
	c.Provider = coreMLProvider
	accelerated := newNativeService(t, c)
	data := nativeRead(t, dir, "sample.png")
	want, err := cpu.Recognize(t.Context(), imageinput.Source{Reader: bytes.NewReader(data)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := accelerated.Recognize(t.Context(), imageinput.Source{Reader: bytes.NewReader(data)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != want.Text || got.Width != want.Width || got.Height != want.Height {
		t.Fatalf("CoreML OCR text differs: CPU=%q CoreML=%q", want.Text, got.Text)
	}
	t.Logf("CoreML preserved text in %d lines", len(got.Lines))
}

func coreMLTestHost() bool { return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" }

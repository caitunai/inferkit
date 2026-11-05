package onnx

import (
	"errors"
	"os"
	"testing"

	ort "github.com/yalue/onnxruntime_go"
)

func TestNativeLeaseLifecycle(t *testing.T) {
	if os.Getenv("OCR_TEST_DIR") == "" {
		t.Skip("set OCR_TEST_DIR to enable native runtime lifecycle tests")
	}
	first, err := Acquire(os.Getenv("OCR_TEST_LIBRARY"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(os.Getenv("OCR_TEST_LIBRARY"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Acquire("incompatible-library-path"); !errors.Is(err, ErrConfig) {
		t.Fatalf("accepted mixed native libraries: %v", err)
	}
	closeLease(t, first)
	closeLease(t, first)
	if !ort.IsInitialized() {
		t.Fatal("one closed lease destroyed another user's environment")
	}
	closeLease(t, second)
	if ort.IsInitialized() {
		t.Fatal("last lease did not destroy native environment")
	}
	third, err := Acquire(os.Getenv("OCR_TEST_LIBRARY"))
	if err != nil {
		t.Fatal(err)
	}
	closeLease(t, third)
}

func closeLease(t *testing.T, lease *Lease) {
	t.Helper()
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}

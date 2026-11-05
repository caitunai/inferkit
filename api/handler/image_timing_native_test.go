package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/services/imagefilter"
)

func TestNativeImageTimingAcrossHTTPPipeline(t *testing.T) {
	service, data := timingModel(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write(data); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	var buffer bytes.Buffer
	output := zerolog.New(&buffer).With().Str("requestID", "native-timing-test").Logger()
	request := httptest.NewRequestWithContext(output.WithContext(t.Context()), http.MethodPost, "/image/filter", strings.NewReader(`{"url":"`+server.URL+`/private.png?signature=never-log-timing-secret"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := imageRecorderFunc(func(context.Context, imagefilter.Result) error { return nil })
	response := serveRecordedFilterRequest(request, service, recorder)
	if response.Code != http.StatusOK {
		t.Fatalf("HTTP pipeline failed: %d %s", response.Code, response.Body)
	}
	fields := imageLogFields(t, &buffer, "image filter timing")
	assertImageDuration(t, fields, "http_parse_ms", "source_read_ms", "hash_ms", "cache_lookup_ms", "slot_wait_ms", "preprocess_ms", "native_pipeline_ms", "tensor_ms", "inference_ms", "result_ms", "cache_store_ms", "persist_ms", "http_response_ms", "total_ms")
	assertNativeImageTiming(t, fields)
	if bytes.Count(buffer.Bytes(), []byte(`"message":"image filter timing"`)) != 1 {
		t.Fatal("nested service duplicated the HTTP trace")
	}

	if strings.Contains(buffer.String(), "private.png") || strings.Contains(buffer.String(), "never-log-timing-secret") {
		t.Fatal("signed image URL leaked into timing logs")
	}
	t.Log(buffer.String())
}

func assertNativeImageTiming(t *testing.T, fields map[string]any) {
	t.Helper()
	if fields["source_type"] != "url" || fields["cache_state"] != "miss" || fields["image_width"] != float64(320) || fields["image_height"] != float64(320) {
		t.Fatalf("incomplete real-image diagnostics: %+v", fields)
	}
	assertNativeDecoderTiming(t, fields)
	if fields["execution_provider"] != "cpu" || fields["inference_attempt"] != float64(1) {
		t.Fatalf("missing pixel format or inference sequence: %+v", fields)
	}
}

func assertNativeDecoderTiming(t *testing.T, fields map[string]any) {
	t.Helper()

	if fields["decode_backend"] != "libvips" || fields["resize_backend"] != "libvips" || fields["pixel_type"] != "rgb" || fields["image_format"] != "png" {
		t.Fatalf("incorrect actual codec diagnostics: %+v", fields)
	}
}

func timingModel(t *testing.T) (*imagefilter.Service, []byte) {
	t.Helper()
	dir := os.Getenv("IMAGE_FILTER_TEST_MODEL")
	if dir == "" {
		t.Skip("set IMAGE_FILTER_TEST_MODEL to run native timing integration")
	}
	settings := viper.New()
	settings.Set("image.filter.model", dir)
	settings.Set("image.filter.library", os.Getenv("IMAGE_FILTER_TEST_LIBRARY"))
	service, err := imagefilter.New(imagefilter.Configuration(settings))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := service.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	data, err := root.ReadFile("fixtures/000_source.png")
	if err != nil {
		t.Fatal(err)
	}
	return service, data
}

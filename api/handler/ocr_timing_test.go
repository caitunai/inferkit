package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"github.com/caitun/inferkit/api/base"
)

func TestOCRTimingCorrelationAndPrivacy(t *testing.T) {
	t.Parallel()
	for _, contentType := range []string{"application/json", "invalid"} {
		var buffer bytes.Buffer
		output := zerolog.New(&buffer).With().Str("requestID", "ocr-timing-test").Logger()
		router := gin.New()
		base.NewRouter(router).POST("/ocr", RecognizeImage(recognizerStub{}))
		request := httptest.NewRequestWithContext(output.WithContext(t.Context()), http.MethodPost, "/ocr", strings.NewReader(`{"url":"https://example.com/private.png?secret=hidden"}`))
		request.Header.Set("Content-Type", contentType)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		fields := imageLogFields(t, &buffer, "OCR timing")
		if fields["requestID"] != "ocr-timing-test" || fields["status"] != float64(response.Code) {
			t.Fatal("OCR timing lost correlation or status")
		}
		assertImageDuration(t, fields, "http_parse_ms", "total_ms")
		if strings.Contains(buffer.String(), "private.png") || strings.Contains(buffer.String(), "hidden") || strings.Contains(buffer.String(), "识别文本") {
			t.Fatal("OCR timing leaked request or text")
		}
		if bytes.Count(buffer.Bytes(), []byte(`"message":"OCR timing"`)) != 1 {
			t.Fatal("OCR emitted duplicate summaries")
		}
	}
}

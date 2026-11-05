package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/services/imageinput"
	"github.com/caitun/inferkit/services/ocr"
)

// TextRecognizer is the HTTP adapter's bounded OCR contract.
type TextRecognizer interface {
	Recognize(context.Context, imageinput.Source) (ocr.Result, error)
	Limits() (int64, time.Duration, int)
}

// RecognizeImage accepts the same upload and URL inputs as image classification.
func RecognizeImage(service TextRecognizer) base.HandlerFunc {
	maxBytes, timeout, maxRequests := service.Limits()
	h := &imageRequestHandler[ocr.Result]{check: service.Recognize, report: ocrError, busy: ocr.ErrBusy, admission: make(chan struct{}, maxRequests), maxBytes: maxBytes, timeout: timeout}
	return func(c *base.Context) { c.Header("Cache-Control", "no-store"); h.handle(c) }
}

func ocrError(c *base.Context, err error) {
	status, message := ocrStatus(err)
	diagnostic := log.Ctx(c.Request.Context()).With().Int("status", status).Str("error", imageFilterLogCause(err)).Logger()
	if status >= 500 {
		diagnostic.Error().Msg("OCR request failed")
	} else {
		diagnostic.Warn().Msg("OCR request failed")
	}
	data := gin.H{}
	if code := imageFilterErrorCode(err); code != "" {
		data["code"] = code
	}
	c.AbortWithStatusJSON(status, gin.H{base.KeyStatus: status, base.KeyMessage: message, base.KeyData: data})
}

func ocrStatus(err error) (int, string) {
	switch {
	case errors.Is(err, ocr.ErrBusy):
		return http.StatusTooManyRequests, "OCR busy"
	case errors.Is(err, ocr.ErrClosed):
		return http.StatusServiceUnavailable, "OCR unavailable"
	case errors.Is(err, ocr.ErrInference), errors.Is(err, ocr.ErrModel):
		if imageRequestTimedOut(err) {
			return http.StatusRequestTimeout, "OCR request canceled or timed out"
		}
		return http.StatusInternalServerError, "OCR inference failed"
	default:
		return imageFilterStatus(err)
	}
}

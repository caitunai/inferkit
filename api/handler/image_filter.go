package handler

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/services/imagefilter"
)

// ImageChecker is the HTTP adapter's narrow inference contract.
type ImageChecker interface {
	Check(context.Context, imagefilter.Source) (imagefilter.Result, error)
	Limits() (int64, time.Duration, int)
}

// FilterImage accepts multipart file/url, JSON url, or form-encoded url.
func FilterImage(service ImageChecker) base.HandlerFunc {
	maxBytes, timeout, maxRequests := service.Limits()
	h := &imageFilterHandler{service: service, admission: make(chan struct{}, maxRequests), maxBytes: maxBytes, timeout: timeout}
	return h.handle
}

type imageFilterHandler struct {
	service   ImageChecker
	admission chan struct{}
	maxBytes  int64
	timeout   time.Duration
}

func (h *imageFilterHandler) handle(c *base.Context) {
	select {
	case h.admission <- struct{}{}:
		defer func() { <-h.admission }()
	default:
		imageFilterError(c, imagefilter.ErrBusy)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), h.timeout)
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	// Bound slow uploads as well as download, preprocessing and inference time.
	if err := setImageReadDeadline(c, time.Now().Add(h.timeout)); err != nil {
		imageFilterError(c, err)
		return
	}
	defer clearImageReadDeadline(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.maxBytes+(64<<10))
	h.check(c)
}

func (h *imageFilterHandler) check(c *base.Context) {
	source, cleanup, err := imageSource(c, h.maxBytes)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		imageFilterError(c, err)
		return
	}
	result, err := h.service.Check(c.Request.Context(), source)
	if err != nil {
		imageFilterError(c, err)
		return
	}
	c.Success(gin.H{"result": result})
}

func setImageReadDeadline(c *base.Context, deadline time.Time) error {
	err := http.NewResponseController(c.Writer).SetReadDeadline(deadline)
	if err != nil && !errors.Is(err, http.ErrNotSupported) {
		return errors.Join(imagefilter.ErrInput, err)
	}
	return nil
}

func clearImageReadDeadline(c *base.Context) {
	if err := setImageReadDeadline(c, time.Time{}); err != nil {
		log.Debug().Err(err).Msg("reset image upload deadline")
	}
}

func imageSource(c *base.Context, maxBytes int64) (imagefilter.Source, func(), error) {
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil {
		return imagefilter.Source{}, nil, imagefilter.ErrInput
	}
	switch mediaType {
	case "multipart/form-data":
		return multipartImage(c, maxBytes)
	case "application/json":
		var body struct {
			URL string `json:"url"`
		}
		if err = json.UnmarshalRead(c.Request.Body, &body); err != nil {
			return imagefilter.Source{}, nil, errors.Join(imagefilter.ErrInput, err)
		}
		return imagefilter.Source{URL: body.URL}, nil, nil
	case "application/x-www-form-urlencoded":
		if err = c.Request.ParseForm(); err != nil {
			return imagefilter.Source{}, nil, errors.Join(imagefilter.ErrInput, err)
		}
		return imagefilter.Source{URL: c.Request.PostForm.Get("url")}, nil, nil
	default:
		return imagefilter.Source{}, nil, imagefilter.ErrInput
	}
}

func multipartImage(c *base.Context, maxBytes int64) (imagefilter.Source, func(), error) {
	// The parser may spill to disk; remove temporary files even after parse failure.
	err := c.Request.ParseMultipartForm(1 << 20)
	cleanup := func() { removeImageMultipart(c) }
	if err != nil {
		return imagefilter.Source{}, cleanup, errors.Join(imagefilter.ErrInput, err)
	}
	form := c.Request.MultipartForm
	files := form.File["file"]
	if len(files) > 1 || len(form.File) > 1 || len(form.Value["url"]) > 1 {
		return imagefilter.Source{}, cleanup, imagefilter.ErrInput
	}
	source := imagefilter.Source{URL: c.Request.PostForm.Get("url")}
	if len(files) == 0 {
		return source, cleanup, nil
	}
	if files[0].Size > maxBytes {
		return imagefilter.Source{}, cleanup, imagefilter.ErrTooLarge
	}
	file, err := files[0].Open()
	if err != nil {
		return imagefilter.Source{}, cleanup, errors.Join(imagefilter.ErrInput, err)
	}
	source.Reader = file
	return source, func() { closeImageUpload(file); cleanup() }, nil
}

func closeImageUpload(file io.Closer) {
	if err := file.Close(); err != nil {
		log.Warn().Err(err).Msg("close image upload")
	}
}

func removeImageMultipart(c *base.Context) {
	if form := c.Request.MultipartForm; form != nil {
		if err := form.RemoveAll(); err != nil {
			log.Warn().Err(err).Msg("remove image upload temporary files")
		}
	}
}

func imageFilterError(c *base.Context, err error) {
	status, message := imageFilterStatus(err)
	if status == http.StatusInternalServerError {
		log.Ctx(c.Request.Context()).Error().Err(err).Msg("image filter failed")
	}
	c.AbortWithStatusJSON(status, gin.H{base.KeyStatus: status, base.KeyMessage: message, base.KeyData: gin.H{}})
}

func imageFilterStatus(err error) (int, string) {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return http.StatusRequestEntityTooLarge, "image request too large"
	}
	switch {
	case errors.Is(err, imagefilter.ErrTooLarge):
		return http.StatusRequestEntityTooLarge, "image too large"
	case imageRequestTimedOut(err):
		return http.StatusRequestTimeout, "image request canceled or timed out"
	case errors.Is(err, imagefilter.ErrURL), errors.Is(err, imagefilter.ErrInput):
		return http.StatusBadRequest, "invalid image or URL"
	case errors.Is(err, imagefilter.ErrDownload):
		return http.StatusBadGateway, "image download failed"
	case errors.Is(err, imagefilter.ErrBusy):
		return http.StatusTooManyRequests, "image filter busy"
	case errors.Is(err, imagefilter.ErrClosed):
		return http.StatusServiceUnavailable, "image filter unavailable"
	default:
		return http.StatusInternalServerError, "image inference failed"
	}
}

func imageRequestTimedOut(err error) bool {
	if networkErr, ok := errors.AsType[net.Error](err); ok && networkErr.Timeout() {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

package handler

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"time"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/services/imageinput"
	"github.com/caitun/inferkit/services/imagetiming"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

type imageRequestHandler[T any] struct {
	check     func(context.Context, imageinput.Source) (T, error)
	report    func(*base.Context, error)
	admission chan struct{}
	busy      error
	maxBytes  int64
	timeout   time.Duration
}

func (h *imageRequestHandler[T]) handle(c *base.Context) {
	done := imagetiming.Measure(c.Request.Context(), imagetiming.Admission)
	select {
	case h.admission <- struct{}{}:
		done(nil)
		defer func() { <-h.admission }()
	default:
		done(h.busy)
		h.report(c, h.busy)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), h.timeout)
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	if err := setImageReadDeadline(c, time.Now().Add(h.timeout)); err != nil {
		h.report(c, err)
		return
	}
	defer clearImageReadDeadline(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.maxBytes+(64<<10))
	h.process(c)
}

func (h *imageRequestHandler[T]) process(c *base.Context) {
	done := imagetiming.Measure(c.Request.Context(), imagetiming.HTTPParse)
	source, cleanup, err := imageSource(c, h.maxBytes)
	done(err)
	if cleanup != nil {
		defer func() {
			finished := imagetiming.Measure(c.Request.Context(), imagetiming.HTTPCleanup)
			cleanup()
			finished(nil)
		}()
	}
	if err != nil {
		h.report(c, err)
		return
	}
	if parsed, parseErr := url.Parse(source.URL); parseErr == nil {
		c.Set(imageSourceHostKey, parsed.Hostname())
	}
	result, err := h.check(c.Request.Context(), source)
	if err != nil {
		h.report(c, err)
		return
	}
	done = imagetiming.Measure(c.Request.Context(), imagetiming.HTTPResponse)
	c.Success(gin.H{"result": result})
	done(nil)
}

func setImageReadDeadline(c *base.Context, deadline time.Time) error {
	err := http.NewResponseController(c.Writer).SetReadDeadline(deadline)
	if err != nil && !errors.Is(err, http.ErrNotSupported) {
		return errors.Join(imageinput.ErrInput, err)
	}
	return nil
}

func clearImageReadDeadline(c *base.Context) {
	if err := setImageReadDeadline(c, time.Time{}); err != nil {
		log.Debug().Err(err).Msg("reset image upload deadline")
	}
}

func imageSource(c *base.Context, maxBytes int64) (imageinput.Source, func(), error) {
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil {
		return imageinput.Source{}, nil, imageinput.ErrInput
	}
	switch mediaType {
	case "multipart/form-data":
		return multipartImage(c, maxBytes)
	case "application/json":
		var body struct {
			URL string `json:"url"`
		}
		if err = json.UnmarshalRead(c.Request.Body, &body); err != nil {
			return imageinput.Source{}, nil, errors.Join(imageinput.ErrInput, err)
		}
		return imageinput.Source{URL: body.URL}, nil, nil
	case "application/x-www-form-urlencoded":
		if err = c.Request.ParseForm(); err != nil {
			return imageinput.Source{}, nil, errors.Join(imageinput.ErrInput, err)
		}
		return imageinput.Source{URL: c.Request.PostForm.Get("url")}, nil, nil
	default:
		return imageinput.Source{}, nil, imageinput.ErrInput
	}
}

func multipartImage(c *base.Context, maxBytes int64) (imageinput.Source, func(), error) {
	// The parser may spill to disk; remove temporary files even after parse failure.
	err := c.Request.ParseMultipartForm(1 << 20)
	cleanup := func() { removeImageMultipart(c) }
	if err != nil {
		return imageinput.Source{}, cleanup, errors.Join(imageinput.ErrInput, err)
	}
	form := c.Request.MultipartForm
	files := form.File["file"]
	if len(files) > 1 || len(form.File) > 1 || len(form.Value["url"]) > 1 {
		return imageinput.Source{}, cleanup, imageinput.ErrInput
	}
	source := imageinput.Source{URL: c.Request.PostForm.Get("url")}
	if len(files) == 0 {
		return source, cleanup, nil
	}
	if files[0].Size > maxBytes {
		return imageinput.Source{}, cleanup, imageinput.ErrTooLarge
	}
	file, err := files[0].Open()
	if err != nil {
		return imageinput.Source{}, cleanup, errors.Join(imageinput.ErrInput, err)
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

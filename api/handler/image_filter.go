package handler

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/services/imagefilter"
	"github.com/caitun/inferkit/services/imageinput"
)

// ImageChecker is the HTTP adapter's narrow inference contract.
type ImageChecker interface {
	Check(context.Context, imagefilter.Source) (imagefilter.Result, error)
	Limits() (int64, time.Duration, int)
}

// ImageRecorder persists successful URL-associated results independently of inference caching.
type ImageRecorder interface {
	Save(context.Context, imagefilter.Result) error
}

// ErrImageCheck identifies failed classification without changing its underlying error identity.
var ErrImageCheck = errors.New("image check failed")

// ErrImageResultSave identifies a failure to durably collect a URL result.
var ErrImageResultSave = errors.New("save image prediction failed")

// FilterImage accepts multipart file/url, JSON url, or form-encoded url.
func FilterImage(service ImageChecker, recorder ImageRecorder) base.HandlerFunc {
	_, _, maxRequests := service.Limits()
	return filterImage(service, recorder, make(chan struct{}, maxRequests))
}

// FilterImageBackends shares body admission across both backends and URL aliases.
func FilterImageBackends(service *imagefilter.Service, recorder ImageRecorder) (base.HandlerFunc, base.HandlerFunc) {
	return filterImageBackends(service.WithBackend(imagefilter.Libvips), service.WithBackend(imagefilter.Go), recorder)
}

func filterImageBackends(native, goBackend ImageChecker, recorder ImageRecorder) (base.HandlerFunc, base.HandlerFunc) {
	_, _, maxRequests := native.Limits()
	admission := make(chan struct{}, maxRequests)
	return filterImage(native, recorder, admission), filterImage(goBackend, recorder, admission)
}

func filterImage(service ImageChecker, recorder ImageRecorder, admission chan struct{}) base.HandlerFunc {
	maxBytes, timeout, _ := service.Limits()
	check := func(ctx context.Context, source imageinput.Source) (imagefilter.Result, error) {
		result, err := service.Check(ctx, source)
		if err != nil {
			return imagefilter.Result{}, errors.Join(ErrImageCheck, err)
		}
		result.URL = source.URL
		if err = saveImageResult(ctx, recorder, result); err != nil {
			return imagefilter.Result{}, err
		}
		return result, nil
	}
	h := &imageRequestHandler[imagefilter.Result]{check: check, report: imageFilterError, busy: imagefilter.ErrBusy, admission: admission, maxBytes: maxBytes, timeout: timeout}
	return measuredImageFilter(h.handle)
}

func imageFilterError(c *base.Context, err error) {
	status, message := imageFilterStatus(err)
	logImageFilterError(c, err, status)
	data := gin.H{}
	if code := imageFilterErrorCode(err); code != "" {
		data["code"] = code
	}
	c.AbortWithStatusJSON(status, gin.H{base.KeyStatus: status, base.KeyMessage: message, base.KeyData: data})
}

func imageFilterStatus(err error) (int, string) {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return http.StatusRequestEntityTooLarge, "image request too large"
	}
	switch {
	case errors.Is(err, ErrImageResultSave):
		return http.StatusServiceUnavailable, "image result storage unavailable"
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

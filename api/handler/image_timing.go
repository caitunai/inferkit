package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/services/imagefilter"
	"github.com/caitun/inferkit/services/imagetiming"
)

func measuredImageFilter(next base.HandlerFunc) base.HandlerFunc {
	return func(c *base.Context) {
		ctx, trace := imagetiming.New(c.Request.Context())
		c.Request = c.Request.WithContext(ctx)
		defer func() { trace.Finish(ctx, c.Writer.Status() < http.StatusBadRequest, c.Writer.Status()) }()
		next(c)
	}
}

func saveImageResult(ctx context.Context, recorder ImageRecorder, result imagefilter.Result) error {
	trace := imagetiming.From(ctx)
	if result.URL == "" {
		trace.Persistence("skipped_no_url")
		return nil
	}
	if recorder == nil {
		trace.Persistence("skipped_disabled")
		return nil
	}
	done := imagetiming.Measure(ctx, imagetiming.Persist)
	err := recorder.Save(ctx, result)
	done(err)
	if err != nil {
		trace.Persistence("failed")
		return errors.Join(ErrImageResultSave, err)
	}
	trace.Persistence("saved")
	return nil
}

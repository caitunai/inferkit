package imagefilter

import (
	"context"
	"errors"

	"github.com/caitun/inferkit/services/imageinput"
	"github.com/caitun/inferkit/services/imagetiming"
	"github.com/go-resty/resty/v2"
)

// Source describes an image; readers are caller-owned and take precedence over URLs.
type Source = imageinput.Source

func newDownloader(c Config, plan executionPlan) *resty.Client {
	return imageinput.NewDownloader(imageinput.Limits{Timeout: c.Timeout, MaxRequests: plan.requests, Concurrency: plan.concurrency})
}

func (s *Service) readSource(ctx context.Context, src Source) ([]byte, error) {
	done := imagetiming.Measure(ctx, imagetiming.SourceRead)
	data, err := imageinput.Read(ctx, s.http, src, s.config.MaxBytes)
	done(err)
	if trace := imagetiming.From(ctx); trace != nil {
		trace.Input(sourceKind(src), len(data))
	}
	if err != nil {
		return nil, errors.Join(ErrSource, err)
	}
	return data, nil
}

func sourceKind(src Source) string {
	if src.Reader != nil {
		if src.URL != "" {
			return "reader_with_url"
		}
		return "reader"
	}
	if src.URL != "" {
		return "url"
	}
	return "empty"
}

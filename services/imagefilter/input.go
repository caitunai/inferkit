package imagefilter

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/rs/zerolog/log"
)

// Source uses Reader when present. URL then describes that file and is never fetched.
// Readers remain caller-owned; only the CLI may open local paths.
type Source struct {
	Reader io.Reader
	URL    string
}

func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errors.Join(ErrURL, err)
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(raw) > 8192 {
		return ErrURL
	}
	return nil
}

func newDownloader(c Config) *resty.Client {
	dialer := &net.Dialer{Timeout: min(c.Timeout, 5*time.Second), KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext: dialer.DialContext, TLSHandshakeTimeout: 5 * time.Second,
		MaxIdleConns: c.MaxRequests, MaxIdleConnsPerHost: c.Concurrency,
		MaxConnsPerHost: c.MaxRequests, IdleConnTimeout: 90 * time.Second,
		ResponseHeaderTimeout: c.Timeout, MaxResponseHeaderBytes: 32 << 10,
	}
	return resty.NewWithClient(&http.Client{
		Transport: transport, Timeout: c.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 3 {
				return ErrURL
			}
			return validateURL(req.URL.String())
		},
	})
}

func (s *Service) readSource(ctx context.Context, src Source) ([]byte, error) {
	if src.URL != "" {
		if err := validateURL(src.URL); err != nil {
			return nil, err
		}
	}
	if src.Reader != nil {
		return readLimited(src.Reader, s.config.MaxBytes, ErrInput)
	}
	if src.URL == "" {
		return nil, ErrInput
	}
	response, err := s.http.R().SetContext(ctx).SetDoNotParseResponse(true).Get(src.URL)
	if err != nil {
		return nil, classifyDownloadError(err)
	}
	defer func() {
		if closeErr := response.RawBody().Close(); closeErr != nil {
			log.Debug().Err(closeErr).Msg("close image download")
		}
	}()
	if response.StatusCode() != http.StatusOK {
		return nil, &DownloadStatusError{
			Host:       response.RawResponse.Request.URL.Hostname(),
			StatusCode: response.StatusCode(), ContentType: response.Header().Get("Content-Type"),
		}
	}
	if response.RawResponse.ContentLength > s.config.MaxBytes {
		return nil, ErrTooLarge
	}
	return readLimited(response.RawBody(), s.config.MaxBytes, ErrDownload)
}

func readLimited(reader io.Reader, limit int64, failure error) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, errors.Join(failure, err)
	}
	if int64(len(data)) > limit {
		return nil, ErrTooLarge
	}
	if len(data) == 0 {
		return nil, failure
	}
	return data, nil
}

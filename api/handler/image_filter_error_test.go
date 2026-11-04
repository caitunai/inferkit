package handler

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/caitun/inferkit/services/imagefilter"
)

func TestDNSFailureIsLoggedWithoutSignedURL(t *testing.T) {
	t.Parallel()
	const sourceURL = "https://cdn.example.com/private-image.jpeg?signature=never-log-this-secret"
	const sourceHost = "cdn.example.com"
	var buffer bytes.Buffer
	output := zerolog.New(&buffer)
	dns := &net.DNSError{Name: sourceHost, Server: "127.0.0.1:53", Err: "no such host", IsNotFound: true}
	failure := errors.Join(imagefilter.ErrDownload, imagefilter.ErrDNS, &url.Error{Op: "Get", URL: sourceURL, Err: dns})
	checker := imageCheckerStub{check: func(imagefilter.Source) (imagefilter.Result, error) { return imagefilter.Result{}, failure }}
	request := httptest.NewRequestWithContext(output.WithContext(t.Context()), http.MethodPost, "/image/filter", strings.NewReader(`{"url":"`+sourceURL+`"}`))
	request.Header.Set("Content-Type", "application/json")
	response := serveFilterRequest(request, checker)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), imageCodeDNS) {
		t.Fatalf("missing actionable DNS response: %d %s", response.Code, response.Body)
	}
	var fields map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	if fields["stage"] != "resolve" || fields["dns_host"] != sourceHost || fields["source_host"] != sourceHost {
		t.Fatalf("missing DNS diagnostics: %s", &buffer)
	}
	if strings.Contains(buffer.String(), "never-log-this-secret") || strings.Contains(buffer.String(), "private-image.jpeg") {
		t.Fatal("image URL path or signed parameters leaked into logs")
	}
}

func TestImageFailureStagesAndDNSCode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err   error
		stage string
	}{
		{imagefilter.ErrURL, "validate_url"},
		{imagefilter.ErrDNS, "resolve"},
		{imagefilter.ErrDownload, "download"},
		{imagefilter.ErrPreprocess, "preprocess"},
		{imagefilter.ErrInference, "inference"},
		{ErrImageResultSave, "persist"},
	}
	for _, test := range cases {
		if stage := imageFilterStage(test.err); stage != test.stage {
			t.Errorf("%v: stage %s, want %s", test.err, stage, test.stage)
		}
	}
	if code := imageFilterErrorCode(errors.Join(imagefilter.ErrDownload, imagefilter.ErrDNS)); code != imageCodeDNS {
		t.Fatalf("DNS failure code: %s", code)
	}
}

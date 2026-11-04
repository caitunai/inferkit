package handler

import (
	"errors"
	"net"
	"net/url"

	"github.com/rs/zerolog/log"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/services/imagefilter"
)

const (
	imageSourceHostKey = "image_source_host"
	imageCodeDNS       = "image_url_dns_failed"
)

func imageFilterErrorCode(err error) string {
	if errors.Is(err, imagefilter.ErrDNS) {
		return imageCodeDNS
	}
	return ""
}

func imageFilterStage(err error) string {
	switch {
	case errors.Is(err, ErrImageResultSave):
		return "persist"
	case errors.Is(err, imagefilter.ErrDNS):
		return "resolve"
	case errors.Is(err, imagefilter.ErrURL):
		return "validate_url"
	case errors.Is(err, imagefilter.ErrDownload):
		return "download"
	case errors.Is(err, imagefilter.ErrPreprocess):
		return "preprocess"
	case errors.Is(err, imagefilter.ErrBusy), errors.Is(err, imagefilter.ErrClosed):
		return "admission"
	case errors.Is(err, imagefilter.ErrInference):
		return "inference"
	default:
		return "input"
	}
}

// Record failure stages and causes without logging complete source URLs.
func logImageFilterError(c *base.Context, err error, status int) {
	diagnostic := log.Ctx(c.Request.Context()).With().Int("status", status).
		Str("stage", imageFilterStage(err)).Str("error", imageFilterLogCause(err)).Str("error_code", imageFilterErrorCode(err))
	if host := c.GetString(imageSourceHostKey); host != "" {
		diagnostic = diagnostic.Str("source_host", host)
	}
	if requestID := c.Writer.Header().Get("X-Request-ID"); requestID != "" {
		diagnostic = diagnostic.Str("request_id", requestID)
	}
	if dns, ok := errors.AsType[*net.DNSError](err); ok {
		diagnostic = diagnostic.Str("dns_host", dns.Name).Str("dns_server", dns.Server)
	}
	if connection, ok := errors.AsType[*net.OpError](err); ok && connection.Addr != nil {
		diagnostic = diagnostic.Str("network_op", connection.Op).Str("remote_address", connection.Addr.String())
	}
	if response, ok := errors.AsType[*imagefilter.DownloadStatusError](err); ok {
		diagnostic = diagnostic.Str("target_host", response.Host).Int("remote_status", response.StatusCode).Str("remote_content_type", response.ContentType)
	}
	output := diagnostic.Logger()
	if status >= 500 {
		output.Error().Msg("image filter request failed")
		return
	}
	output.Warn().Msg("image filter request failed")
}

// net/http URL errors contain the complete URL, including potentially signed query parameters.
func imageFilterLogCause(err error) string {
	if requestErr, ok := errors.AsType[*url.Error](err); ok {
		return imageFilterLogCause(requestErr.Err)
	}
	return err.Error()
}

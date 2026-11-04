package imagefilter

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
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
	transport := &http.Transport{
		DialContext: publicDial, TLSHandshakeTimeout: 5 * time.Second,
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

func publicDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.Join(ErrURL, err)
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, errors.Join(ErrDownload, err)
	}
	if len(ips) == 0 {
		return nil, ErrURL
	}
	for _, ip := range ips {
		if !publicIP(ip) {
			return nil, ErrURL
		}
	}
	// Dial the checked IP itself, retaining the original HTTP Host and TLS SNI.
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	if err != nil {
		return nil, errors.Join(ErrDownload, err)
	}
	return conn, nil
}

var forbiddenRanges = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001:db8::/32"),
	// Reject transition mechanisms that can tunnel otherwise forbidden IPv4 addresses.
	netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("2001::/32"),
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range forbiddenRanges {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

func (s *Service) readSource(ctx context.Context, src Source) ([]byte, error) {
	if src.URL != "" {
		if err := validateURL(src.URL); err != nil {
			return nil, err
		}
	}
	if src.Reader != nil {
		return readLimited(src.Reader, s.config.MaxBytes)
	}
	if src.URL == "" {
		return nil, ErrInput
	}
	response, err := s.http.R().SetContext(ctx).SetDoNotParseResponse(true).Get(src.URL)
	if err != nil {
		return nil, errors.Join(ErrDownload, err)
	}
	defer func() {
		if closeErr := response.RawBody().Close(); closeErr != nil {
			log.Debug().Err(closeErr).Msg("close image download")
		}
	}()
	if response.StatusCode() != http.StatusOK {
		return nil, ErrDownload
	}
	if response.RawResponse.ContentLength > s.config.MaxBytes {
		return nil, ErrTooLarge
	}
	return readLimited(response.RawBody(), s.config.MaxBytes)
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, errors.Join(ErrInput, err)
	}
	if int64(len(data)) > limit {
		return nil, ErrTooLarge
	}
	if len(data) == 0 {
		return nil, ErrInput
	}
	return data, nil
}

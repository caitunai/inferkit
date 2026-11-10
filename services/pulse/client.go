package pulse

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
	"uuid"

	"github.com/go-resty/resty/v2"
	"github.com/golang-jwt/jwt/v5"

	"github.com/caitun/inferkit/xutil"
)

const (
	// ImageFilter counts one authenticated image classification request.
	ImageFilter = "image.filter"
	// ImageOCR counts one authenticated text recognition request.
	ImageOCR       = "image.ocr"
	externalUserID = "inferkit-u1"
	sessionLayout  = "2006010215"
)

var sessionZone = time.FixedZone("Asia/Shanghai", 8*60*60)

// Client owns a bounded HTTP pool and protected background reporting tasks.
// Enqueue is nonblocking best effort; Report is the synchronous transport operation.
type Client struct {
	owner  context.Context //nolint:containedctx // Client owns this derived context until Close has drained or canceled all reporting tasks.
	http   *resty.Client
	tasks  *xutil.Group
	cancel context.CancelFunc
	slots  chan struct{}
	config Config
	mu     sync.Mutex
	once   sync.Once
	closed bool
}

type event struct {
	RequestID       string `json:"request_id"`
	EventName       string `json:"event_name"`
	ExternalUserID  string `json:"external_user_id"`
	ClientSessionID string `json:"client_session_id"`
	TenantID        int64  `json:"tenant_id"`
	AppID           int64  `json:"app_id"`
	PlatformID      int64  `json:"platform_id"`
	Tokens          int64  `json:"tokens"`
}

// HTTPStatusError records a rejected response without retaining its body.
type HTTPStatusError struct {
	StatusCode int
}

func (e *HTTPStatusError) Error() string {
	return "Pulse HTTP status " + strconv.Itoa(e.StatusCode)
}

// New creates a reporter with an independent lifetime. Call Close to drain it.
func New(c Config) (*Client, error) {
	return NewContext(context.Background(), c)
}

// NewContext validates configuration and binds background work to its process owner.
func NewContext(ctx context.Context, c Config) (*Client, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: c.Timeout, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment, DialContext: dialer.DialContext,
		TLSHandshakeTimeout: c.Timeout, ResponseHeaderTimeout: c.Timeout,
		MaxIdleConns: c.MaxConnections, MaxIdleConnsPerHost: c.MaxConnections,
		MaxConnsPerHost: c.MaxConnections, IdleConnTimeout: 90 * time.Second,
		MaxResponseHeaderBytes: int64(c.MaxResponseBytes),
	}
	client := resty.New().SetTransport(transport).SetTimeout(c.Timeout).
		SetResponseBodyLimit(c.MaxResponseBytes).SetRedirectPolicy(resty.NoRedirectPolicy())
	owner, cancel := context.WithCancel(ctx)
	return &Client{
		http: client, config: c,
		owner: owner, cancel: cancel, tasks: xutil.WaitGroup(reportTaskName), slots: make(chan struct{}, c.MaxConnections),
	}, nil
}

// Report sends one event using its admission hour, a fresh UUID and an HS256 JWT.
// Tokens contains elapsed processing time in whole milliseconds, excluding delivery.
// Caller cancellation and the configured timeout bound the complete HTTP call,
// including waiting for a pooled connection. A nil client disables reporting.
func (c *Client) Report(ctx context.Context, name string, admittedAt time.Time, elapsed time.Duration) error {
	if c == nil {
		return nil
	}
	if name != ImageFilter && name != ImageOCR {
		return ErrEvent
	}
	body, err := json.Marshal(event{
		TenantID: c.config.TenantID, AppID: c.config.AppID, PlatformID: c.config.PlatformID,
		RequestID: uuid.New().String(), EventName: name, ExternalUserID: externalUserID,
		ClientSessionID: "sid-" + admittedAt.In(sessionZone).Format(sessionLayout) + "0000",
		Tokens:          max(0, elapsed.Milliseconds()),
	})
	if err != nil {
		return errors.Join(ErrReport, err)
	}
	token, err := c.sign(time.Now())
	if err != nil {
		return errors.Join(ErrReport, err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()
	response, err := c.http.R().SetContext(requestCtx).SetAuthToken(token).
		SetHeader("Content-Type", "application/json").SetBody(body).Post(c.config.URL)
	if err != nil {
		return errors.Join(ErrReport, err)
	}
	if !response.IsSuccess() {
		return errors.Join(ErrReport, &HTTPStatusError{StatusCode: response.StatusCode()})
	}
	return nil
}

func (c *Client) sign(now time.Time) (string, error) {
	claims := jwt.RegisteredClaims{
		Issuer: c.config.Issuer, Audience: jwt.ClaimStrings{c.config.Audience},
		IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(c.config.TokenLifetime)),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(c.config.Secret))
	if err != nil {
		return "", errors.Join(ErrSign, err)
	}
	return token, nil
}

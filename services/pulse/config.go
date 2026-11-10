// Package pulse reports image API usage to the Pulse event service.
package pulse

import (
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/viper"
)

var (
	// ErrConfig identifies invalid Pulse signing, destination or resource settings.
	ErrConfig = errors.New("invalid Pulse configuration")
	// ErrReport classifies event delivery failures while preserving their cause.
	ErrReport = errors.New("report Pulse event failed")
	// ErrEvent identifies an unsupported event name.
	ErrEvent = errors.New("invalid Pulse event")
	// ErrSign identifies a JWT signing failure.
	ErrSign = errors.New("sign Pulse JWT failed")
	// ErrBusy identifies a dropped best-effort event when all reporting slots are occupied.
	ErrBusy = errors.New("pulse reporting capacity exhausted")
	// ErrClosed identifies submissions after reporter shutdown or owner cancellation.
	ErrClosed = errors.New("pulse reporting is closed")
)

// Config contains trusted startup settings; Secret must never be logged.
type Config struct {
	URL              string
	Secret           string
	Audience         string
	Issuer           string
	TenantID         int64
	AppID            int64
	PlatformID       int64
	Timeout          time.Duration
	TokenLifetime    time.Duration
	ShutdownTimeout  time.Duration
	MaxConnections   int
	MaxResponseBytes int
}

// Configuration reads Pulse settings once, preserving environment overrides.
// The caller controls activation using pulse.app.enabled.
func Configuration(v *viper.Viper) Config {
	c := Config{
		Issuer: "inferkit-server", TenantID: 1, AppID: 3, PlatformID: 1,
		Timeout: time.Second, TokenLifetime: 5 * time.Minute, ShutdownTimeout: 5 * time.Second,
		MaxConnections: 32, MaxResponseBytes: 64 << 10,
	}
	c.URL = v.GetString("pulse.server.url")
	c.Secret = v.GetString("pulse.server.secret")
	c.Audience = v.GetString("pulse.server.audience")
	if v.IsSet("pulse.app.issuer") {
		c.Issuer = v.GetString("pulse.app.issuer")
	}
	for key, target := range map[string]*int64{"tenantId": &c.TenantID, "appId": &c.AppID, "platformId": &c.PlatformID} {
		if v.IsSet("pulse.app." + key) {
			*target = v.GetInt64("pulse.app." + key)
		}
	}
	for key, target := range map[string]*time.Duration{"timeout": &c.Timeout, "tokenLifetime": &c.TokenLifetime, "shutdownTimeout": &c.ShutdownTimeout} {
		if v.IsSet("pulse.app." + key) {
			*target = v.GetDuration("pulse.app." + key)
		}
	}
	for key, target := range map[string]*int{"maxConnections": &c.MaxConnections, "maxResponseBytes": &c.MaxResponseBytes} {
		if v.IsSet("pulse.app." + key) {
			*target = v.GetInt("pulse.app." + key)
		}
	}
	return c
}

func (c Config) validate() error {
	u, err := url.Parse(c.URL)
	if err != nil {
		return ErrConfig
	}
	if u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return ErrConfig
	}
	if len(c.Secret) < 32 || strings.TrimSpace(c.Audience) == "" || strings.TrimSpace(c.Issuer) == "" {
		return ErrConfig
	}
	return c.validateBounds()
}

func (c Config) validateBounds() error {
	bounds := [][3]int64{
		{c.TenantID, 1, 1<<53 - 1},
		{c.AppID, 1, 1<<53 - 1},
		{c.PlatformID, 1, 1<<53 - 1},
		{int64(c.Timeout), 1, int64(30 * time.Second)},
		{int64(c.TokenLifetime), int64(time.Second), int64(24 * time.Hour)},
		{int64(c.ShutdownTimeout), 1, int64(time.Minute)},
		{int64(c.MaxConnections), 1, 1024},
		{int64(c.MaxResponseBytes), 1, 1 << 20},
	}
	for _, bound := range bounds {
		if bound[0] < bound[1] || bound[0] > bound[2] {
			return ErrConfig
		}
	}
	return nil
}

package pulse

import (
	"errors"
	"testing"
	"time"

	"github.com/spf13/viper"
)

func TestConfigurationOverridesAndEnvironment(t *testing.T) {
	t.Setenv("PULSE_TEST_SECRET", testSecret)
	v := viper.New()
	if err := v.BindEnv("pulse.server.secret", "PULSE_TEST_SECRET"); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{
		"server.url": "https://example.com/events", "server.audience": "test-pulse", "app.issuer": "test-issuer",
		"app.tenantId": 2, "app.appId": 4, "app.platformId": 5, "app.timeout": "2s",
		"app.tokenLifetime": "10m", "app.shutdownTimeout": "6s", "app.maxConnections": 8, "app.maxResponseBytes": 4096,
	}
	for key, value := range settings {
		v.Set("pulse."+key, value)
	}
	c := Configuration(v)
	want := Config{
		URL: "https://example.com/events", Secret: testSecret, Audience: "test-pulse", Issuer: "test-issuer",
		TenantID: 2, AppID: 4, PlatformID: 5, Timeout: 2 * time.Second, TokenLifetime: 10 * time.Minute,
		MaxConnections: 8, MaxResponseBytes: 4096, ShutdownTimeout: 6 * time.Second,
	}
	if c != want {
		t.Fatal("configuration overrides were not loaded")
	}
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidConfiguration(t *testing.T) {
	t.Parallel()
	changes := map[string]func(*Config){
		"http":          func(c *Config) { c.URL = "http://example.com/events" },
		"relative URL":  func(c *Config) { c.URL = "/events" },
		"invalid URL":   func(c *Config) { c.URL = "://%" },
		"credentials":   func(c *Config) { c.URL = "https://user:pass@example.com/events" },
		"query":         func(c *Config) { c.URL = "https://example.com/events?secret=value" },
		"fragment":      func(c *Config) { c.URL = "https://example.com/events#part" },
		"secret":        func(c *Config) { c.Secret = "short" },
		"audience":      func(c *Config) { c.Audience = " " },
		"issuer":        func(c *Config) { c.Issuer = "" },
		"tenant":        func(c *Config) { c.TenantID = 0 },
		"app":           func(c *Config) { c.AppID = -1 },
		"platform":      func(c *Config) { c.PlatformID = 0 },
		"app.timeout":   func(c *Config) { c.Timeout = 0 },
		"long timeout":  func(c *Config) { c.Timeout = time.Minute },
		"lifetime":      func(c *Config) { c.TokenLifetime = time.Millisecond },
		"shutdown":      func(c *Config) { c.ShutdownTimeout = 0 },
		"connections":   func(c *Config) { c.MaxConnections = 0 },
		"response size": func(c *Config) { c.MaxResponseBytes = 0 },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			config := testConfig("https://example.com/events")
			change(&config)
			if _, err := New(config); !errors.Is(err, ErrConfig) {
				t.Fatalf("invalid config accepted: %v", err)
			}
		})
	}
}

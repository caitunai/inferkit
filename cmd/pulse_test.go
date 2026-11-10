package cmd

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"

	"github.com/caitun/inferkit/services/configload"
	"github.com/caitun/inferkit/services/pulse"
)

const localPulseConfig = `
[pulse.server]
url="https://local-pulse.example.com/events"
secret="local-pulse-signing-secret-at-least-32-bytes"
audience="local-pulse"
[pulse.app]
enabled=true
issuer="local-inferkit"
tenantId=2
appId=4
platformId=5
timeout="3s"
tokenLifetime="10m"
maxConnections=8
maxResponseBytes=4096
`

const remotePulseConfig = `
[pulse]
[pulse.server]
audience="pulse"
secret="remote-pulse-signing-secret-at-least-32-bytes"
url="https://remote-pulse.example.com/api/pulse/events"
`

func TestPulseClientActivation(t *testing.T) {
	t.Parallel()
	v := viper.New()
	client, err := newPulseClient(t.Context(), v)
	if client != nil || err != nil {
		t.Fatal("Pulse must default to disabled")
	}
	v.Set("pulse.app.enabled", true)
	if _, err = newPulseClient(t.Context(), v); !errors.Is(err, pulse.ErrConfig) || !errors.Is(err, ErrServeCommand) {
		t.Fatalf("invalid enabled configuration must prevent startup: %v", err)
	}
	v.Set("pulse.server.url", "https://pulse.example.com/api/pulse/events")
	v.Set("pulse.server.secret", "test-secret-for-pulse-with-32-bytes")
	v.Set("pulse.server.audience", "pulse")
	client, err = newPulseClient(t.Context(), v)
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
}

func TestPulseRemoteConfigurationPreservesLocalApp(t *testing.T) {
	t.Parallel()
	for _, remoteTOML := range []string{"[pulse]\n", remotePulseConfig} {
		local := readPulseConfig(t, localPulseConfig)
		want := pulse.Configuration(local)
		remote := readPulseConfig(t, remoteTOML)
		if remote.IsSet("pulse.server") {
			want.URL = remote.GetString("pulse.server.url")
			want.Secret = remote.GetString("pulse.server.secret")
			want.Audience = remote.GetString("pulse.server.audience")
		}
		results := []configload.Result{{Config: remote.AllSettings()}}
		if err := mergePublishedConfigurationResults(local, results); err != nil {
			t.Fatal(err)
		}
		if pulse.Configuration(local) != want || !local.GetBool("pulse.app.enabled") {
			t.Fatal("remote Pulse settings replaced unrelated local app settings")
		}
		client, err := newPulseClient(t.Context(), local)
		if err != nil {
			t.Fatal(err)
		}
		client.Close()
	}
}

func TestPulseRemoteOverridesAndEnvironmentPrecedence(t *testing.T) {
	t.Setenv("PULSE_SERVER_SECRET", "environment-signing-secret-at-least-32-bytes")
	local := readPulseConfig(t, localPulseConfig)
	local.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	local.AutomaticEnv()
	remote := readPulseConfig(t, remotePulseConfig+"\n[pulse.app]\ntimeout=\"2s\"\n")
	if err := mergePublishedConfigurationResults(local, []configload.Result{{Config: remote.AllSettings()}}); err != nil {
		t.Fatal(err)
	}
	c := pulse.Configuration(local)
	if c.Timeout != 2*time.Second {
		t.Fatal("remote app setting must override the same local key")
	}
	if c.Secret != "environment-signing-secret-at-least-32-bytes" {
		t.Fatal("environment secret must take precedence over remote configuration")
	}
}

func readPulseConfig(t *testing.T, document string) *viper.Viper {
	t.Helper()
	v := viper.New()
	v.SetConfigType("toml")
	if err := v.ReadConfig(strings.NewReader(document)); err != nil {
		t.Fatal(err)
	}
	return v
}

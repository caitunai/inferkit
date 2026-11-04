package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/services/configload"
)

const (
	testConfigEnabledKey      = "enabled"
	testConfigFeatureKey      = "feature"
	testConfigSharedNamespace = "shared"
	testConfigNamespace       = "service"
	testConfigEnvironment     = "production"
)

func TestRootCommandLoadsPublishedConfigurationForChildCommands(t *testing.T) {
	t.Parallel()
	databaseSettings := configload.Settings{Targets: []configload.Target{{
		Source: configload.SourceDatabase, Namespace: testConfigNamespace, Environment: testConfigEnvironment,
	}}}
	httpSettings := configload.Settings{Targets: []configload.Target{{
		Source: configload.SourceHTTP, Namespace: testConfigNamespace, Environment: testConfigEnvironment,
	}}}
	mixedSettings := configload.Settings{Targets: []configload.Target{
		{Source: configload.SourceDatabase, Namespace: testConfigSharedNamespace, Environment: "base"},
		{Source: configload.SourceHTTP, Namespace: testConfigNamespace, Environment: testConfigEnvironment},
	}}

	if rootCmd.PersistentPreRunE == nil {
		t.Fatal("root command does not register the configuration hook")
	}
	if !cobra.EnableTraverseRunHooks {
		t.Fatal("Cobra parent hook traversal is disabled")
	}
	if !shouldLoadPublishedConfiguration(serveCmd, true, databaseSettings) ||
		!shouldLoadPublishedConfiguration(queueCmd, true, httpSettings) {
		t.Fatal("regular child commands do not load published configuration")
	}
	if shouldLoadPublishedConfiguration(generateConfigKeyCmd, true, databaseSettings) {
		t.Fatal("config-key generate must skip database configuration loading")
	}
	if !shouldLoadPublishedConfiguration(generateConfigKeyCmd, true, httpSettings) {
		t.Fatal("config-key generate unexpectedly skips HTTP configuration loading")
	}
	if shouldLoadPublishedConfiguration(generateConfigKeyCmd, true, mixedSettings) {
		t.Fatal("config-key generate must skip an atomic mixed-source target list")
	}
	if shouldLoadPublishedConfiguration(serveCmd, false, httpSettings) {
		t.Fatal("disabled published configuration unexpectedly loads")
	}
}

//nolint:cyclop // This end-to-end test keeps one setup and assertion lifecycle visible.
func TestMergePublishedConfigurationOverridesBusinessConfigAndProtectsBootstrap(t *testing.T) {
	t.Parallel()

	target := viper.New()
	target.SetConfigType("toml")
	if err := target.ReadConfig(strings.NewReader(`
[db]
host="bootstrap-db"
[configcrypt]
keyring="/external/keys.json"
[configcenter]
enabled=true
[configload]
enabled=true
[redis]
host="bootstrap-redis"
`)); err != nil {
		t.Fatalf("ReadConfig() error = %v", err)
	}
	remote := map[string]any{
		"DB":                 map[string]any{"host": "remote-db"},
		"configcrypt":        map[string]any{"keyring": "/remote/keys.json"},
		"configcenter":       map[string]any{testConfigEnabledKey: false},
		"configload":         map[string]any{testConfigEnabledKey: false},
		"redis":              map[string]any{"host": "remote-redis", "database": json.Number("3")},
		testConfigFeatureKey: map[string]any{"Enabled": true},
	}

	if err := mergePublishedConfiguration(target, remote); err != nil {
		t.Fatalf("mergePublishedConfiguration() error = %v", err)
	}
	if target.GetString("db.host") != "bootstrap-db" ||
		target.GetString("configcrypt.keyring") != "/external/keys.json" ||
		!target.GetBool("configcenter.enabled") || !target.GetBool("configload.enabled") {
		t.Fatal("mergePublishedConfiguration() replaced protected bootstrap settings")
	}
	if target.GetString("redis.host") != "remote-redis" || target.GetInt("redis.database") != 3 ||
		!target.GetBool(testConfigFeatureKey+"."+testConfigEnabledKey) {
		t.Fatal("mergePublishedConfiguration() did not merge remote business settings")
	}
	feature, ok := remote[testConfigFeatureKey].(map[string]any)
	if !ok {
		t.Fatalf("remote feature has type %T, want map[string]any", remote[testConfigFeatureKey])
	}
	if feature["Enabled"] != true {
		t.Fatal("mergePublishedConfiguration() mutated the source configuration")
	}
}

func TestPublishedConfigLoadSettingsReadsMixedTargetSourcesAndHTTPOverrides(t *testing.T) {
	t.Setenv("REMOTE_CONFIG_API_KEY", "remote-api-key")

	config := viper.New()
	config.SetConfigType("toml")
	if err := config.ReadConfig(strings.NewReader(`
[configload]
enabled=true

[[configload.targets]]
source="database"
namespace="shared"
environment="base"

[[configload.targets]]
source="http"
namespace="service"
environment="production"

[configload.targets.http]
baseURL="https://remote-config.example.test"
apiKeyEnv="REMOTE_CONFIG_API_KEY"
timeout="11s"
`)); err != nil {
		t.Fatalf("ReadConfig() error = %v", err)
	}

	settings, err := publishedConfigLoadSettings(config)
	if err != nil {
		t.Fatalf("publishedConfigLoadSettings() error = %v", err)
	}
	if len(settings.Targets) != 2 || settings.Targets[0].Source != configload.SourceDatabase {
		t.Fatalf("publishedConfigLoadSettings() targets = %#v", settings.Targets)
	}
	httpTarget := settings.Targets[1]
	if httpTarget.Source != configload.SourceHTTP ||
		httpTarget.HTTP.BaseURL != "https://remote-config.example.test" ||
		httpTarget.HTTP.APIKey != "remote-api-key" || httpTarget.HTTP.Timeout != 11*time.Second {
		t.Fatalf("publishedConfigLoadSettings() HTTP target = %#v", httpTarget)
	}
}

func TestPublishedConfigLoadSettingsDefaultsEachHTTPTimeout(t *testing.T) {
	t.Setenv("DEFAULT_TIMEOUT_API_KEY", "default-timeout-key")

	config := viper.New()
	config.Set("configload.targets", []map[string]any{{
		"source":      "http",
		"namespace":   testConfigNamespace,
		"environment": testConfigEnvironment,
		"http": map[string]any{
			"baseURL":   "https://config.example.test",
			"apiKeyEnv": "DEFAULT_TIMEOUT_API_KEY",
		},
	}})

	settings, err := publishedConfigLoadSettings(config)
	if err != nil {
		t.Fatalf("publishedConfigLoadSettings() error = %v", err)
	}
	if len(settings.Targets) != 1 || settings.Targets[0].HTTP.Timeout != defaultConfigLoadHTTPTimeout ||
		settings.Targets[0].HTTP.APIKey != "default-timeout-key" {
		t.Fatalf("publishedConfigLoadSettings() targets = %#v", settings.Targets)
	}
}

func TestMergePublishedConfigurationResultsUsesDeclarationOrder(t *testing.T) {
	t.Parallel()

	target := viper.New()
	results := []configload.Result{
		{Config: map[string]any{
			testConfigFeatureKey: map[string]any{
				testConfigEnabledKey: false,
				"owner":              testConfigSharedNamespace,
			},
			"servers": []any{testConfigSharedNamespace},
		}},
		{Config: map[string]any{
			testConfigFeatureKey: map[string]any{testConfigEnabledKey: true},
			"servers":            []any{testConfigNamespace, "backup"},
		}},
	}
	if err := mergePublishedConfigurationResults(target, results); err != nil {
		t.Fatalf("mergePublishedConfigurationResults() error = %v", err)
	}
	if !target.GetBool(testConfigFeatureKey+"."+testConfigEnabledKey) ||
		target.GetString(testConfigFeatureKey+".owner") != testConfigSharedNamespace {
		t.Fatalf("merged feature configuration = %#v", target.GetStringMap(testConfigFeatureKey))
	}
	servers := target.GetStringSlice("servers")
	if len(servers) != 2 || servers[0] != testConfigNamespace || servers[1] != "backup" {
		t.Fatalf("merged servers = %#v", servers)
	}
}

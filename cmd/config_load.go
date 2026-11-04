package cmd

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/db"
	"github.com/caitun/inferkit/services/configload"
)

const defaultConfigLoadHTTPTimeout = 5 * time.Second

var (
	// ErrPublishedConfigLoad indicates published configuration bootstrap failed.
	ErrPublishedConfigLoad = errors.New("published configuration bootstrap failed")
	// ErrPublishedConfigSettings indicates read published configuration loader settings failed.
	ErrPublishedConfigSettings = errors.New("read published configuration loader settings failed")
	// ErrPublishedConfigMerge indicates merge published configuration into Viper failed.
	ErrPublishedConfigMerge = errors.New("merge published configuration into Viper failed")
	// ErrPublishedConfigDB indicates load published configuration from database failed.
	ErrPublishedConfigDB    = errors.New("load published configuration from database failed")
	protectedBootstrapRoots = map[string]struct{}{
		"configcenter": {},
		"configcrypt":  {},
		"configload":   {},
		"db":           {},
	}
)

func loadPublishedConfiguration(ctx context.Context, settings configload.Settings) error {
	results, err := configload.LoadAll(ctx, settings, loadPublishedConfigurationFromDatabase)
	if err != nil {
		return errors.Join(ErrPublishedConfigLoad, err)
	}
	if mergeErr := mergePublishedConfigurationResults(viper.GetViper(), results); mergeErr != nil {
		return errors.Join(ErrPublishedConfigLoad, mergeErr)
	}
	for index, result := range results {
		log.Info().
			Int("target_index", index+1).
			Int("target_count", len(results)).
			Str("source", string(result.Source)).
			Str("namespace", result.Namespace).
			Str("environment", result.Environment).
			Uint64("version", result.Version).
			Msg("published configuration loaded")
	}
	return nil
}

func publishedConfigLoadSettings(config *viper.Viper) (configload.Settings, error) {
	targets := make([]configload.Target, 0)
	if err := config.UnmarshalKey("configload.targets", &targets); err != nil {
		return configload.Settings{}, errors.Join(ErrPublishedConfigSettings, err)
	}
	for index := range targets {
		applyHTTPConfigLoadDefaults(&targets[index])
	}
	return configload.Settings{
		Targets: targets,
	}, nil
}

func applyHTTPConfigLoadDefaults(target *configload.Target) {
	if !strings.EqualFold(strings.TrimSpace(string(target.Source)), string(configload.SourceHTTP)) {
		return
	}
	if target.HTTP.Timeout == 0 {
		target.HTTP.Timeout = defaultConfigLoadHTTPTimeout
	}
	apiKeyEnv := strings.TrimSpace(target.HTTP.APIKeyEnv)
	if apiKeyEnv == "" {
		return
	}
	target.HTTP.APIKey = os.Getenv(apiKeyEnv)
	if target.HTTP.APIKey == "" {
		target.HTTP.APIKey = apiKeyEnv
	}
}

func loadPublishedConfigurationFromDatabase(
	ctx context.Context,
	namespaceSlug string,
	environmentSlug string,
) (*configload.Result, error) {
	_, _, published, err := db.LatestPublishedConfigBySlugsInternal(ctx, namespaceSlug, environmentSlug)
	if err != nil {
		return nil, errors.Join(ErrPublishedConfigDB, err)
	}
	return &configload.Result{
		Config:  published.Config,
		Version: published.Version,
	}, nil
}

func mergePublishedConfiguration(target *viper.Viper, config map[string]any) error {
	filtered := make(map[string]any, len(config))
	for key, value := range config {
		if _, protected := protectedBootstrapRoots[strings.ToLower(key)]; protected {
			continue
		}
		filtered[key] = clonePublishedConfigValue(value)
	}
	if err := target.MergeConfigMap(filtered); err != nil {
		return errors.Join(ErrPublishedConfigMerge, err)
	}
	return nil
}

func mergePublishedConfigurationResults(target *viper.Viper, results []configload.Result) error {
	staged := viper.New()
	for _, result := range results {
		if err := mergePublishedConfiguration(staged, result.Config); err != nil {
			return err
		}
	}
	return mergePublishedConfiguration(target, staged.AllSettings())
}

func clonePublishedConfigValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, child := range typed {
			cloned[key] = clonePublishedConfigValue(child)
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for index, child := range typed {
			cloned[index] = clonePublishedConfigValue(child)
		}
		return cloned
	default:
		return typed
	}
}

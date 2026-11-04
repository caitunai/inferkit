// Package configload loads immutable published configuration into the
// application's Viper bootstrap process.
package configload

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
)

// Source represents source data.
type Source string

const (
	// SourceDatabase identifies the "database" value.
	SourceDatabase Source = "database"
	// SourceHTTP identifies the "http" value.
	SourceHTTP     Source = "http"
	maxHTTPBody           = 2 * 1024 * 1024
	maxHTTPTimeout        = time.Minute
)

var (
	// ErrLoad indicates load published configuration failed.
	ErrLoad = errors.New("load published configuration failed")
	// ErrInvalidSettings indicates invalid published configuration loader settings.
	ErrInvalidSettings = errors.New("invalid published configuration loader settings")
	// ErrTargetsRequired indicates configload.targets must contain at least one target.
	ErrTargetsRequired = errors.New("configload.targets must contain at least one target")
	// ErrTargetDuplicate indicates configload.targets contains a duplicate source endpoint, namespace, and environment.
	ErrTargetDuplicate = errors.New("configload.targets contains a duplicate source endpoint, namespace, and environment")
	// ErrNamespaceInvalid indicates configload target namespace must be a valid namespace identifier.
	ErrNamespaceInvalid = errors.New("configload target namespace must be a valid namespace identifier")
	// ErrEnvironmentInvalid indicates configload target environment must be a valid environment identifier.
	ErrEnvironmentInvalid = errors.New("configload target environment must be a valid environment identifier")
	// ErrUnsupportedSource indicates every configload target source must be database or http.
	ErrUnsupportedSource = errors.New("every configload target source must be database or http")
	// ErrHTTPBaseURLInvalid indicates HTTP target baseURL must be an absolute HTTP or HTTPS URL without credentials, query, or fragment.
	ErrHTTPBaseURLInvalid = errors.New("HTTP target baseURL must be an absolute HTTP or HTTPS URL without credentials, query, or fragment")
	// ErrHTTPAPIKeyRequired indicates an API key is required for every HTTP configload target.
	ErrHTTPAPIKeyRequired = errors.New("an API key is required for every HTTP configload target")
	// ErrHTTPTimeoutInvalid indicates HTTP target timeout must be greater than zero and at most one minute.
	ErrHTTPTimeoutInvalid = errors.New("HTTP target timeout must be greater than zero and at most one minute")
	// ErrDatabaseLoader indicates database configuration loader is unavailable.
	ErrDatabaseLoader = errors.New("database configuration loader is unavailable")
	// ErrHTTP indicates published configuration HTTP request failed.
	ErrHTTP = errors.New("published configuration HTTP request failed")
	// ErrInvalidResponse indicates invalid published configuration response.
	ErrInvalidResponse = errors.New("invalid published configuration response")
	configSlugPattern  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
)

// Settings represents settings data.
type Settings struct {
	Targets []Target
}

// Target identifies one immutable published environment and the source used to
// retrieve it. API keys are populated from trusted bootstrap configuration and
// are never included in errors.
type Target struct {
	Source      Source       `mapstructure:"source"`
	Namespace   string       `mapstructure:"namespace"`
	Environment string       `mapstructure:"environment"`
	HTTP        HTTPSettings `mapstructure:"http"`
}

// HTTPSettings represents http settings data.
type HTTPSettings struct {
	BaseURL   string        `mapstructure:"baseURL"`
	APIKey    string        `mapstructure:"-"`
	APIKeyEnv string        `mapstructure:"apiKeyEnv"`
	Timeout   time.Duration `mapstructure:"timeout"`
}

// Result represents result data.
type Result struct {
	Config      map[string]any
	Source      Source
	Namespace   string
	Environment string
	Version     uint64
}

// DatabaseLoader represents database loader data.
type DatabaseLoader func(context.Context, string, string) (*Result, error)

// HTTPStatusError represents http status error data.
type HTTPStatusError struct {
	StatusCode int
}

// TargetError represents target error data.
type TargetError struct {
	Err         error
	Source      Source
	Namespace   string
	Environment string
	Index       int
}

func (e *TargetError) Error() string {
	return "load config target " + strconv.Itoa(e.Index+1) + " (" + string(e.Source) + ":" +
		e.Namespace + "/" + e.Environment + "): " + e.Err.Error()
}

func (e *TargetError) Unwrap() error { return e.Err }

func (e *HTTPStatusError) Error() string {
	return "published configuration HTTP status " + strconv.Itoa(e.StatusCode)
}

// LoadAll loads every target in declaration order. Loading stops on the first
// failure so callers never merge a partially loaded target set.
func LoadAll(ctx context.Context, settings Settings, databaseLoader DatabaseLoader) ([]Result, error) {
	settings = normalizeSettings(settings)
	if err := validateSettings(settings, databaseLoader); err != nil {
		return nil, errors.Join(ErrLoad, err)
	}
	results := make([]Result, 0, len(settings.Targets))
	for index, target := range settings.Targets {
		result, err := loadTarget(ctx, target, databaseLoader)
		if err != nil {
			return nil, errors.Join(ErrLoad, newTargetError(err, target, index))
		}
		if result == nil || result.Config == nil {
			return nil, errors.Join(ErrLoad, newTargetError(ErrInvalidResponse, target, index))
		}
		result.Source = target.Source
		result.Namespace = target.Namespace
		result.Environment = target.Environment
		results = append(results, *result)
	}
	return results, nil
}

func normalizeSettings(settings Settings) Settings {
	targets := make([]Target, len(settings.Targets))
	for index, target := range settings.Targets {
		targets[index] = normalizeTarget(target)
	}
	settings.Targets = targets
	return settings
}

func normalizeTarget(target Target) Target {
	target.Source = Source(strings.ToLower(strings.TrimSpace(string(target.Source))))
	target.Namespace = strings.ToLower(strings.TrimSpace(target.Namespace))
	target.Environment = strings.ToLower(strings.TrimSpace(target.Environment))
	target.HTTP = normalizeHTTPSettings(target.HTTP)
	return target
}

func normalizeHTTPSettings(settings HTTPSettings) HTTPSettings {
	settings.BaseURL = strings.TrimRight(strings.TrimSpace(settings.BaseURL), "/")
	settings.APIKey = strings.TrimSpace(settings.APIKey)
	settings.APIKeyEnv = strings.TrimSpace(settings.APIKeyEnv)
	return settings
}

func validateSettings(settings Settings, databaseLoader DatabaseLoader) error {
	if len(settings.Targets) == 0 {
		return errors.Join(ErrInvalidSettings, ErrTargetsRequired)
	}
	seen := make(map[string]struct{}, len(settings.Targets))
	for index, target := range settings.Targets {
		if err := validateTarget(target, databaseLoader); err != nil {
			return errors.Join(ErrInvalidSettings, newTargetError(err, target, index))
		}
		key := targetIdentity(target)
		if _, exists := seen[key]; exists {
			return errors.Join(ErrInvalidSettings, newTargetError(ErrTargetDuplicate, target, index))
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateTarget(target Target, databaseLoader DatabaseLoader) error {
	if !configSlugPattern.MatchString(target.Namespace) {
		return ErrNamespaceInvalid
	}
	if !configSlugPattern.MatchString(target.Environment) {
		return ErrEnvironmentInvalid
	}
	switch target.Source {
	case SourceDatabase:
		if databaseLoader == nil {
			return ErrDatabaseLoader
		}
		return nil
	case SourceHTTP:
		return validateHTTPSettings(target.HTTP)
	default:
		return ErrUnsupportedSource
	}
}

func validateHTTPSettings(settings HTTPSettings) error {
	if settings.APIKey == "" {
		return ErrHTTPAPIKeyRequired
	}
	if settings.Timeout <= 0 || settings.Timeout > maxHTTPTimeout {
		return ErrHTTPTimeoutInvalid
	}
	baseURL, err := url.Parse(settings.BaseURL)
	if err != nil || !validHTTPBaseURL(baseURL) {
		return ErrHTTPBaseURLInvalid
	}
	return nil
}

func validHTTPBaseURL(baseURL *url.URL) bool {
	if baseURL.Host == "" || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return false
	}
	return baseURL.Scheme == "http" || baseURL.Scheme == "https"
}

func targetIdentity(target Target) string {
	identity := string(target.Source) + "\x00" + target.Namespace + "\x00" + target.Environment
	if target.Source == SourceHTTP {
		identity += "\x00" + target.HTTP.BaseURL
	}
	return identity
}

func newTargetError(err error, target Target, index int) *TargetError {
	return &TargetError{
		Err: err, Source: target.Source, Namespace: target.Namespace, Environment: target.Environment, Index: index,
	}
}

// UsesSource reports whether the target list contains the requested source.
func UsesSource(settings Settings, source Source) bool {
	settings = normalizeSettings(settings)
	for _, target := range settings.Targets {
		if target.Source == source {
			return true
		}
	}
	return false
}

func loadTarget(ctx context.Context, target Target, databaseLoader DatabaseLoader) (*Result, error) {
	switch target.Source {
	case SourceDatabase:
		return databaseLoader(ctx, target.Namespace, target.Environment)
	case SourceHTTP:
		return loadHTTP(ctx, newHTTPClient(target.HTTP), target.HTTP.BaseURL, target)
	default:
		return nil, ErrUnsupportedSource
	}
}

type httpEnvelope struct {
	Message string           `json:"message"`
	Data    httpResponseData `json:"data"`
	Status  int              `json:"status"`
}

type httpResponseData struct {
	Config  map[string]any `json:"config"`
	Version uint64         `json:"version"`
}

func newHTTPClient(settings HTTPSettings) *resty.Client {
	return resty.New().
		SetTimeout(settings.Timeout).
		SetResponseBodyLimit(maxHTTPBody).
		SetRedirectPolicy(resty.NoRedirectPolicy()).
		SetCloseConnection(true).
		SetJSONUnmarshaler(unmarshalJSONNumber)
}

func loadHTTP(ctx context.Context, client *resty.Client, baseURL string, target Target) (*Result, error) {
	endpoint, err := url.JoinPath(
		baseURL,
		"config-center/api/runtime",
		target.Namespace,
		target.Environment,
	)
	if err != nil {
		return nil, errors.Join(ErrHTTP, err)
	}
	envelope := &httpEnvelope{}
	response, err := client.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json").
		SetHeader("X-API-Key", target.HTTP.APIKey).
		SetQueryParam("format", "json").
		SetResult(envelope).
		Get(endpoint)
	if err != nil {
		return nil, errors.Join(ErrHTTP, err)
	}
	if !response.IsSuccess() {
		return nil, errors.Join(ErrHTTP, &HTTPStatusError{StatusCode: response.StatusCode()})
	}
	if envelope.Status != 0 || envelope.Data.Config == nil {
		return nil, errors.Join(ErrHTTP, ErrInvalidResponse)
	}
	return &Result{Config: envelope.Data.Config, Version: envelope.Data.Version}, nil
}

func unmarshalJSONNumber(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return errors.Join(ErrInvalidResponse, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return ErrInvalidResponse
		}
		return errors.Join(ErrInvalidResponse, err)
	}
	return nil
}

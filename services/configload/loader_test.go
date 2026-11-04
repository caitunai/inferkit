package configload

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const (
	testNamespace       = "service"
	testEnvironment     = "production"
	testSharedNamespace = "shared"
	testBaseEnvironment = "base"
	testAPIKey          = "test-api-key-value" // #nosec G101 -- non-production test fixture.
)

var errTestTargetFailure = errors.New("test target failure")

func missingDatabaseResult(context.Context, string, string) (*Result, error) {
	return nil, nil //nolint:nilnil // Deliberately simulate a loader that violates its result contract.
}

func TestLoadAllFromDatabase(t *testing.T) {
	t.Parallel()

	called := false
	results, err := LoadAll(t.Context(), Settings{
		Targets: []Target{{
			Source: SourceDatabase, Namespace: testNamespace, Environment: testEnvironment,
		}},
	}, func(_ context.Context, namespace, environment string) (*Result, error) {
		called = true
		if namespace != testNamespace || environment != testEnvironment {
			t.Fatalf("database loader identifiers = %s/%s", namespace, environment)
		}
		return &Result{Config: map[string]any{"enabled": true}, Version: 4}, nil
	})
	if err != nil {
		t.Fatalf("LoadAll() error = %v", err)
	}
	result := results[0]
	if !called || result.Source != SourceDatabase || result.Version != 4 {
		t.Fatalf("LoadAll() result = %#v, called = %t", result, called)
	}
}

//nolint:gocognit // This end-to-end test keeps one setup and assertion lifecycle visible.
func TestLoadAllFromHTTP(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config-center/api/runtime/service/production" {
			t.Errorf("request path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("format") != "json" {
			t.Errorf("format = %q", r.URL.Query().Get("format"))
		}
		if r.Header.Get("X-Api-Key") != testAPIKey {
			t.Error("request API Key does not match")
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"status":0,"message":"ok","data":{"version":7,"config":{"port":8080,"ratio":1.5}}}`)); err != nil {
			t.Errorf("response write error = %v", err)
		}
	}))
	defer server.Close()

	results, err := LoadAll(t.Context(), validHTTPSettings(server.URL), nil)
	if err != nil {
		t.Fatalf("LoadAll() error = %v", err)
	}
	result := results[0]
	if result.Source != SourceHTTP || result.Version != 7 {
		t.Fatalf("LoadAll() result = %#v", result)
	}
	if result.Config["port"] != json.Number("8080") || result.Config["ratio"] != json.Number("1.5") {
		t.Fatalf("LoadAll() numeric values = %#v", result.Config)
	}
}

func TestLoadAllFromDatabasePreservesTargetOrder(t *testing.T) {
	t.Parallel()

	targets := []Target{
		{Source: SourceDatabase, Namespace: testSharedNamespace, Environment: testBaseEnvironment},
		{Source: SourceDatabase, Namespace: testNamespace, Environment: testEnvironment},
		{Source: SourceDatabase, Namespace: testNamespace, Environment: "region-cn"},
	}
	loaded := make([]string, 0, len(targets))
	results, err := LoadAll(t.Context(), Settings{
		Targets: targets,
	}, func(_ context.Context, namespace, environment string) (*Result, error) {
		loaded = append(loaded, namespace+"/"+environment)
		return &Result{
			Config:  map[string]any{"target": namespace + "/" + environment},
			Version: uint64(len(loaded)),
		}, nil
	})
	if err != nil {
		t.Fatalf("LoadAll() error = %v", err)
	}
	for index, target := range targets {
		identifier := target.Namespace + "/" + target.Environment
		if loaded[index] != identifier || results[index].Namespace != target.Namespace ||
			results[index].Environment != target.Environment || results[index].Version != uint64(index+1) {
			t.Fatalf("LoadAll() result[%d] = %#v, loaded = %#v", index, results[index], loaded)
		}
	}
}

func TestLoadAllFromHTTPUsesTargetAPIKeys(t *testing.T) {
	t.Parallel()

	expectedKeys := map[string]string{
		"/config-center/api/runtime/" + testSharedNamespace + "/" + testBaseEnvironment: "shared-api-key",
		"/config-center/api/runtime/" + testNamespace + "/" + testEnvironment:           "service-api-key",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedKey, exists := expectedKeys[r.URL.Path]
		if !exists {
			t.Errorf("unexpected request path = %q", r.URL.Path)
		}
		if r.Header.Get("X-Api-Key") != expectedKey {
			t.Errorf("request API Key for %s does not match", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"status":0,"message":"ok","data":{"version":1,"config":{"enabled":true}}}`)); err != nil {
			t.Errorf("response write error = %v", err)
		}
	}))
	defer server.Close()

	results, err := LoadAll(t.Context(), Settings{
		Targets: []Target{
			{
				Source: SourceHTTP, Namespace: testSharedNamespace, Environment: testBaseEnvironment,
				HTTP: HTTPSettings{BaseURL: server.URL, APIKey: "shared-api-key", Timeout: time.Second},
			},
			{
				Source: SourceHTTP, Namespace: testNamespace, Environment: testEnvironment,
				HTTP: HTTPSettings{BaseURL: server.URL, APIKey: "service-api-key", Timeout: time.Second},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("LoadAll() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("LoadAll() result count = %d", len(results))
	}
}

//nolint:cyclop,gocognit // This end-to-end test keeps one setup and assertion lifecycle visible.
func TestLoadAllSupportsMixedSourcesAndIndependentHTTPServers(t *testing.T) {
	t.Parallel()

	newServer := func(expectedPath, expectedAPIKey, origin string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != expectedPath {
				t.Errorf("request path = %q, want %q", r.URL.Path, expectedPath)
			}
			if r.Header.Get("X-Api-Key") != expectedAPIKey {
				t.Errorf("request API Key for %s does not match", origin)
			}
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{"status":0,"message":"ok","data":{"version":2,"config":{"origin":"` + origin + `"}}}`)); err != nil {
				t.Errorf("response write error = %v", err)
			}
		}))
	}
	primaryServer := newServer(
		"/config-center/api/runtime/service/production",
		"primary-api-key",
		"primary",
	)
	defer primaryServer.Close()
	backupServer := newServer(
		"/config-center/api/runtime/service/production",
		"backup-api-key",
		"backup",
	)
	defer backupServer.Close()

	results, err := LoadAll(t.Context(), Settings{
		Targets: []Target{
			{Source: SourceDatabase, Namespace: testSharedNamespace, Environment: testBaseEnvironment},
			{
				Source: SourceHTTP, Namespace: testNamespace, Environment: testEnvironment,
				HTTP: HTTPSettings{BaseURL: primaryServer.URL, APIKey: "primary-api-key", Timeout: time.Second},
			},
			{
				Source: SourceHTTP, Namespace: testNamespace, Environment: testEnvironment,
				HTTP: HTTPSettings{BaseURL: backupServer.URL, APIKey: "backup-api-key", Timeout: 2 * time.Second},
			},
		},
	}, func(_ context.Context, namespace, environment string) (*Result, error) {
		if namespace != testSharedNamespace || environment != testBaseEnvironment {
			t.Fatalf("database loader identifiers = %s/%s", namespace, environment)
		}
		return &Result{Config: map[string]any{"origin": "database"}, Version: 1}, nil
	})
	if err != nil {
		t.Fatalf("LoadAll() error = %v", err)
	}
	if len(results) != 3 || results[0].Source != SourceDatabase || results[1].Source != SourceHTTP ||
		results[2].Source != SourceHTTP || results[0].Config["origin"] != "database" ||
		results[1].Config["origin"] != "primary" || results[2].Config["origin"] != "backup" {
		t.Fatalf("LoadAll() results = %#v", results)
	}
}

func TestLoadAllFromHTTPRejectsErrorStatus(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	_, err := LoadAll(t.Context(), validHTTPSettings(server.URL), nil)
	if !errors.Is(err, ErrHTTP) {
		t.Fatalf("LoadAll() error = %v, want ErrHTTP", err)
	}
	statusError := &HTTPStatusError{}
	if !errors.As(err, &statusError) || statusError.StatusCode != http.StatusUnauthorized {
		t.Fatalf("LoadAll() error = %v, want HTTP 401 classification", err)
	}
}

//nolint:funlen // This end-to-end test keeps one setup and assertion lifecycle visible.
func TestLoadAllRejectsInvalidSettingsAndResults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr  error
		name     string
		loader   DatabaseLoader
		settings Settings
	}{
		{
			name: "invalid namespace",
			settings: Settings{
				Targets: []Target{{
					Source: SourceDatabase, Namespace: "Invalid Namespace", Environment: testEnvironment,
				}},
			},
			wantErr: ErrNamespaceInvalid,
		},
		{
			name: "missing source",
			settings: Settings{Targets: []Target{{
				Namespace: testNamespace, Environment: testEnvironment,
			}}},
			wantErr: ErrUnsupportedSource,
		},
		{
			name: "unsupported source",
			settings: Settings{
				Targets: []Target{{Source: "file", Namespace: testNamespace, Environment: testEnvironment}},
			},
			wantErr: ErrUnsupportedSource,
		},
		{
			name: "database loader is missing",
			settings: Settings{
				Targets: []Target{{
					Source: SourceDatabase, Namespace: testNamespace, Environment: testEnvironment,
				}},
			},
			wantErr: ErrDatabaseLoader,
		},
		{
			name: "database result is missing",
			settings: Settings{
				Targets: []Target{{
					Source: SourceDatabase, Namespace: testNamespace, Environment: testEnvironment,
				}},
			},
			loader:  missingDatabaseResult,
			wantErr: ErrInvalidResponse,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := LoadAll(t.Context(), tt.settings, tt.loader)
			if !errors.Is(err, ErrLoad) || !errors.Is(err, tt.wantErr) {
				t.Fatalf("LoadAll() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoadAllRejectsMissingAndDuplicateTargets(t *testing.T) {
	t.Parallel()

	loader := func(context.Context, string, string) (*Result, error) {
		return &Result{Config: map[string]any{}}, nil
	}
	_, err := LoadAll(t.Context(), Settings{}, loader)
	if !errors.Is(err, ErrTargetsRequired) {
		t.Fatalf("LoadAll() missing-target error = %v", err)
	}

	_, err = LoadAll(t.Context(), Settings{
		Targets: []Target{
			{Source: SourceDatabase, Namespace: testNamespace, Environment: testEnvironment},
			{Source: " DATABASE ", Namespace: " SERVICE ", Environment: "Production"},
		},
	}, loader)
	if !errors.Is(err, ErrTargetDuplicate) {
		t.Fatalf("LoadAll() duplicate-target error = %v", err)
	}
	targetError := &TargetError{}
	if !errors.As(err, &targetError) || targetError.Index != 1 {
		t.Fatalf("LoadAll() duplicate target metadata = %#v", targetError)
	}
}

func TestLoadAllReturnsTargetMetadataOnFailure(t *testing.T) {
	t.Parallel()

	loadError := errTestTargetFailure
	_, err := LoadAll(t.Context(), Settings{
		Targets: []Target{
			{Source: SourceDatabase, Namespace: testSharedNamespace, Environment: testBaseEnvironment},
			{Source: SourceDatabase, Namespace: testNamespace, Environment: testEnvironment},
		},
	}, func(_ context.Context, namespace, _ string) (*Result, error) {
		if namespace == testNamespace {
			return nil, loadError
		}
		return &Result{Config: map[string]any{}}, nil
	})
	if !errors.Is(err, loadError) {
		t.Fatalf("LoadAll() error = %v, want target cause", err)
	}
	targetError := &TargetError{}
	if !errors.As(err, &targetError) || targetError.Index != 1 ||
		targetError.Namespace != testNamespace || targetError.Environment != testEnvironment {
		t.Fatalf("LoadAll() target error = %#v", targetError)
	}
}

func validHTTPSettings(baseURL string) Settings {
	return Settings{
		Targets: []Target{{
			Source: SourceHTTP, Namespace: testNamespace, Environment: testEnvironment,
			HTTP: HTTPSettings{BaseURL: baseURL, APIKey: testAPIKey, Timeout: time.Second},
		}},
	}
}

package db

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/caitun/inferkit/services/configcrypt"
)

const (
	testBase               = "base"
	testFlags              = "flags"
	testHost               = "host"
	testPort               = "port"
	testProd               = "prod"
	testRegions            = "regions"
	testService            = "service"
	testServiceHostPointer = "/service/host"
)

//nolint:cyclop,gocognit // This end-to-end test keeps one setup and assertion lifecycle visible.
func TestNamespaceAPIKeyIsEncryptedAndAuthenticated(t *testing.T) {
	keyringPath := filepath.Join(t.TempDir(), "keys.json")
	if err := configcrypt.GenerateFileKey(keyringPath, "namespace-test-key"); err != nil {
		t.Fatalf("GenerateFileKey() error = %v", err)
	}
	if err := configcrypt.Configure(configcrypt.Settings{
		Enabled:     true,
		Provider:    configcrypt.ProviderFile,
		ActiveKeyID: "namespace-test-key",
		KeyringPath: keyringPath,
	}); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	t.Cleanup(func() {
		if err := configcrypt.Configure(configcrypt.Settings{}); err != nil {
			t.Fatalf("reset Configure() error = %v", err)
		}
	})

	apiKey := strings.Repeat("a", minConfigAPIKeyLength)
	stored, err := encryptNamespaceAPIKey(apiKey, 7)
	if err != nil {
		t.Fatalf("encryptNamespaceAPIKey() error = %v", err)
	}
	if stored == apiKey || strings.Contains(stored, apiKey) {
		t.Fatal("encryptNamespaceAPIKey() stored plaintext API key")
	}
	namespace := &ConfigNamespace{ID: 7, APIKey: stored}
	publicView, err := json.Marshal(namespace)
	if err != nil {
		t.Fatalf("Marshal() namespace error = %v", err)
	}
	if strings.Contains(string(publicView), stored) || strings.Contains(string(publicView), apiKey) {
		t.Fatal("ConfigNamespace JSON exposed API key material")
	}
	if operationErr := authenticateNamespaceAPIKey(namespace, apiKey); operationErr != nil {
		t.Fatalf("authenticateNamespaceAPIKey() error = %v", operationErr)
	}
	if operationErr := authenticateNamespaceAPIKey(namespace, strings.Repeat("b", minConfigAPIKeyLength)); !errors.Is(operationErr, ErrConfigAPIKeyUnauthorized) {
		t.Fatalf("authenticateNamespaceAPIKey() error = %v, want ErrConfigAPIKeyUnauthorized", operationErr)
	}
}

func TestValidateNamespaceAPIKey(t *testing.T) {
	t.Parallel()
	base := ConfigNamespaceInput{Name: "Service", Slug: testService}
	if err := validateConfigNamespaceInput(base, true); !errors.Is(err, ErrConfigAPIKeyInvalid) {
		t.Fatalf("validateConfigNamespaceInput() missing key error = %v", err)
	}
	base.APIKey = strings.Repeat("a", minConfigAPIKeyLength)
	if err := validateConfigNamespaceInput(base, true); err != nil {
		t.Fatalf("validateConfigNamespaceInput() error = %v", err)
	}
	base.APIKey = strings.Repeat("a", minConfigAPIKeyLength-1)
	if err := validateConfigNamespaceInput(base, false); !errors.Is(err, ErrConfigAPIKeyInvalid) {
		t.Fatalf("validateConfigNamespaceInput() short key error = %v", err)
	}
}

func TestDecodeConfigPreservesSupportedTypes(t *testing.T) {
	t.Parallel()

	config, err := DecodeConfig([]byte(`{
		"object":{"enabled":true},
		"array":["a",2,false],
		"string":"value",
		"bool":true,
		"int":42,
		"float":3.14
	}`))
	if err != nil {
		t.Fatalf("DecodeConfig() error = %v", err)
	}
	if got := config["int"]; got != json.Number("42") {
		t.Fatalf("int = %#v, want json.Number(42)", got)
	}
	if got := config["float"]; got != json.Number("3.14") {
		t.Fatalf("float = %#v, want json.Number(3.14)", got)
	}
}

func TestDecodeConfigRejectsUnsupportedValues(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"null":           `{"value":null}`,
		"non-object":     `[1,2,3]`,
		"empty key":      `{" ":true}`,
		"multiple roots": `{} {}`,
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := DecodeConfig([]byte(raw))
			if !errors.Is(err, ErrConfigInvalid) {
				t.Fatalf("DecodeConfig() error = %v, want ErrConfigInvalid", err)
			}
		})
	}
}

func TestDecodeConfigRejectsExcessiveDepth(t *testing.T) {
	t.Parallel()

	raw := strings.Repeat(`{"nested":`, maxConfigDepth+1) + `true` + strings.Repeat("}", maxConfigDepth+1)
	_, err := DecodeConfig([]byte(raw))
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("DecodeConfig() error = %v, want ErrConfigInvalid", err)
	}
}

func TestMergeConfigRecursivelyMergesObjectsAndReplacesArrays(t *testing.T) {
	t.Parallel()

	base := map[string]any{
		testService: map[string]any{testHost: testBase, testPort: json.Number("80")},
		testFlags:   []any{testBase},
		"keep":      true,
	}
	override := map[string]any{
		testService: map[string]any{testPort: json.Number("443")},
		testFlags:   []any{"child", "replacement"},
	}
	want := map[string]any{
		testService: map[string]any{testHost: testBase, testPort: json.Number("443")},
		testFlags:   []any{"child", "replacement"},
		"keep":      true,
	}
	got := mergeConfig(base, override)
	gotJSON, gotErr := json.Marshal(got)
	wantJSON, wantErr := json.Marshal(want)
	if gotErr != nil || wantErr != nil || string(gotJSON) != string(wantJSON) {
		t.Fatalf("mergeConfig() = %#v, want %#v", got, want)
	}
	mapValue(t, mapValue(t, got)[testService])[testHost] = "changed"
	if mapValue(t, base[testService])[testHost] != testBase {
		t.Fatal("mergeConfig() mutated the base configuration")
	}
}

func TestDecodeConfigDescriptionsValidatesJSONPointers(t *testing.T) {
	t.Parallel()

	config, err := DecodeConfig([]byte(`{"service/api":{"host":"localhost"},"regions":["cn"]}`))
	if err != nil {
		t.Fatalf("DecodeConfig() error = %v", err)
	}
	descriptions, err := DecodeConfigDescriptions(
		[]byte(`{"/service~1api":" service settings ","/service~1api/host":"host","/regions/0":"China"}`),
		config,
	)
	if err != nil {
		t.Fatalf("DecodeConfigDescriptions() error = %v", err)
	}
	if descriptions["/service~1api"] != "service settings" {
		t.Fatalf("description = %q, want normalized value", descriptions["/service~1api"])
	}

	_, err = DecodeConfigDescriptions([]byte(`{"/regions/1":"missing"}`), config)
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("DecodeConfigDescriptions() error = %v, want ErrConfigInvalid", err)
	}
}

func TestMergeConfigWithDescriptionsReplacesDescriptionSubtrees(t *testing.T) {
	t.Parallel()

	base := map[string]any{
		testService: map[string]any{testHost: testBase, testPort: json.Number("80")},
		testRegions: []any{"cn", "us"},
	}
	override := map[string]any{
		testService: map[string]any{testHost: testProd},
		testRegions: []any{"cn"},
	}
	merged, descriptions := mergeConfigWithDescriptions(
		base,
		ConfigDescriptions{
			"/service":             "service",
			testServiceHostPointer: "base host",
			"/regions/1":           "US",
		},
		override,
		ConfigDescriptions{
			testServiceHostPointer: "production host",
			"/regions":             testRegions,
			"/regions/0":           "China",
		},
		"",
	)
	want := ConfigDescriptions{
		"/service":             "service",
		testServiceHostPointer: "production host",
		"/regions":             testRegions,
		"/regions/0":           "China",
	}
	if !equalDescriptions(descriptions, want) {
		t.Fatalf("descriptions = %#v, want %#v", descriptions, want)
	}
	if mapValue(t, mapValue(t, merged)[testService])[testPort] != json.Number("80") {
		t.Fatal("mergeConfigWithDescriptions() did not preserve inherited object fields")
	}
}

func equalDescriptions(left, right ConfigDescriptions) bool {
	if len(left) != len(right) {
		return false
	}
	for pointer, description := range left {
		if right[pointer] != description {
			return false
		}
	}
	return true
}

func mapValue(t *testing.T, value any) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("value has type %T, want map[string]any", value)
	}
	return result
}

func TestResolveConfigFromEnvironmentsBuildsRootToLeafChain(t *testing.T) {
	t.Parallel()

	environments := []ConfigEnvironment{
		{ID: 3, ParentID: 2, Name: testProd, DraftConfig: `{"service":{"host":"prod"}}`},
		{ID: 1, Name: testBase, DraftConfig: `{"service":{"host":"base","port":80},"enabled":true}`},
		{ID: 2, ParentID: 1, Name: "region", DraftConfig: `{"service":{"port":8080}}`},
	}
	resolved, err := resolveConfigFromEnvironments(environments, 3)
	if err != nil {
		t.Fatalf("resolveConfigFromEnvironments() error = %v", err)
	}
	if got := []uint{resolved.Chain[0].ID, resolved.Chain[1].ID, resolved.Chain[2].ID}; !slices.Equal(got, []uint{1, 2, 3}) {
		t.Fatalf("chain IDs = %v, want [1 2 3]", got)
	}
	service := mapValue(t, resolved.Config[testService])
	if service[testHost] != testProd || service[testPort] != json.Number("8080") {
		t.Fatalf("resolved service = %#v", service)
	}
}

func TestApplyConfigEnvironmentReleaseStateDetectsDraftChanges(t *testing.T) {
	t.Parallel()

	environments := []ConfigEnvironment{
		{ID: 1, DraftConfig: `{"service":{"host":"base"}}`, DraftDescriptions: `{}`},
		{ID: 2, ParentID: 1, DraftConfig: `{"service":{"port":8080}}`, DraftDescriptions: `{}`},
		{ID: 3, DraftConfig: `{}`, DraftDescriptions: `{}`},
	}
	releases := []ConfigRelease{
		{
			EnvironmentID: 1,
			Version:       2,
			Config:        `{"service":{"host":"base"}}`,
			Descriptions:  `{}`,
		},
		{
			EnvironmentID: 2,
			Version:       4,
			Config:        `{"service":{"host":"old","port":8080}}`,
			Descriptions:  `{}`,
		},
	}

	if err := applyConfigEnvironmentReleaseState(environments, releases); err != nil {
		t.Fatalf("applyConfigEnvironmentReleaseState() error = %v", err)
	}
	if environments[0].HasDraft || environments[0].Version != 2 {
		t.Fatalf("base state = hasDraft %t, version %d; want false, 2", environments[0].HasDraft, environments[0].Version)
	}
	if !environments[1].HasDraft || environments[1].Version != 4 {
		t.Fatalf("child state = hasDraft %t, version %d; want true, 4", environments[1].HasDraft, environments[1].Version)
	}
	if !environments[2].HasDraft || environments[2].Version != 0 {
		t.Fatalf("unpublished state = hasDraft %t, version %d; want true, 0", environments[2].HasDraft, environments[2].Version)
	}
}

func TestValidateEnvironmentParentRejectsCycle(t *testing.T) {
	t.Parallel()

	environments := []ConfigEnvironment{
		{ID: 1, ParentID: 0},
		{ID: 2, ParentID: 1},
		{ID: 3, ParentID: 2},
	}
	err := validateEnvironmentParent(environments, 1, 3)
	if !errors.Is(err, ErrConfigEnvironmentInvalid) {
		t.Fatalf("validateEnvironmentParent() error = %v, want ErrConfigEnvironmentInvalid", err)
	}
}

func TestUniqueSortedIDs(t *testing.T) {
	t.Parallel()

	got := uniqueSortedIDs([]uint{4, 0, 2, 4, 1})
	want := []uint{1, 2, 4}
	if !slices.Equal(got, want) {
		t.Fatalf("uniqueSortedIDs() = %v, want %v", got, want)
	}
}

func TestValidateConfigPublishInputRequiresBoundedReason(t *testing.T) {
	t.Parallel()

	valid := ConfigPublishInput{EnvironmentIDs: []uint{1}, Reason: "修复生产环境数据库连接配置"}
	if err := validateConfigPublishInput(valid); err != nil {
		t.Fatalf("validateConfigPublishInput() error = %v", err)
	}
	missing := valid
	missing.Reason = "  "
	if err := validateConfigPublishInput(missing); !errors.Is(err, ErrConfigReleaseInvalid) {
		t.Fatalf("validateConfigPublishInput() missing reason error = %v, want ErrConfigReleaseInvalid", err)
	}
	tooLong := valid
	tooLong.Reason = strings.Repeat("发", maxConfigReleaseReasonLength+1)
	if err := validateConfigPublishInput(tooLong); !errors.Is(err, ErrConfigReleaseInvalid) {
		t.Fatalf("validateConfigPublishInput() long reason error = %v, want ErrConfigReleaseInvalid", err)
	}
}

func TestConfigNamespaceInputNormalizationAndValidation(t *testing.T) {
	t.Parallel()

	input := normalizeConfigNamespaceInput(ConfigNamespaceInput{
		Name:        "  Order Service  ",
		Slug:        "  ORDER-SERVICE  ",
		Description: "  order configuration  ",
	})
	if input.Name != "Order Service" || input.Slug != "order-service" || input.Description != "order configuration" {
		t.Fatalf("normalizeConfigNamespaceInput() = %#v", input)
	}
	if err := validateConfigNamespaceInput(input, false); err != nil {
		t.Fatalf("validateConfigNamespaceInput() error = %v", err)
	}

	invalid := input
	invalid.Slug = "invalid namespace"
	if err := validateConfigNamespaceInput(invalid, false); !errors.Is(err, ErrConfigNamespaceInvalid) {
		t.Fatalf("validateConfigNamespaceInput() error = %v, want ErrConfigNamespaceInvalid", err)
	}
}

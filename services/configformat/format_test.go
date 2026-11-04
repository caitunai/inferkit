package configformat

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

const (
	testHostValue  = "localhost"
	testServiceKey = "service"
)

//nolint:gocognit // This end-to-end test keeps one setup and assertion lifecycle visible.
func TestRenderPublishedConfigurationFormats(t *testing.T) {
	t.Parallel()
	config := map[string]any{
		"enabled": true,
		"regions": []any{"cn", "us"},
		testServiceKey: map[string]any{
			"host":  testHostValue,
			"port":  json.Number("8080"),
			"ratio": json.Number("0.75"),
		},
	}
	descriptions := map[string]string{
		"/service":      "服务连接",
		"/service/host": "服务地址",
	}

	tests := map[Format][]string{
		JSON: {`"config"`, `"descriptions"`, `"/service/host": "服务地址"`},
		YAML: {"# 服务连接", "service:", "  # 服务地址", `host: "localhost"`},
		TOML: {"# 配置项描述", "# /service/host: 服务地址", "[service]", `host = "localhost"`},
		ENV:  {"# 配置项描述", `SERVICE__HOST="localhost"`, "SERVICE__PORT=8080"},
		INI:  {"; 配置项描述", "[service]", `host="localhost"`, "ratio=0.75"},
	}
	for outputFormat, fragments := range tests {
		t.Run(string(outputFormat), func(t *testing.T) {
			t.Parallel()
			output, err := Render(config, descriptions, outputFormat)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			for _, fragment := range fragments {
				if !bytes.Contains(output, []byte(fragment)) {
					t.Fatalf("Render() output does not contain %q:\n%s", fragment, output)
				}
			}
			second, err := Render(config, descriptions, outputFormat)
			if err != nil || !bytes.Equal(output, second) {
				t.Fatalf("Render() output is not deterministic: %v", err)
			}
		})
	}
}

func TestYAMLAndTOMLOutputsCanBeParsed(t *testing.T) {
	t.Parallel()
	config := map[string]any{
		testServiceKey: map[string]any{
			"host": testHostValue,
			"port": json.Number("8080"),
		},
		"regions": []any{"cn", "us"},
	}
	for _, outputFormat := range []Format{YAML, TOML} {
		output, err := Render(config, map[string]string{"/service": "service"}, outputFormat)
		if err != nil {
			t.Fatalf("Render(%s) error = %v", outputFormat, err)
		}
		parser := viper.New()
		parser.SetConfigType(string(outputFormat))
		if operationErr := parser.ReadConfig(bytes.NewReader(output)); operationErr != nil {
			t.Fatalf("ReadConfig(%s) error = %v\n%s", outputFormat, operationErr, output)
		}
		if parser.GetString("service.host") != testHostValue || parser.GetInt("service.port") != 8080 {
			t.Fatalf("parsed %s output lost configuration values", outputFormat)
		}
	}
}

func TestParseFormatAliasesAndRejectsUnsupportedValue(t *testing.T) {
	t.Parallel()
	tests := map[string]Format{
		"":       JSON,
		"JSON":   JSON,
		"yml":    YAML,
		".env":   ENV,
		"dotenv": ENV,
		"ini":    INI,
	}
	for input, expected := range tests {
		actual, err := Parse(input)
		if err != nil || actual != expected {
			t.Fatalf("Parse(%q) = %q, %v; want %q", input, actual, err, expected)
		}
	}
	if _, err := Parse("xml"); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("Parse(xml) error = %v, want ErrUnsupportedFormat", err)
	}
}

func TestRenderRejectsUnsupportedValues(t *testing.T) {
	t.Parallel()
	_, err := Render(map[string]any{"invalid": make(chan int)}, nil, YAML)
	if !errors.Is(err, ErrFormatConfig) {
		t.Fatalf("Render() error = %v, want ErrFormatConfig", err)
	}
	if !strings.Contains(TOML.ContentType(), "toml") {
		t.Fatal("TOML content type is incorrect")
	}
}

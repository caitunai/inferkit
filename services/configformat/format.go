// Package configformat renders published configuration values in formats used
// by people and machine clients while preserving configuration descriptions.
package configformat

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Format represents format data.
type Format string

const (
	// JSON identifies the "json" value.
	JSON Format = "json"
	// YAML identifies the "yaml" value.
	YAML Format = "yaml"
	// TOML identifies the "toml" value.
	TOML Format = "toml"
	// ENV identifies the "env" value.
	ENV Format = "env"
	// INI identifies the "ini" value.
	INI Format = "ini"
)

var (
	// ErrUnsupportedFormat indicates unsupported configuration output format.
	ErrUnsupportedFormat = errors.New("unsupported configuration output format")
	// ErrFormatConfig indicates format configuration output failed.
	ErrFormatConfig = errors.New("format configuration output failed")
)

// Parse performs the parse operation.
func Parse(value string) (Format, error) {
	normalized := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), ".")
	switch normalized {
	case "", string(JSON):
		return JSON, nil
	case string(YAML), "yml":
		return YAML, nil
	case string(TOML):
		return TOML, nil
	case string(ENV), "dotenv":
		return ENV, nil
	case string(INI):
		return INI, nil
	default:
		return "", ErrUnsupportedFormat
	}
}

// ContentType performs the content type operation.
func (format Format) ContentType() string {
	switch format {
	case JSON:
		return "application/json; charset=utf-8"
	case YAML:
		return "application/yaml; charset=utf-8"
	case TOML:
		return "application/toml; charset=utf-8"
	case ENV, INI:
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

// Extension performs the extension operation.
func (format Format) Extension() string {
	if format == "" {
		return string(JSON)
	}
	return string(format)
}

// Render emits the same logical document as the configuration-center preview:
// JSON carries descriptions in a sibling object, while text formats use
// comments so parsers still receive the configuration at their root.
func Render(config map[string]any, descriptions map[string]string, format Format) ([]byte, error) {
	switch format {
	case JSON:
		raw, err := json.MarshalIndent(map[string]any{
			"config":       config,
			"descriptions": descriptions,
		}, "", "  ")
		if err != nil {
			return nil, errors.Join(ErrFormatConfig, err)
		}
		return append(raw, '\n'), nil
	case YAML:
		return renderYAML(config, descriptions)
	case TOML:
		return renderTOML(config, descriptions)
	case ENV:
		return renderENV(config, descriptions)
	case INI:
		return renderINI(config, descriptions)
	default:
		return nil, ErrUnsupportedFormat
	}
}

func renderYAML(config map[string]any, descriptions map[string]string) ([]byte, error) {
	if len(config) == 0 {
		return []byte("{}\n"), nil
	}
	lines, err := yamlObject(config, descriptions, nil, 0)
	if err != nil {
		return nil, err
	}
	return linesBytes(lines), nil
}

func yamlObject(object map[string]any, descriptions map[string]string, path []any, indent int) ([]string, error) {
	lines := make([]string, 0, len(object))
	for _, key := range sortedKeys(object) {
		childPath := appendPath(path, key)
		lines = appendDescription(lines, descriptions[pathToPointer(childPath)], indent, "# ")
		prefix := strings.Repeat(" ", indent) + yamlKey(key) + ":"
		valueLines, err := yamlValue(object[key], descriptions, childPath, indent, prefix)
		if err != nil {
			return nil, err
		}
		lines = append(lines, valueLines...)
	}
	return lines, nil
}

func yamlArray(array []any, descriptions map[string]string, path []any, indent int) ([]string, error) {
	lines := make([]string, 0, len(array))
	for index, value := range array {
		childPath := appendPath(path, index)
		lines = appendDescription(lines, descriptions[pathToPointer(childPath)], indent, "# ")
		prefix := strings.Repeat(" ", indent) + "-"
		valueLines, err := yamlValue(value, descriptions, childPath, indent, prefix)
		if err != nil {
			return nil, err
		}
		lines = append(lines, valueLines...)
	}
	return lines, nil
}

func yamlValue(value any, descriptions map[string]string, path []any, indent int, prefix string) ([]string, error) {
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) == 0 {
			return []string{prefix + " {}"}, nil
		}
		children, err := yamlObject(typed, descriptions, path, indent+2)
		return append([]string{prefix}, children...), err
	case []any:
		if len(typed) == 0 {
			return []string{prefix + " []"}, nil
		}
		children, err := yamlArray(typed, descriptions, path, indent+2)
		return append([]string{prefix}, children...), err
	default:
		scalar, err := scalarValue(value)
		if err != nil {
			return nil, err
		}
		return []string{prefix + " " + scalar}, nil
	}
}

func renderTOML(config map[string]any, descriptions map[string]string) ([]byte, error) {
	lines := descriptionIndex(descriptions, "# ")
	formatted, err := appendTOMLTable(lines, config, nil)
	if err != nil {
		return nil, err
	}
	return linesBytes(formatted), nil
}

//nolint:gocognit // This bounded format or protocol state machine keeps type handling and error exits explicit.
func appendTOMLTable(lines []string, object map[string]any, path []string) ([]string, error) {
	if len(path) > 0 {
		lines = appendBlankLine(lines)
		parts := make([]string, len(path))
		for index, value := range path {
			parts[index] = tomlKey(value)
		}
		lines = append(lines, "["+strings.Join(parts, ".")+"]")
	}
	keys := sortedKeys(object)
	for _, key := range keys {
		if _, nested := object[key].(map[string]any); nested {
			continue
		}
		value, err := tomlValue(object[key])
		if err != nil {
			return nil, err
		}
		lines = append(lines, tomlKey(key)+" = "+value)
	}
	for _, key := range keys {
		nested, ok := object[key].(map[string]any)
		if !ok {
			continue
		}
		var err error
		lines, err = appendTOMLTable(lines, nested, appendStringPath(path, key))
		if err != nil {
			return nil, err
		}
	}
	return lines, nil
}

//nolint:gocognit // This bounded format or protocol state machine keeps type handling and error exits explicit.
func tomlValue(value any) (string, error) {
	switch typed := value.(type) {
	case []any:
		values := make([]string, len(typed))
		for index, child := range typed {
			formatted, err := tomlValue(child)
			if err != nil {
				return "", err
			}
			values[index] = formatted
		}
		return "[" + strings.Join(values, ", ") + "]", nil
	case map[string]any:
		values := make([]string, 0, len(typed))
		for _, key := range sortedKeys(typed) {
			formatted, err := tomlValue(typed[key])
			if err != nil {
				return "", err
			}
			values = append(values, tomlKey(key)+" = "+formatted)
		}
		return "{ " + strings.Join(values, ", ") + " }", nil
	default:
		return scalarValue(value)
	}
}

func renderENV(config map[string]any, descriptions map[string]string) ([]byte, error) {
	lines := descriptionIndex(descriptions, "# ")
	values := flattenValues(config, nil, nil)
	for _, value := range values {
		encoded, err := envValue(value.Value)
		if err != nil {
			return nil, err
		}
		lines = append(lines, envKey(value.Path)+"="+encoded)
	}
	return linesBytes(lines), nil
}

//nolint:gocognit // This bounded format or protocol state machine keeps type handling and error exits explicit.
func renderINI(config map[string]any, descriptions map[string]string) ([]byte, error) {
	lines := descriptionIndex(descriptions, "; ")
	for _, key := range sortedKeys(config) {
		if _, nested := config[key].(map[string]any); nested {
			continue
		}
		value, err := envValue(config[key])
		if err != nil {
			return nil, err
		}
		lines = append(lines, key+"="+value)
	}
	for _, section := range sortedKeys(config) {
		object, ok := config[section].(map[string]any)
		if !ok {
			continue
		}
		lines = appendBlankLine(lines)
		lines = append(lines, "["+section+"]")
		for _, value := range flattenValues(object, nil, nil) {
			encoded, err := envValue(value.Value)
			if err != nil {
				return nil, err
			}
			lines = append(lines, joinPath(value.Path, ".")+"="+encoded)
		}
	}
	return linesBytes(lines), nil
}

type flattenedValue struct {
	Value any
	Path  []any
}

func flattenValues(value any, path []any, result []flattenedValue) []flattenedValue {
	if object, ok := value.(map[string]any); ok {
		for _, key := range sortedKeys(object) {
			result = flattenValues(object[key], appendPath(path, key), result)
		}
		return result
	}
	return append(result, flattenedValue{Path: path, Value: value})
}

func envValue(value any) (string, error) {
	switch value.(type) {
	case string, []any, map[string]any:
		raw, err := json.Marshal(value)
		if err != nil {
			return "", errors.Join(ErrFormatConfig, err)
		}
		return string(raw), nil
	default:
		return scalarValue(value)
	}
}

func scalarValue(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		raw, err := json.Marshal(typed)
		if err != nil {
			return "", errors.Join(ErrFormatConfig, err)
		}
		return string(raw), nil
	case json.Number:
		return scalarJSONNumber(typed)
	case bool:
		return strconv.FormatBool(typed), nil
	case int:
		return strconv.Itoa(typed), nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64), nil
	default:
		return "", ErrFormatConfig
	}
}

func scalarJSONNumber(value json.Number) (string, error) {
	if _, err := value.Int64(); err == nil {
		return value.String(), nil
	}
	if _, err := value.Float64(); err != nil {
		return "", errors.Join(ErrFormatConfig, err)
	}
	return value.String(), nil
}

func yamlKey(key string) string {
	if isBareYAMLKey(key) {
		return key
	}
	return strconv.Quote(key)
}

func tomlKey(key string) string {
	if isBareTOMLKey(key) {
		return key
	}
	return strconv.Quote(key)
}

func isBareYAMLKey(value string) bool {
	for index, current := range value {
		if current != '_' && current != '-' && !unicode.IsLetter(current) && (index == 0 || !unicode.IsDigit(current)) {
			return false
		}
	}
	return value != ""
}

func isBareTOMLKey(value string) bool {
	for _, current := range value {
		if current != '_' && current != '-' && !unicode.IsLetter(current) && !unicode.IsDigit(current) {
			return false
		}
	}
	return value != ""
}

func envKey(path []any) string {
	parts := make([]string, len(path))
	for index, segment := range path {
		normalized := make([]rune, 0, len(toString(segment)))
		for _, current := range toString(segment) {
			if isASCIIAlphaNumeric(current) {
				normalized = append(normalized, unicode.ToUpper(current))
			} else {
				normalized = append(normalized, '_')
			}
		}
		parts[index] = string(normalized)
	}
	return strings.Join(parts, "__")
}

func isASCIIAlphaNumeric(value rune) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func descriptionIndex(descriptions map[string]string, marker string) []string {
	if len(descriptions) == 0 {
		return nil
	}
	pointers := make([]string, 0, len(descriptions))
	for pointer := range descriptions {
		pointers = append(pointers, pointer)
	}
	sort.Strings(pointers)
	lines := []string{marker + "配置项描述"}
	for _, pointer := range pointers {
		for line := range strings.SplitSeq(descriptions[pointer], "\n") {
			lines = append(lines, marker+pointer+": "+line)
		}
	}
	return append(lines, "")
}

func appendDescription(lines []string, description string, indent int, marker string) []string {
	if description == "" {
		return lines
	}
	prefix := strings.Repeat(" ", indent) + marker
	for line := range strings.SplitSeq(description, "\n") {
		lines = append(lines, prefix+line)
	}
	return lines
}

func pathToPointer(path []any) string {
	if len(path) == 0 {
		return ""
	}
	parts := make([]string, len(path))
	for index, segment := range path {
		value := strings.ReplaceAll(toString(segment), "~", "~0")
		parts[index] = strings.ReplaceAll(value, "/", "~1")
	}
	return "/" + strings.Join(parts, "/")
}

func joinPath(path []any, separator string) string {
	parts := make([]string, len(path))
	for index, segment := range path {
		parts[index] = toString(segment)
	}
	return strings.Join(parts, separator)
}

func toString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case int:
		return strconv.Itoa(typed)
	default:
		return ""
	}
}

func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func appendPath(path []any, value any) []any {
	result := make([]any, len(path), len(path)+1)
	copy(result, path)
	return append(result, value)
}

func appendStringPath(path []string, value string) []string {
	result := make([]string, len(path), len(path)+1)
	copy(result, path)
	return append(result, value)
}

func appendBlankLine(lines []string) []string {
	if len(lines) > 0 && lines[len(lines)-1] != "" {
		return append(lines, "")
	}
	return lines
}

func linesBytes(lines []string) []byte {
	return []byte(strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n")
}

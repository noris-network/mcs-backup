package config

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// parseEnviron turns KEY=VALUE pairs into a map. Later entries win.
func parseEnviron(environ []string) map[string]string {
	env := make(map[string]string, len(environ))
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok && k != "" {
			env[k] = v
		}
	}
	return env
}

// mergeFile adds the settings from the YAML file at path to env. Keys are the
// environment variable names; variables that are already set (even to an empty
// value) win over the file. Scalars become strings, lists become shell-quoted
// argument strings (colon separated for the *_PATHS variables) and mappings
// become JSON.
func mergeFile(env map[string]string, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc map[string]any
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	if err := dec.Decode(&doc); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: %w", path, err)
	}
	for key, val := range doc {
		if _, set := env[key]; set {
			continue
		}
		s, err := yamlValue(key, val)
		if err != nil {
			return fmt.Errorf("%s: %s: %w", path, key, err)
		}
		env[key] = s
	}
	return nil
}

func yamlValue(key string, val any) (string, error) {
	switch v := val.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	case bool:
		return strconv.FormatBool(v), nil
	case int:
		return strconv.Itoa(v), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case []any:
		items := make([]string, len(v))
		for i, item := range v {
			s, err := yamlValue(key, item)
			if err != nil {
				return "", err
			}
			items[i] = s
		}
		if strings.HasSuffix(key, "_PATHS") {
			return strings.Join(items, ":"), nil
		}
		return JoinArgs(items), nil
	case map[string]any:
		b, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(b), nil
	default:
		return "", fmt.Errorf("unsupported value of type %T", val)
	}
}

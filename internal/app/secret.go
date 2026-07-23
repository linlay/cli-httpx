package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type siteSecret struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func loadSecret(dir, site string) (*siteSecret, error) {
	path := secretPath(dir, site)
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: secret file %q not found", ErrExecution, path)
		}
		return nil, fmt.Errorf("%w: read secret file %q: %v", ErrExecution, path, err)
	}

	var secret siteSecret
	if err := json.Unmarshal(content, &secret); err != nil {
		return nil, fmt.Errorf("%w: decode secret file %q: %v", ErrExecution, path, err)
	}
	if strings.TrimSpace(secret.Username) == "" {
		return nil, fmt.Errorf("%w: secret file %q is missing username", ErrExecution, path)
	}
	if strings.TrimSpace(secret.Password) == "" {
		return nil, fmt.Errorf("%w: secret file %q is missing password", ErrExecution, path)
	}
	return &secret, nil
}

func secretPath(dir, site string) string {
	return filepath.Join(dir, site+".json")
}

func findSecretFile(dir, site string) ([]byte, string, error) {
	path := filepath.Join(dir, site+".json")
	content, err := os.ReadFile(path)
	if err == nil {
		return content, path, nil
	}
	if !os.IsNotExist(err) {
		return nil, "", fmt.Errorf("%w: read secret file %q: %v", ErrExecution, path, err)
	}

	return nil, "", fmt.Errorf("%w: secret file not found at %q; create it with: mkdir -p %q && cat > %q; expected JSON object like {\"cookie\":\"...\"}", ErrConfig, path, dir, path)
}

func loadSecretKey(dir, site, key string) (any, error) {
	content, path, err := findSecretFile(dir, site)
	if err != nil {
		return nil, err
	}

	var data map[string]any
	if err := json.Unmarshal(content, &data); err != nil {
		return nil, fmt.Errorf("%w: invalid secret JSON at %q: %v; expected a JSON object like {\"cookie\":\"...\"}", ErrConfig, path, err)
	}
	if data == nil {
		return nil, fmt.Errorf("%w: invalid secret JSON at %q: expected a JSON object like {\"cookie\":\"...\"}", ErrConfig, path)
	}
	value, ok := data[key]
	if !ok {
		return nil, fmt.Errorf("%w: secret key %q not found in %q", ErrExecution, key, path)
	}
	return value, nil
}

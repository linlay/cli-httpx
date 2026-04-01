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

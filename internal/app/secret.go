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
	return loadSecretAt(path, path)
}

func loadSecretForScope(dir, site string, scope storageScope) (*siteSecret, error) {
	return loadSecretAt(secretPath(dir, site), displaySecretPath(dir, site, scope))
}

func loadSecretAt(path, displayPath string) (*siteSecret, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: secret file %q not found", ErrExecution, displayPath)
		}
		return nil, fmt.Errorf("%w: read secret file %q: %v", ErrExecution, displayPath, pathErrorCause(err))
	}

	var secret siteSecret
	if err := json.Unmarshal(content, &secret); err != nil {
		return nil, fmt.Errorf("%w: decode secret file %q: %v", ErrExecution, displayPath, err)
	}
	if strings.TrimSpace(secret.Username) == "" {
		return nil, fmt.Errorf("%w: secret file %q is missing username", ErrExecution, displayPath)
	}
	if strings.TrimSpace(secret.Password) == "" {
		return nil, fmt.Errorf("%w: secret file %q is missing password", ErrExecution, displayPath)
	}
	return &secret, nil
}

func secretPath(dir, site string) string {
	return filepath.Join(dir, site+".json")
}

func findSecretFile(dir, site string) ([]byte, string, error) {
	path := filepath.Join(dir, site+".json")
	return findSecretFileAt(path, path, dir)
}

func findSecretFileForScope(dir, site string, scope storageScope) ([]byte, string, error) {
	path := filepath.Join(dir, site+".json")
	displayPath := displaySecretPath(dir, site, scope)
	displayDir := dir
	if scope == scopeChat {
		displayDir = filepath.Join(".secret", "httpx")
	}
	return findSecretFileAt(path, displayPath, displayDir)
}

func findSecretFileAt(path, displayPath, displayDir string) ([]byte, string, error) {
	content, err := os.ReadFile(path)
	if err == nil {
		return content, displayPath, nil
	}
	if !os.IsNotExist(err) {
		return nil, "", fmt.Errorf("%w: read secret file %q: %v", ErrExecution, displayPath, pathErrorCause(err))
	}

	return nil, "", fmt.Errorf("%w: secret file not found at %q; create it under %q; expected JSON object like {\"cookie\":\"...\"}", ErrConfig, displayPath, displayDir)
}

func loadSecretKey(dir, site, key string) (any, error) {
	content, path, err := findSecretFile(dir, site)
	return decodeSecretKey(content, path, key, err)
}

func loadSecretKeyForScope(dir, site, key string, scope storageScope) (any, error) {
	content, path, err := findSecretFileForScope(dir, site, scope)
	return decodeSecretKey(content, path, key, err)
}

func decodeSecretKey(content []byte, path, key string, findErr error) (any, error) {
	if findErr != nil {
		return nil, findErr
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

func pathErrorCause(err error) error {
	if pathErr, ok := err.(*os.PathError); ok {
		return pathErr.Err
	}
	return err
}

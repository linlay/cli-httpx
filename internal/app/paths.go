package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func defaultConfigDir() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "httpx")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".config", "httpx")
}

func defaultSecretDir() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "secret", "httpx")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "secret", "httpx")
	}
	return filepath.Join(home, ".local", "secret", "httpx")
}

func resolveConfigPath(configDir, site string) (string, error) {
	if site == "" {
		return "", fmt.Errorf("%w: site is required", ErrConfig)
	}

	if configDir == defaultConfigDir() {
		envKey := siteConfigEnvKey(site)
		if envPath, ok := os.LookupEnv(envKey); ok && strings.TrimSpace(envPath) != "" {
			return resolveLoadedConfigPath(envKey, envPath, site)
		}
	}

	if info, err := os.Stat(configDir); err == nil && !info.IsDir() {
		return "", fmt.Errorf("%w: config path %q must be a directory", ErrConfig, configDir)
	}

	return filepath.Join(configDir, site+".toml"), nil
}

func resolveLoadedConfigPath(envKey, envPath, site string) (string, error) {
	if info, err := os.Stat(envPath); err == nil {
		if info.IsDir() {
			return filepath.Join(envPath, site+".toml"), nil
		}
		return envPath, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("%w: stat config path from %s: %v", ErrConfig, envKey, err)
	}

	if filepath.Ext(envPath) == ".toml" {
		return envPath, nil
	}
	return filepath.Join(envPath, site+".toml"), nil
}

func listConfigSites(configDir string) ([]string, error) {
	if info, err := os.Stat(configDir); err == nil && !info.IsDir() {
		return nil, fmt.Errorf("%w: config path %q must be a directory", ErrConfig, configDir)
	}

	entries, err := os.ReadDir(configDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: read config directory: %v", ErrConfig, err)
	}

	sites := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".toml" {
			continue
		}
		site := strings.TrimSuffix(name, ".toml")
		if err := validateSiteName(site); err != nil {
			return nil, err
		}
		sites = append(sites, site)
	}
	sort.Strings(sites)
	return sites, nil
}

func defaultStateDir() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "httpx")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "state", "httpx")
	}
	return filepath.Join(home, ".local", "state", "httpx")
}

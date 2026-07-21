package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	agentConfigHomeEnv = "AP_AGENT_CONFIG_HOME"
)

func defaultConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "httpx")
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
	return resolveConfigPathWithFallback(configDir, true, site)
}

func resolveConfigPathWithFallback(configDir string, allowFallback bool, site string) (string, error) {
	if site == "" {
		return "", fmt.Errorf("%w: site is required", ErrConfig)
	}

	configDirs, err := configSearchDirs(configDir, allowFallback)
	if err != nil {
		return "", err
	}
	for _, dir := range configDirs {
		if info, err := os.Stat(dir); err == nil && !info.IsDir() {
			return "", fmt.Errorf("%w: config path %q must be a directory", ErrConfig, dir)
		} else if err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("%w: stat config path %q: %v", ErrConfig, dir, err)
		}
		path := filepath.Join(dir, site+".toml")
		if _, err := os.Stat(path); err == nil {
			return path, nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("%w: stat config path %q: %v", ErrConfig, path, err)
		}
	}
	return filepath.Join(configDirs[0], site+".toml"), nil
}

func listConfigSites(configDir string) ([]string, error) {
	return listConfigSitesWithFallback(configDir, true)
}

func listConfigSitesWithFallback(configDir string, allowFallback bool) ([]string, error) {
	configDirs, err := configSearchDirs(configDir, allowFallback)
	if err != nil {
		return nil, err
	}
	sitesByName := map[string]struct{}{}
	for _, dir := range configDirs {
		sites, err := listConfigSitesInDir(dir)
		if err != nil {
			return nil, err
		}
		for _, site := range sites {
			sitesByName[site] = struct{}{}
		}
	}
	sites := make([]string, 0, len(sitesByName))
	for site := range sitesByName {
		sites = append(sites, site)
	}
	sort.Strings(sites)
	return sites, nil
}

func listConfigSitesInDir(configDir string) ([]string, error) {
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

func configSearchDirs(configDir string, allowFallback bool) ([]string, error) {
	if !allowFallback || !sameConfigPath(configDir, defaultConfigDir()) {
		return []string{configDir}, nil
	}
	dirs := []string{}
	if agentConfigHome := strings.TrimSpace(os.Getenv(agentConfigHomeEnv)); agentConfigHome != "" {
		dirs = appendUniqueConfigDir(dirs, filepath.Join(agentConfigHome, "httpx"))
	}
	return appendUniqueConfigDir(dirs, defaultConfigDir()), nil
}

func appendUniqueConfigDir(dirs []string, dir string) []string {
	for _, existing := range dirs {
		if sameConfigPath(existing, dir) {
			return dirs
		}
	}
	return append(dirs, dir)
}

func sameConfigPath(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
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

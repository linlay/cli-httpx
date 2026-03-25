package app

import (
	"fmt"
	"os"
	"path/filepath"
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

func resolveConfigPath(configDir, profile string) (string, error) {
	if profile == "" {
		return "", fmt.Errorf("%w: profile is required", ErrConfig)
	}

	if info, err := os.Stat(configDir); err == nil && !info.IsDir() {
		return "", fmt.Errorf("%w: config path %q must be a directory", ErrConfig, configDir)
	}

	return filepath.Join(configDir, profile+".toml"), nil
}

func defaultStateDir() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "httpx")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".httpx-state"
	}
	return filepath.Join(home, ".local", "state", "httpx")
}

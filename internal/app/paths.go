package app

import (
	"os"
	"path/filepath"
)

func defaultConfigPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "httpx", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.toml"
	}
	return filepath.Join(home, ".config", "httpx", "config.toml")
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

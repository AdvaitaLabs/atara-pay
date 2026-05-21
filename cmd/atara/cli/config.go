package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Config is the persisted user state. Lives at $HOME/.atara/config.json so
// successive commands reuse the base URL + API key without re-prompting.
// Env vars (ATARA_BASE_URL, ATARA_API_KEY) override; --flags override env.
type Config struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
}

const (
	envBaseURL = "ATARA_BASE_URL"
	envAPIKey  = "ATARA_API_KEY"
)

// configPath returns the on-disk path. Honors XDG_CONFIG_HOME if set,
// otherwise falls back to ~/.atara/.
func configPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "atara", "config.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".atara", "config.json"), nil
}

// loadConfig reads the on-disk file. Missing file is not an error —
// returns the zero Config so first-time users still get sensible defaults
// from env / flags.
func loadConfig() (Config, error) {
	var c Config
	p, err := configPath()
	if err != nil {
		return c, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return c, nil
		}
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("config %s: %w", p, err)
	}
	return c, nil
}

// saveConfig atomically writes c. Creates parent dir if needed. Permissions
// set to 0600 — the API key is in this file.
func saveConfig(c Config) error {
	p, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// resolveConfig builds the effective Config for one command invocation.
// Precedence (highest first): --flag → env → file → defaults.
func resolveConfig(flagBase, flagKey string) (Config, error) {
	c, err := loadConfig()
	if err != nil {
		return c, err
	}
	if v := os.Getenv(envBaseURL); v != "" {
		c.BaseURL = v
	}
	if v := os.Getenv(envAPIKey); v != "" {
		c.APIKey = v
	}
	if flagBase != "" {
		c.BaseURL = flagBase
	}
	if flagKey != "" {
		c.APIKey = flagKey
	}
	if c.BaseURL == "" {
		c.BaseURL = "http://localhost:8080"
	}
	return c, nil
}

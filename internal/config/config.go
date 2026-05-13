// Package config loads Atara-Pay runtime configuration from environment variables.
package config

import (
	"fmt"
	"os"
)

// Config is the full Atara-Pay runtime configuration.
type Config struct {
	Port string

	// CrossMint
	CrossMintAPIKey  string
	CrossMintBaseURL string

	// Tempo (filled in when the Tempo adapter lands)
	TempoRPCURL     string
	TempoPrivateKey string

	// Routing
	DefaultRail string
	RoutingMode string
}

// Load reads config from env, applying sensible defaults.
func Load() (*Config, error) {
	cfg := &Config{
		Port:             getEnv("ATARA_PAY_PORT", "8080"),
		CrossMintAPIKey:  os.Getenv("CROSSMINT_API_KEY"),
		CrossMintBaseURL: os.Getenv("CROSSMINT_BASE_URL"),
		TempoRPCURL:      os.Getenv("TEMPO_RPC_URL"),
		TempoPrivateKey:  os.Getenv("TEMPO_PRIVATE_KEY"),
		DefaultRail:      getEnv("ATARA_PAY_DEFAULT_RAIL", "crossmint"),
		RoutingMode:      getEnv("ATARA_PAY_ROUTING_MODE", "smart"),
	}

	if cfg.CrossMintAPIKey == "" && cfg.TempoRPCURL == "" {
		return nil, fmt.Errorf("at least one rail must be configured (set CROSSMINT_API_KEY or TEMPO_RPC_URL)")
	}
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

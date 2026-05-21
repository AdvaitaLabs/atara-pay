// Package config loads Atara-Pay runtime configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config is the full Atara-Pay runtime configuration.
type Config struct {
	Port string

	// Persistence
	DatabaseURL string // postgres://...
	RedisURL    string // redis://...

	// SessionSigningKey is the HS256 secret used to mint dashboard session
	// JWTs. Must be at least 32 bytes — see internal/auth/session.go.
	SessionSigningKey []byte

	// CrossMint
	CrossMintAPIKey  string
	CrossMintBaseURL string

	// Tempo
	TempoRPCURL  string
	TempoChainID int64

	// Routing
	DefaultRail string
	RoutingMode string

	// Environment is the gateway's deployment ring. One of: "production",
	// "staging", "development". Surfaces in /health and every webhook so
	// integrators can fail-closed when a test key talks to prod (or vice
	// versa). Defaults to "development" when unset.
	Environment string
}

// Load reads config from env, applying sensible defaults.
func Load() (*Config, error) {
	chainID, err := parseIntEnv("TEMPO_CHAIN_ID", 0)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Port:              getEnv("ATARA_PAY_PORT", "8080"),
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		RedisURL:          os.Getenv("REDIS_URL"),
		SessionSigningKey: []byte(os.Getenv("SESSION_SIGNING_KEY")),
		CrossMintAPIKey:   os.Getenv("CROSSMINT_API_KEY"),
		CrossMintBaseURL:  os.Getenv("CROSSMINT_BASE_URL"),
		TempoRPCURL:       os.Getenv("TEMPO_RPC_URL"),
		TempoChainID:      chainID,
		DefaultRail:       getEnv("ATARA_PAY_DEFAULT_RAIL", "crossmint"),
		RoutingMode:       getEnv("ATARA_PAY_ROUTING_MODE", "smart"),
		Environment:       normalizeEnv(getEnv("ATARA_PAY_ENVIRONMENT", "development")),
	}

	if cfg.CrossMintAPIKey == "" && cfg.TempoRPCURL == "" {
		return nil, fmt.Errorf("at least one rail must be configured (set CROSSMINT_API_KEY or TEMPO_RPC_URL)")
	}
	if cfg.TempoRPCURL != "" && cfg.TempoChainID == 0 {
		return nil, fmt.Errorf("TEMPO_CHAIN_ID must be set when TEMPO_RPC_URL is configured (4217=mainnet, 42431=testnet)")
	}
	return cfg, nil
}

func parseIntEnv(key string, fallback int64) (int64, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}

// normalizeEnv coerces common aliases ("prod"→"production", "test"/"sandbox"
// →"staging") so /health and webhook payloads only ever emit one of three
// canonical values.
func normalizeEnv(v string) string {
	switch v {
	case "prod", "production", "live":
		return "production"
	case "staging", "stage", "sandbox", "test", "testnet":
		return "staging"
	default:
		return "development"
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

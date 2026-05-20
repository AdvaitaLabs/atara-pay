// Package cache owns ATARA-Pay's Redis client.
//
// Redis is used for high-frequency operations that must be atomic across
// processes:
//   - usage counters for spending limits (INCR + EXPIRE per period)
//   - rate limits per tenant
//   - idempotency keys
//   - short-lived caches (wallet rail lookups)
package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client is an alias for go-redis so callers don't import the package directly.
type Client = redis.Client

// Config configures the Redis client.
type Config struct {
	URL string // redis://[:password]@host:port/db
}

// Connect dials Redis and verifies with PING.
func Connect(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("cache: URL is required")
	}

	opt, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	client := redis.NewClient(opt)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return client, nil
}

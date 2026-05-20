// Package db owns ATARA-Pay's PostgreSQL connection pool.
//
// The pool is created once at startup and shared across the process. Queries
// go through sqlc-generated code (see internal/db/queries/, added later).
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool is the application-wide pgx pool.
type Pool = pgxpool.Pool

// Config configures the PostgreSQL pool.
type Config struct {
	URL             string // postgres://user:pass@host:port/db?sslmode=disable
	MaxConns        int32  // default 10
	MinConns        int32  // default 2
	MaxConnLifetime time.Duration
}

// Connect dials PostgreSQL and verifies the connection with a ping.
func Connect(ctx context.Context, cfg Config) (*Pool, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("db: URL is required")
	}

	pcfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse db url: %w", err)
	}

	if cfg.MaxConns > 0 {
		pcfg.MaxConns = cfg.MaxConns
	} else {
		pcfg.MaxConns = 10
	}
	if cfg.MinConns > 0 {
		pcfg.MinConns = cfg.MinConns
	} else {
		pcfg.MinConns = 2
	}
	if cfg.MaxConnLifetime > 0 {
		pcfg.MaxConnLifetime = cfg.MaxConnLifetime
	} else {
		pcfg.MaxConnLifetime = 30 * time.Minute
	}

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("create pg pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return pool, nil
}

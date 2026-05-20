// Atara-Pay — protocol-aggregation gateway for agentic payments.
package main

import (
	"context"
	"log"

	"github.com/atara-xyz/atara-pay/internal/adapters"
	"github.com/atara-xyz/atara-pay/internal/adapters/crossmint"
	"github.com/atara-xyz/atara-pay/internal/adapters/tempo"
	"github.com/atara-xyz/atara-pay/internal/cache"
	"github.com/atara-xyz/atara-pay/internal/config"
	"github.com/atara-xyz/atara-pay/internal/db"
	"github.com/atara-xyz/atara-pay/internal/keystore"
	"github.com/atara-xyz/atara-pay/internal/router"
	"github.com/atara-xyz/atara-pay/internal/server"
	"github.com/atara-xyz/atara-pay/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx := context.Background()

	// ── PostgreSQL ────────────────────────────────────────────────────
	// Optional during MVP: when blank we run in memory-only mode and the
	// auth routes are not mounted.
	var pool *pgxpool.Pool
	if cfg.DatabaseURL != "" {
		pool, err = db.Connect(ctx, db.Config{URL: cfg.DatabaseURL})
		if err != nil {
			log.Fatalf("postgres: %v", err)
		}
		defer pool.Close()
		log.Println("[atara-pay] postgres connected")
	} else {
		log.Println("[atara-pay] DATABASE_URL not set — auth routes disabled (memory-only mode)")
	}

	// ── Redis (still optional; consumed in M6 limits) ────────────────
	if cfg.RedisURL != "" {
		rdb, err := cache.Connect(ctx, cache.Config{URL: cfg.RedisURL})
		if err != nil {
			log.Fatalf("redis: %v", err)
		}
		defer rdb.Close()
		log.Println("[atara-pay] redis connected")
		_ = rdb // wired into limit middleware in M6
	} else {
		log.Println("[atara-pay] REDIS_URL not set — limit counters will use in-memory fallback")
	}

	// ── Session signing key ──────────────────────────────────────────
	// Required when persistence is on; loudly disable login otherwise.
	if pool != nil && len(cfg.SessionSigningKey) < 32 {
		log.Fatal("SESSION_SIGNING_KEY must be at least 32 bytes when DATABASE_URL is set " +
			"(generate one with: openssl rand -base64 48)")
	}

	// ── Encryption keystore (Level 2) ────────────────────────────────
	// Optional today (Tempo adapter still uses the in-memory keystore until
	// M3.3 migrates it to the DB). Once that migration lands, missing master
	// keys here will be a fatal config error.
	var ks *keystore.AESKeystore
	if pool != nil {
		k, err := keystore.FromEnv()
		if err != nil {
			log.Printf("[atara-pay] keystore not configured (%v) — Tempo wallets will use in-memory keys", err)
		} else {
			ks = k
			log.Printf("[atara-pay] keystore loaded (current version=%d)", ks.CurrentVersion())
		}
	}
	_ = ks // wired into Tempo adapter in M3.3

	// ── Rails ────────────────────────────────────────────────────────
	var registered []adapters.Adapter

	if cfg.CrossMintAPIKey != "" {
		cm, err := crossmint.New(crossmint.Config{
			APIKey:  cfg.CrossMintAPIKey,
			BaseURL: cfg.CrossMintBaseURL,
		})
		if err != nil {
			log.Fatalf("crossmint adapter: %v", err)
		}
		registered = append(registered, cm)
		log.Println("[atara-pay] rail registered: crossmint")
	}

	if cfg.TempoRPCURL != "" {
		tp, err := tempo.New(tempo.Config{
			RPCURL:  cfg.TempoRPCURL,
			ChainID: cfg.TempoChainID,
		}, nil)
		if err != nil {
			log.Fatalf("tempo adapter: %v", err)
		}
		registered = append(registered, tp)
		log.Printf("[atara-pay] rail registered: tempo (chainId=%d)", cfg.TempoChainID)
	}

	if len(registered) == 0 {
		log.Fatal("no rails configured")
	}

	// ── HTTP server ──────────────────────────────────────────────────
	r := router.New(types.Rail(cfg.DefaultRail), registered...)
	s := server.New(server.Deps{
		Router:            r,
		Pool:              pool,
		SessionSigningKey: cfg.SessionSigningKey,
	})

	addr := ":" + cfg.Port
	log.Printf("[atara-pay] listening on %s (default rail: %s)", addr, cfg.DefaultRail)
	if err := s.Listen(addr); err != nil {
		log.Fatal(err)
	}
}

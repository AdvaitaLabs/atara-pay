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
	"github.com/atara-xyz/atara-pay/internal/router"
	"github.com/atara-xyz/atara-pay/internal/server"
	"github.com/atara-xyz/atara-pay/internal/types"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx := context.Background()

	// PostgreSQL — required once persistence lands, optional during MVP so
	// the existing in-memory adapters keep working until M2/M3 migrate to DB.
	if cfg.DatabaseURL != "" {
		pool, err := db.Connect(ctx, db.Config{URL: cfg.DatabaseURL})
		if err != nil {
			log.Fatalf("postgres: %v", err)
		}
		defer pool.Close()
		log.Println("[atara-pay] postgres connected")
		_ = pool // wired into handlers in next sprint
	} else {
		log.Println("[atara-pay] DATABASE_URL not set — running in memory-only mode")
	}

	// Redis — same story: optional now, required for usage counters in M6.
	if cfg.RedisURL != "" {
		rdb, err := cache.Connect(ctx, cache.Config{URL: cfg.RedisURL})
		if err != nil {
			log.Fatalf("redis: %v", err)
		}
		defer rdb.Close()
		log.Println("[atara-pay] redis connected")
		_ = rdb
	} else {
		log.Println("[atara-pay] REDIS_URL not set — limit counters will use in-memory fallback")
	}

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

	r := router.New(types.Rail(cfg.DefaultRail), registered...)
	s := server.New(r)

	addr := ":" + cfg.Port
	log.Printf("[atara-pay] listening on %s (default rail: %s)", addr, cfg.DefaultRail)
	if err := s.Listen(addr); err != nil {
		log.Fatal(err)
	}
}

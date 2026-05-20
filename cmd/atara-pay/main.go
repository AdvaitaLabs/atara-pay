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
	"github.com/atara-xyz/atara-pay/internal/sessionkey"
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

	// ── Redis (powers limits.Service period accumulators) ───────────
	var rdb *cache.Client
	if cfg.RedisURL != "" {
		var err error
		rdb, err = cache.Connect(ctx, cache.Config{URL: cfg.RedisURL})
		if err != nil {
			log.Fatalf("redis: %v", err)
		}
		defer rdb.Close()
		log.Println("[atara-pay] redis connected")
	} else {
		log.Println("[atara-pay] REDIS_URL not set — period accumulators disabled, " +
			"synchronous limit gates (per-tx / recipient / expiry) still enforce")
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
	// Typed handles for the rails we build, so the dual-rail wallet-group
	// handler in the server can call them directly. They're also packed
	// into the legacy Router for the existing /v1/{wallets,transactions,
	// onramp} endpoints.
	var (
		registered []adapters.Adapter
		cmAdapter  *crossmint.Adapter
		tpAdapter  *tempo.Adapter
	)

	if cfg.CrossMintAPIKey != "" {
		var err error
		cmAdapter, err = crossmint.New(crossmint.Config{
			APIKey:  cfg.CrossMintAPIKey,
			BaseURL: cfg.CrossMintBaseURL,
		})
		if err != nil {
			log.Fatalf("crossmint adapter: %v", err)
		}
		registered = append(registered, cmAdapter)
		log.Println("[atara-pay] rail registered: crossmint")
	}

	if cfg.TempoRPCURL != "" {
		var err error
		tpAdapter, err = tempo.New(tempo.Config{
			RPCURL:  cfg.TempoRPCURL,
			ChainID: cfg.TempoChainID,
		}, nil)
		if err != nil {
			log.Fatalf("tempo adapter: %v", err)
		}
		registered = append(registered, tpAdapter)
		log.Printf("[atara-pay] rail registered: tempo (chainId=%d)", cfg.TempoChainID)
	}

	if len(registered) == 0 {
		log.Fatal("no rails configured")
	}

	// Heads-up if the dual-rail endpoint won't mount.
	if pool != nil {
		var missing []string
		if cmAdapter == nil {
			missing = append(missing, "CrossMint")
		}
		if tpAdapter == nil {
			missing = append(missing, "Tempo")
		}
		if ks == nil {
			missing = append(missing, "keystore")
		}
		if len(missing) > 0 {
			log.Printf("[atara-pay] /v1/wallet-groups disabled: missing %v", missing)
		}
	}

	// ── HTTP server ──────────────────────────────────────────────────
	r := router.New(types.Rail(cfg.DefaultRail), registered...)

	// keystore.Keystore is already an interface; *AESKeystore satisfies it.
	// We pass nil through when ks is nil so server.New can skip mounting
	// the wallet-groups endpoint cleanly.
	var ksIface keystore.Keystore
	if ks != nil {
		ksIface = ks
	}

	s := server.New(server.Deps{
		Router:            r,
		Pool:              pool,
		SessionSigningKey: cfg.SessionSigningKey,
		CrossMint:         cmAdapter,
		Tempo:             tpAdapter,
		Keystore:          ksIface,
		Redis:             rdb,
	})

	// Session-key background rotator. Sweeps expired rows + logs rotation
	// backlog every minute. Webhook handoff lands in M7.
	if pool != nil {
		rotatorCtx, cancelRotator := context.WithCancel(ctx)
		defer cancelRotator()
		rotator := sessionkey.NewRotator(pool, 0, 0)
		go rotator.Run(rotatorCtx)
	}

	addr := ":" + cfg.Port
	log.Printf("[atara-pay] listening on %s (default rail: %s)", addr, cfg.DefaultRail)
	if err := s.Listen(addr); err != nil {
		log.Fatal(err)
	}
}

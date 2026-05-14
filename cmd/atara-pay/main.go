// Atara-Pay — protocol-aggregation gateway for agentic payments.
package main

import (
	"log"

	"github.com/atara-xyz/atara-pay/internal/adapters"
	"github.com/atara-xyz/atara-pay/internal/adapters/crossmint"
	"github.com/atara-xyz/atara-pay/internal/adapters/tempo"
	"github.com/atara-xyz/atara-pay/internal/config"
	"github.com/atara-xyz/atara-pay/internal/router"
	"github.com/atara-xyz/atara-pay/internal/server"
	"github.com/atara-xyz/atara-pay/internal/types"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
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

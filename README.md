# ATARA-Pay

> **One API. Every payment rail. Built for the age of AI agents.**

**ATARA-Pay** is the protocol-aggregation gateway of the [Atara](#the-atara-stack) stack. It exposes a single, opinionated HTTP API on top of multiple payment rails (CrossMint, Tempo, Loka P2P Lightning, OTC), so AI agents and B2B integrators can issue wallets, move money, and accept fiat **without learning any one provider's SDK**.

[![Go](https://img.shields.io/badge/Go-1.24%2B-00ADD8.svg)](https://go.dev)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Status](https://img.shields.io/badge/status-alpha-orange.svg)](#roadmap)

---

## Why ATARA-Pay

Building an agentic-payment product today means stitching together:

- **CrossMint** for managed smart-contract wallets and credit-card on-ramps,
- **Tempo** for sub-second stablecoin transfers and AI session keys,
- **Lightning / Sui** for micro-payments and cross-border settlement,
- A KYC vendor, an OTC desk, a webhook handler, an idempotency store, …

Each one ships its own SDK, auth model, error shape, and chain semantics. Six weeks of glue before the first real payment moves.

ATARA-Pay collapses all of that into:

```http
POST /v1/wallets               → wallet on any rail
POST /v1/transactions          → transfer on any rail
POST /v1/onramp/orders         → fiat → stablecoin on any rail
GET  /v1/quote                 → best route across all rails    (planned)
```

Switch providers by changing **one config line**. No SDK install. No business-logic change. Curl works. Any language works.

---

## Design principles

1. **One API, many rails.** Adapters translate; the public surface never leaks provider details.
2. **Smart routing.** Given an intent (`send 50 USDC to alice@x`), the gateway picks the cheapest / fastest rail. Override with `?rail=crossmint` when you must.
3. **One-line integration.** No SDK install required — talk to the gateway over plain HTTP/JSON with an API key.
4. **Stateless adapters, stateful gateway.** Adapters are pure protocol clients. Idempotency, retries, audit, billing live in the gateway.
5. **Zero lock-in.** Apache-2.0. Your data lives in your Postgres. Take it and go.

---

## Architecture

```
                ┌─────────────────────────────────────────┐
                │  Your App / AI Agent / B2B Client       │
                └────────────────────┬────────────────────┘
                                     │  HTTP + API Key
                                     ▼
┌────────────────────────────────────────────────────────────┐
│                     ATARA-Pay Gateway                       │
│  ┌──────────────┬──────────────┬─────────────┬───────────┐ │
│  │   /v1 API    │   Router     │  Auth/Quota │  Webhooks │ │
│  └──────────────┴──────┬───────┴─────────────┴───────────┘ │
│                        │                                    │
│         ┌──────────────┼──────────────┬─────────────┐      │
│         ▼              ▼              ▼             ▼      │
│   ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐  │
│   │CrossMint │  │  Tempo   │  │ Loka-LN  │  │ Atara-OTC│  │
│   │ adapter  │  │ adapter  │  │ adapter  │  │ adapter  │  │
│   └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘  │
└────────┼─────────────┼─────────────┼─────────────┼────────┘
         ▼             ▼             ▼             ▼
    api.crossmint  Tempo RPC    LND gRPC / Sui   OTC desk
```

| Layer | Owns |
|---|---|
| `/v1` API | Unified request/response shape, validation, OpenAPI spec |
| Router | Rail selection, quoting, failover, idempotency |
| Adapter | Provider-specific HTTP/RPC client; pure translator |
| Storage | Postgres (audit + idempotency keys) + Redis (cache + rate limit) |

---

## Supported rails

| Rail | Wallet | Send | Onramp | Status |
|---|---|---|---|---|
| **CrossMint** | EVM/Solana/Stellar | ✅ | ✅ Credit card → USDC | ✅ shipped |
| **Tempo** | Tempo L1 (EOA) | ✅ pathUSD (TIP-20) | ❌ (no fiat rail) | ✅ shipped |
| **Loka P2P Lightning** | LN + Sui | sats / SUI / hold-invoice | ❌ (use OTC) | ⏳ planned |
| **Atara OTC** | — | large-volume fiat ↔ stablecoin | ✅ | ⏳ planned |

---

## Quickstart

### 1. Run the gateway

```sh
git clone https://github.com/atara-xyz/atara-pay
cd atara-pay
cp .env.example .env          # fill in CROSSMINT_API_KEY
go run ./cmd/atara-pay
```

Server listens on `:8080`.

### 2. Create a wallet (any rail, same call)

```sh
curl -X POST http://localhost:8080/v1/wallets \
  -H "Content-Type: application/json" \
  -d '{
    "rail":  "crossmint",
    "chain": "base",
    "type":  "smart",
    "owner": { "type": "email", "value": "alice@example.com" }
  }'
```

Response:

```json
{
  "id":      "01J...",
  "rail":    "crossmint",
  "chain":   "base",
  "address": "0xA1B2...",
  "created_at": "2026-05-12T03:14:15Z"
}
```

### 3. Send a payment

```sh
curl -X POST "http://localhost:8080/v1/transactions?rail=crossmint" \
  -H "Content-Type: application/json" \
  -d '{
    "from":   "01J...",
    "to":     "0xC3D4...",
    "amount": "50",
    "asset":  "USDC",
    "chain":  "base"
  }'
```

### 4. Fiat on-ramp

```sh
curl -X POST "http://localhost:8080/v1/onramp/orders?rail=crossmint" \
  -d '{
    "wallet_address": "0xA1B2...",
    "fiat":           { "amount": "100", "currency": "USD" },
    "asset":          "USDC",
    "chain":          "base"
  }'
```

Returns a hosted-checkout URL. User pays card → USDC lands in wallet.

Full demo: [`examples/curl.sh`](examples/curl.sh).

---

## Configuration

`.env`:

```ini
ATARA_PAY_PORT=8080
ATARA_PAY_DEFAULT_RAIL=crossmint
ATARA_PAY_ROUTING_MODE=smart

# CrossMint — https://www.crossmint.com/console
CROSSMINT_API_KEY=sk_staging_xxx
CROSSMINT_BASE_URL=https://staging.crossmint.com

# Tempo — public testnet defaults shown
TEMPO_RPC_URL=https://rpc.moderato.tempo.xyz
TEMPO_CHAIN_ID=42431
```

Disable a rail by leaving its keys blank — ATARA-Pay will skip it at boot.

---

## Repository layout

```
atara-pay/
├── cmd/atara-pay/              # main.go
├── internal/
│   ├── server/                 # Fiber HTTP server (/v1 routes)
│   ├── router/                 # rail selection + quoting
│   ├── adapters/
│   │   ├── adapter.go          # the Adapter contract
│   │   ├── crossmint/          # ✅ shipped
│   │   └── tempo/              # ✅ shipped
│   ├── types/                  # unified Wallet / Tx / Order
│   ├── config/
│   └── errors/
├── api/v1/                     # OpenAPI spec (WIP)
├── examples/                   # curl + JS + Python samples
└── docs/                       # design notes
```

---

## Roadmap

- [x] Project skeleton + design
- [x] CrossMint adapter (wallets, transfers, onramp)
- [x] Tempo adapter (wallets, pathUSD transfers)
- [ ] Tempo session keys (AI-agent spending limits) ← **current**
- [ ] Loka P2P Lightning adapter
- [ ] Smart router (quote + failover)
- [ ] Atara OTC adapter
- [ ] Idempotency + webhook fan-out
- [ ] OpenAPI 3.1 spec + auto-generated clients

---

## The Atara stack

ATARA-Pay is one of four open-source services that together form **Atara — the middleware platform for autonomous AI agents:**

| Service | Role |
|---|---|
| **[Aegean Consensus](https://github.com/atara-xyz/aegean-consensus)** | Multi-agent BFT consensus for trustworthy AI decisions |
| **[Prakasa](https://github.com/atara-xyz/prakasa)** | P2P LLM compute network — distributed, censorship-resistant inference |
| **[Loka P2P Lightning](https://github.com/atara-xyz/loka-p2p-lnd)** | Lightning + Sui payment node — micro-payments and self-custody |
| **ATARA-Pay (this repo)** | Unified payment gateway aggregating all of the above |

Together they let an AI agent **think with consensus, run on shared compute, and pay autonomously** — all on open infrastructure.

---

## License

Apache-2.0.

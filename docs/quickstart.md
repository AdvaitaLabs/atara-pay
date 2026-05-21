# ATARA-Pay — Quickstart

A developer running this guide top-to-bottom should have:

- a tenant account (with a test API key in hand),
- a dual-rail wallet group (CrossMint + Tempo) for one of their users,
- their first fiat-onramp link, and
- one AI-agent session key with per-day spending limits

in **under 10 minutes**.

The flow is HTTP only. Nothing here needs an Atara SDK install — every
step is a single curl. SDKs (TypeScript / Python / Go) are generated from
the same OpenAPI spec these calls obey; see [SDK install](#sdk-install) at
the bottom.

---

## 0. Prerequisites

You need Atara-Pay running locally. Pick one:

### Local development (no Docker)

```sh
git clone https://github.com/atara-xyz/atara-pay
cd atara-pay
cp .env.example .env

# Fill in at minimum:
#   DATABASE_URL=...
#   REDIS_URL=...
#   SESSION_SIGNING_KEY=$(openssl rand -base64 48)
#   ATARA_PAY_MASTER_KEY_V1=$(openssl rand -base64 32)
#   CROSSMINT_API_KEY=sk_staging_xxx    (from crossmint.com/console)
#   TEMPO_RPC_URL=https://rpc.moderato.tempo.xyz
#   TEMPO_CHAIN_ID=42431
$EDITOR .env

make migrate-up                  # apply DB schema
make run                         # boot on :8080
```

### Docker-compose (stack only; you still run the gateway via `make run`)

```sh
docker compose up -d             # postgres + redis
make migrate-up
make run
```

Smoke check:

```sh
curl http://localhost:8080/health
# {"rails":["crossmint","tempo"],"status":"ok"}
```

---

## 1. Sign up — gets you a tenant + 2 API keys + the default policy

```sh
curl -X POST http://localhost:8080/signup \
  -H 'Content-Type: application/json' \
  -d '{
    "company_name": "AI-Tutor Inc",
    "email":        "cto@ai-tutor.example",
    "password":     "your-strong-password-12+"
  }' | jq .
```

Response (truncated):

```json
{
  "tenant":  { "id": "tn_01J…", "plan": "free" },
  "user":    { "id": "u_01J…",  "role": "owner" },
  "api_keys": {
    "test": { "id": "ak_…", "key": "sk_test_…", "prefix": "sk_test_…" },
    "live": { "id": "ak_…", "key": "sk_live_…", "prefix": "sk_live_…" }
  },
  "_notice": "Store the api_keys.{test,live}.key values now…"
}
```

Stash the **`sk_test_…`** value into your shell — you'll send it with every
subsequent request:

```sh
export ATARA_KEY=sk_test_xxx
```

**What just happened on the backend**, in one paragraph: Atara-Pay opened a
single `pgx` transaction, inserted a `tenants` row + an owner `users` row
(argon2id-hashed password) + two `api_keys` rows + a Conservative-tier
`limit_policies` row, then committed. If any step fails, the whole thing
rolls back — you'll never get a half-built tenant. The raw `key` strings
in the response only ever leave the server here; later GETs return only
the prefix.

---

## 2. Provision a wallet group for one of your users

The single call below creates **two** wallets at once: a CrossMint smart
wallet on Base (the easy path for fiat onramp) and a Tempo EOA wallet (the
cheap path for AI-agent micropayments). The keys for the Tempo wallet are
AES-256-GCM-encrypted before they touch disk.

```sh
curl -X POST http://localhost:8080/v1/wallet-groups \
  -H "Authorization: Bearer $ATARA_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "owner": { "type": "user", "ref": "your-internal-user-id-alice" },
    "display_name": "Alice"
  }' | jq .
```

Response (truncated):

```json
{
  "id": "wg_01J…",
  "owner_type": "user",
  "owner_ref":  "your-internal-user-id-alice",
  "wallets": [
    { "id": "wlt_…", "rail": "crossmint", "chain": "base",  "address": "0x…" },
    { "id": "wlt_…", "rail": "tempo",     "chain": "tempo", "address": "0x…" }
  ]
}
```

**Idempotency**: re-running this exact call returns the same group with
`"_reused": true`. Repeated user registrations don't mint duplicate
wallets.

```sh
export ATARA_GROUP=wg_01J...
```

---

## 3. Start a fiat onramp (USD → USDC on Base)

```sh
curl -X POST http://localhost:8080/v1/wallet-groups/$ATARA_GROUP/onramp \
  -H "Authorization: Bearer $ATARA_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "fiat":  { "amount": "50", "currency": "USD" },
    "asset": "USDC"
  }' | jq .
```

Response includes a `checkout_url` you embed in an iframe / new tab for
the end-user. Atara never sees the card — CrossMint hosts the form,
processes the card, and credits USDC into the wallet within seconds. Your
subscribed webhook endpoints get an `onramp.created` event immediately;
when CrossMint settles the order, the `onramp.completed` event follows.

---

## 4. Create a session key for an AI agent (the M5 special)

A session key is its own secp256k1 keypair with **its own limit policy**
attached. Hand the private key to your AI runtime; Atara enforces the
policy on every signed transfer — gateway-side AND, when
`on_chain_enforce: true`, on the Tempo precompile too.

```sh
curl -X POST http://localhost:8080/v1/wallet-groups/$ATARA_GROUP/session-keys \
  -H "Authorization: Bearer $ATARA_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "alice-ai-tutor",
    "limits": {
      "per_tx_amount":  "1",
      "daily_amount":   "5",
      "weekly_amount":  "30",
      "monthly_amount": "100",
      "allowed_recipients": ["merchant:openai", "merchant:prakasa"]
    },
    "rotation_mode": "auto_rotate",
    "rotation_interval_hours": 24,
    "on_chain_enforce": false
  }' | jq .
```

Response (truncated):

```json
{
  "id": "sk_01J…",
  "public_address": "0xAGENT…",
  "policy_id": "pol_…",
  "status": "active",
  "expires_at": "2026-05-22T03:14:15Z",
  "rail_native": false,
  "private_key": "a1b2c3d4...64 chars hex",
  "_notice": "Store the private_key value now…"
}
```

Two important things:

- **`private_key` is shown ONCE.** Stuff it straight into the agent's
  environment — Atara doesn't keep a plaintext copy.
- Set `on_chain_enforce: true` to additionally call Tempo's
  `authorizeKey` precompile. After that, even an attacker who steals the
  session-key bytes still can't spend more than `daily_amount` per 24h —
  the chain reverts past that, no matter how many requests they fire.

---

## 5. Send a transfer (smart-routed by asset)

`asset: "USDC"` routes to CrossMint; `asset: "pathUSD"` routes to Tempo.
The `signer_id` parameter (optional) tells the gateway to enforce the
session key's tighter limits instead of the tenant default.

```sh
curl -X POST http://localhost:8080/v1/wallet-groups/$ATARA_GROUP/transactions \
  -H "Authorization: Bearer $ATARA_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "to":     "merchant:openai",
    "amount": "0.05",
    "asset":  "USDC",
    "signer_id": "sk_01J…",
    "idempotency_key": "agent-job-42"
  }' | jq .
```

If the policy would deny (e.g. the daily cap is exhausted) you get a
`429` with the structured violation in the body. Same `idempotency_key`
on a retry returns the previous result instead of double-charging.

---

## 6. Register a webhook receiver

Atara fans every domain event out to every subscribed endpoint via
HMAC-SHA256-signed POSTs.

```sh
curl -X POST http://localhost:8080/v1/webhook-endpoints \
  -H "Authorization: Bearer $ATARA_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "url": "https://your-host.example/atara-webhook",
    "subscribed_events": [
      "onramp.completed",
      "transaction.succeeded",
      "limit.exceeded",
      "session_key.expired"
    ]
  }' | jq .
```

The response carries the raw `secret` once. Verify each delivery:

```python
import hmac, hashlib, base64

def verify(req_body: bytes, signature_hex: str, secret_b64: str) -> bool:
    secret = base64.b64decode(secret_b64)
    expected = hmac.new(secret, req_body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, signature_hex)
```

Deliveries carry four headers:

| Header | Purpose |
|---|---|
| `X-Atara-Event-Id` | unique per webhook_event; use as idempotency key on your side |
| `X-Atara-Event-Type` | e.g. `transaction.succeeded` |
| `X-Atara-Signature` | the HMAC hex |
| `X-Atara-Signature-Version` | which secret revision signed it (relevant during rotation) |

Atara retries failed deliveries with exponential backoff
(30s → 2m → 10m → 1h → 6h → 24h cap, 8 attempts total) before marking
the event `dead`. After 30 consecutive failures on the same endpoint we
auto-pause it; flip `status: "active"` on the endpoint to resume.

---

## 7. Pull a monthly statement

```sh
curl http://localhost:8080/v1/tenants/me/statements/2026-05 \
  -H "Authorization: Bearer $ATARA_KEY" | jq .
```

Returns per-`(asset, direction)` totals for the month plus completed
onramp totals. Useful for reconciliation; pair with the per-group
`/balance` endpoint for granular reporting.

---

## SDK install

The OpenAPI spec at `api/v1/openapi.yaml` is the source of truth.
Generated SDKs land in `sdk/<lang>` (gitignored — regenerate per release):

```sh
make sdk-ts       # TypeScript / axios → @atara-xyz/atara-pay
make sdk-python   # Python → atara_pay
make sdk-go       # Go → atarapay
make sdks         # all three
```

---

## Operational endpoints

| Endpoint | Purpose |
|---|---|
| `GET /health` | Lightweight liveness + registered-rail snapshot |
| `GET /metrics` | Prometheus text format. Includes Go runtime + HTTP traffic histograms + per-domain counters (limit violations, webhook deliveries, session-key events, rail latency) |
| `GET /v1/rails` | Same content as `/health`'s `rails`, separately routed for dashboards |

---

## Where to go next

- **Production checklist** (when it's written): rotating the keystore master key, rotating session JWT signing key, sponsor wallet topology
- **Webhook reference**: see the `Webhooks` section in the OpenAPI spec for the full event catalog with payload schemas
- **PLAN.md** (private; ask) — internal roadmap covering Lightning adapter, OTC desk, sponsored gas (M11.2), and the open KYC + tier-approval flows

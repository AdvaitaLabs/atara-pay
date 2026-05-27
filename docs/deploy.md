# Deploy Atara-Pay (testnet / staging)

End-to-end recipe to bring the gateway up and verify everything works.

## 0. Prereqs

- Docker Desktop / Docker Engine ≥ 24
- `openssl`, `curl`, `jq` on the host (used by deploy + smoke scripts)
- (Optional) CrossMint Staging API key — only if you want to test the
  CrossMint rail. Tempo-only deploys work without it.

```bash
brew install jq          # macOS
# or
sudo apt-get install jq  # Debian/Ubuntu
```

## 1. Generate secrets + boot

One command does everything: generates secrets, writes `.env`,
builds the image, runs migrations, starts the gateway, waits for
`/health`.

```bash
cd /path/to/atara-pay
./scripts/deploy-local.sh --bootstrap
```

What you should see, in order:
1. `→ generating .env from .env.example with fresh secrets`
2. `→ booting postgres + redis`
3. `→ running migrations` (output ends with `8/u session_key_pending_authorize`)
4. `→ starting atara-pay gateway`
5. `✓ gateway healthy at http://localhost:8080`
6. JSON dump of `/health` showing `"environment": "staging"` and the
   sandbox banner in the gateway logs.

If you want the CrossMint rail too, edit `.env`:

```
CROSSMINT_API_KEY=sk_staging_xxx
```

and re-run `./scripts/deploy-local.sh --rebuild` (no `--bootstrap` —
that would refuse to overwrite your existing `.env`).

## 2. Run the smoke test

This is the "does deploy actually work" check. Spins up a temporary
tenant, mints an API key, creates both platform-custody and self-custody
wallet groups, reads capabilities, and prepares (but does not submit) a
self-custody transfer.

```bash
./scripts/smoke-test.sh
```

Expected output highlights:

```
──── 1. GET /health ────
  ✓ GET /health → 200
  → environment=staging rails=tempo,crossmint  (or just tempo)

──── 4. POST /v1/wallet-groups  (platform custody) ────
  ✓ POST /v1/wallet-groups → 201
  → group=wg_… with 2 wallet(s) on rails: crossmint,tempo
  (if CrossMint not configured: 1 wallet on rails: tempo)

──── 6. GET /v1/wallet-groups/{id}/capabilities ────
  ✓ GET /v1/wallet-groups/wg_…/capabilities → 200
  {
    "custody": "platform",
    "wallets": [
      { "rail": "tempo", "can_transfer": true, "can_mint_session_key": true, ... }
    ]
  }

──── 7. POST /v1/wallet-groups  (custody=user, Tempo external address) ────
  ✓ POST /v1/wallet-groups → 201
  → group=wg_… custody=user

──── 8. POST /v1/wallet-groups/{id}/transactions/prepare ────
  ✓ POST /v1/wallet-groups/wg_…/transactions/prepare → 201
  → prepare_id=tx_… chain_id=42431 nonce=0

✓ Smoke test complete.
```

If step 8 reports an RPC error, your gateway can't reach
`rpc.moderato.tempo.xyz` — check container egress / corporate firewall.
The earlier steps still pass; just the RPC half is offline.

## 3. Manual probes (sanity / debugging)

Once the gateway is up, you can also poke it manually:

```bash
# Health + environment label
curl -s http://localhost:8080/health | jq .

# Sign up + grab the JWT
curl -s -X POST http://localhost:8080/signup \
  -H 'Content-Type: application/json' \
  -d '{"email":"me@example.com","password":"hunter2hunter2","tenant_name":"Me Inc"}' | jq .

# … take the returned session.token, paste below
JWT=eyJ…

# Mint an API key (with the session JWT)
curl -s -X POST http://localhost:8080/v1/api-keys \
  -H "Authorization: Bearer $JWT" \
  -H 'Content-Type: application/json' \
  -d '{"name":"primary","environment":"test"}' | jq .

# Use the API key from now on
KEY=sk_test_…

# Create a self-custody Tempo wallet group
curl -s -X POST http://localhost:8080/v1/wallet-groups \
  -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "owner": {"type":"user","ref":"alice-1"},
    "custody": "user",
    "external_addresses": {"tempo": "0xYOUR_TEMPO_ADDRESS"}
  }' | jq .

# Read its capabilities
curl -s http://localhost:8080/v1/wallet-groups/wg_…/capabilities \
  -H "Authorization: Bearer $KEY" | jq .
```

## 4. Inspecting / following logs

```bash
docker compose logs -f atara-pay        # gateway only
docker compose logs -f                  # everything
docker compose ps                       # status of all services
```

Sandbox banner you should see on startup:

```
[atara-pay] ┌──────────────────────────────────────────┐
[atara-pay] │  SANDBOX — environment=staging           │
[atara-pay] │  NOT for real money. Test keys only.     │
[atara-pay] └──────────────────────────────────────────┘
```

Set `ATARA_PAY_ENVIRONMENT=production` in `.env` to silence it.

## 5. Tear-down

```bash
./scripts/deploy-local.sh --down   # stop containers, keep DB data
./scripts/deploy-local.sh --nuke   # stop containers, wipe volumes
```

## 6. Test the full self-custody signing flow

The smoke script stops at `prepare` because the test environment has no
private key for the registered external address. To go the rest of the
way, you need a Tempo wallet you control (MetaMask + custom Moderato
RPC works fine):

1. Register your real address: `curl … --custody user --external-tempo 0xYOUR_ADDR`
2. Top it up with pathUSD from the Moderato faucet
   (`https://faucet.moderato.tempo.xyz`)
3. `POST .../transactions/prepare` → returns `unsigned.raw_unsigned_hex`
4. Sign that RLP with MetaMask / ethers.js using your wallet's key
5. `POST .../transactions/submit` with `signed_tx_hex` → broadcasts
6. Poll `GET /v1/transactions/{id}` or wait for the `transaction.broadcast`
   webhook

For the agent-automation flow (Phase 4 — on-chain session keys):

1. Steps 1-2 above
2. `POST .../session-keys` on the self-custody group → returns
   `unsigned_authorize.raw_unsigned_hex` + a `private_key` (the
   session key's bytes — store immediately)
3. Sign the authorize blob with your wallet's master key
4. `POST .../session-keys/{id}/submit-authorize` with the signed bytes
   → session key flips to `active`
5. From now on, an agent runtime holding the session private key can
   `POST .../transactions` autonomously, bounded by the on-chain
   precompile limits we set in step 2.

## 7. Troubleshooting

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| Deploy script aborts on `SESSION_SIGNING_KEY is empty` | Forgot to bootstrap | `./scripts/deploy-local.sh --bootstrap` |
| `/health` never returns | Gateway crash-looping | `docker compose logs --tail=100 atara-pay` |
| `keystore: no ATARA_PAY_MASTER_KEY_V* env vars set` | `.env` not picked up | `docker compose --env-file .env up …` (script does this; manual runs must pass it) |
| Migrations fail with `relation … does not exist` | Volume holds an old half-migrated DB | `./scripts/deploy-local.sh --nuke` then bootstrap again |
| `/v1/wallet-groups` returns `404 not found` | Tenant + Tempo + keystore not all configured at boot | Re-check `.env`; gateway logs print which dep is missing |
| Prepare returns RPC errors | Tempo RPC unreachable from container | Test from host: `curl https://rpc.moderato.tempo.xyz`; check container DNS / proxy |
| `/v1/wallet-groups/{id}/onramp` returns 503 `onramp_not_configured` | CrossMint not set in `.env` | Add `CROSSMINT_API_KEY` and `--rebuild` |

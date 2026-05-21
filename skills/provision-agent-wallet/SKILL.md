---
name: provision-agent-wallet
description: Create a wallet group plus a scoped session key that an autonomous agent can spend from.
allowed-tools: [Bash]
---

# Provision a wallet + session key for an autonomous agent

Use when the user says things like:
- "give my new shopping agent a wallet capped at $50/day"
- "set up a wallet for agent-007 that can only pay merchant:openai"
- "I need a session key for the trading bot, expires next Friday"

The pattern: one **wallet group** (dual-rail Crossmint + Tempo) plus one
**session key** that bounds what the agent can spend.

## Step 1 — wallet group

If the user has not named an existing owner ref, use a stable identifier
they provide (agent id, employee id, etc.). The endpoint is idempotent on
`(owner_type, owner_ref)`.

```
atara wallets create \
  --owner-type agent \
  --owner-ref  "<agent-id>" \
  --display-name "<human label>" \
  --crossmint-chain base
```

Record the returned `id` — this is `<wallet-group-id>`.

## Step 2 — session key

Pick the tightest scope that still does the job. Defaults to refuse:

- per-tx cap = single largest expected payment
- daily cap = what the user actually authorized for the day
- recipient allowlist whenever the agent's job is to pay a known set
- expiry MUST be set (server requires it)

```
atara session-keys create \
  --group       <wallet-group-id> \
  --expires-at  2026-06-01T00:00:00Z \
  --per-tx      5 \
  --daily       50 \
  --to          merchant:openai \
  --to          merchant:anthropic
```

Add `--on-chain` only when the user explicitly wants Tempo `authorizeKey`
enforcement (slower, costs gas, but tamper-proof). Default is gateway-only.

## Step 3 — hand the agent the session-key id

The CLI returns `{"id": "sk_...", ...}`. Print the `id` so the user can
inject it into the agent's runtime config. Do not print or store the raw
private key — the gateway encrypts and holds it.

## After provisioning

Verify by listing:

```
atara session-keys list --group <wallet-group-id> --pretty
```

---
name: tenant-bootstrap
description: First-time setup for a new Atara-Pay tenant — CLI config, API key, webhook endpoint, default limits.
allowed-tools: [Bash]
---

# Bootstrap a new tenant

Use when the user has just signed up and says:
- "set up Atara for me"
- "I just made an account, what now?"
- "configure my dev environment"

## 1. Local CLI

```
atara init
```

This prompts for `base_url` (default `https://api.atara.xyz`, override
to `https://api-staging.atara.xyz` for testnet) and the API key the user
copied from the dashboard. Writes `~/.atara/config.json` with 0600.

Sanity check:

```
atara health --pretty
```

## 2. Mint a separate live key (if the user only has a test key)

Skip on staging. On production:

```
atara keys create --name "primary-server" --env live
```

The response contains `secret` exactly once — tell the user to copy it to
their secret manager **now**. Subsequent listings only show the prefix.

## 3. Default limits

Suggest a starting policy. Adjust numbers from the user's actual budget.

```
atara limits update --json '{
  "per_tx":   "1000",
  "daily":    "5000",
  "monthly":  "50000",
  "currency": "USD"
}'
```

## 4. Webhook endpoint

Required for receiving `transaction.completed`, `onramp.completed`,
`limit.exceeded`.

```
atara webhooks create \
  --url   https://example.com/atara/webhook \
  --event transaction.completed \
  --event onramp.completed \
  --event limit.exceeded \
  --description "primary backend"
```

The returned `secret` is the HMAC key. Save it to env as `ATARA_WEBHOOK_SECRET`;
the response shows it only once. Rotate later with
`atara webhooks rotate-secret <id>`.

## 5. Smoke test

```
atara wallets create --owner-type user --owner-ref bootstrap-test
atara wallets list --pretty
```

Two calls — second time the `create` returns the same id (idempotent on
`owner-ref`) and HTTP 200, not 201. That confirms idempotency works.

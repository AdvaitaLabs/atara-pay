---
name: investigate-violation
description: Triage a denied transfer or a limit.exceeded webhook event.
allowed-tools: [Bash]
---

# Investigate a limit violation

Use when:
- A transfer returned HTTP 403 with `error: "limit_exceeded"`.
- A `limit.exceeded` webhook fired.
- The user asks "why did that payment fail?"

## 1. Pull current policy

```
atara limits get --pretty
```

Note the active `per_tx`, `daily`, `monthly` caps and `currency`.

## 2. Pull recent violations

```
atara limits violations --limit 20 --pretty
```

The shape per row: `id`, `tenant_id`, `wallet_group_id`, `session_key_id`,
`amount`, `currency`, `which_limit`, `created_at`, `idempotency_key`.

## 3. Diagnose

`which_limit` tells you the gate that tripped:

- `per_tx`: single transfer above the per-tx cap. Either raise the cap
  (with explicit user approval) or split the payment.
- `daily` / `monthly`: rolling window full. Wait for it to reset (UTC
  midnight for daily, calendar month for monthly) or raise the cap.
- `recipient`: session key allowlist refused the destination. Mint a new
  session key with an updated `--to` list, or send from a tenant-owned
  wallet that has no allowlist.
- `expiry`: session key is past `expires_at`. Issue a new one.

## 4. Remediation

Never silently raise limits. Confirm with the user, then either:

```
atara limits update --file ./new-policy.json
```

or mint a more permissive session key (see
[provision-agent-wallet](../provision-agent-wallet/SKILL.md)). Do not retry
the original transfer with the same idempotency key after policy changes —
generate a fresh one.

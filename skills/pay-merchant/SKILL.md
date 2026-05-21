---
name: pay-merchant
description: Send a one-off payment from an Atara-Pay wallet group to a merchant alias or external address.
allowed-tools: [Bash]
---

# Pay a merchant from a wallet group

Use when the user says things like:
- "send 5 USDC to merchant:openai from wallet wg_abc"
- "pay alice@tempo 0.25 USDC out of my main wallet"
- "settle that 12.50 with the supplier"

## Preflight

1. Confirm the CLI is configured: `atara health --pretty`. A non-200 means the
   gateway is unreachable — surface the error verbatim, do not retry blindly.
2. If the user did not name a source wallet group, list candidates:
   `atara wallets list --pretty` and ask which one to use.

## Send

Run:

```
atara tx send \
  --from   <wallet-group-id> \
  --to     <merchant-alias-or-address> \
  --amount <decimal> \
  --asset  USDC \
  --idempotency-key "$(uuidgen)"
```

- Always pass `--idempotency-key`. Without one, retries can double-spend.
- Add `--session-key-id <sk_…>` when the wallet group is governed by a
  session key the agent has been issued. Skip it for tenant-owned keys.
- Add `--rail crossmint` or `--rail tempo` only if the user explicitly
  pinned a rail. Otherwise let the server choose.

## Interpret the response

- `status: "submitted"` — the gateway accepted the transfer and is dispatching
  to the rail. The returned `id` is the tx record; poll with
  `atara health` or watch your webhook for `transaction.completed`.
- A non-2xx body has shape `{"error": "..."}`. Quote the error to the user
  and stop — do not retry without changes.

## Safety

- Never invent recipient addresses. If unsure, ask.
- Never raise `--amount` beyond what the user said.
- If the response is HTTP 403 with a `limit.exceeded` reason, do not retry.
  Tell the user which limit tripped and run [investigate-violation](../investigate-violation/SKILL.md).

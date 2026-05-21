---
name: onramp-fiat
description: Top up a wallet group with crypto purchased from a card via CrossMint.
allowed-tools: [Bash]
---

# Top up a wallet group from fiat

Use when the user says:
- "buy 100 USDC into wallet wg_abc"
- "fund the agent wallet with $25 from my card"
- "onramp 50 EUR worth of USDC"

This kicks off a hosted CrossMint checkout. The CLI returns a checkout URL
that the user (or their downstream customer) opens in a browser to enter
card details. We never see PAN.

## Run

```
atara tx onramp \
  --from          <wallet-group-id> \
  --fiat-amount   <decimal> \
  --fiat-currency USD \
  --asset         USDC
```

Add `--chain base|polygon|solana` only if the user pinned a chain; default
follows what the wallet group was created with.

## Hand off

The response includes `checkout_url`. Print it and tell the user (or paste
into their downstream surface). The transfer settles into the wallet group
asynchronously; the `onramp.completed` webhook is the source of truth.

## Testnet (sandbox)

On staging, CrossMint accepts Stripe test card `4242 4242 4242 4242`,
any future expiry, any CVC. See [docs/testnet.md](../../docs/testnet.md).

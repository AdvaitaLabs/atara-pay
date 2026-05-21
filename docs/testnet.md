# Testnet (sandbox) walkthrough

End-to-end testing of Atara-Pay against the staging stack. No real money,
no KYC, no production keys. Everything in this doc is safe to share in
demos and tutorials.

## What's connected

| Component       | Endpoint / network                         | Notes |
| --------------- | ------------------------------------------ | ----- |
| Atara gateway   | `https://api-staging.atara.xyz`            | Same code as prod, isolated DB |
| CrossMint       | `https://staging.crossmint.com`            | Card onramp + multichain wallets |
| Tempo (Hetu)    | Moderato — `rpc.moderato.tempo.xyz`, chainId `42431` | Free pathUSD via faucet |
| Card processor  | Stripe test mode                           | Accepts the test cards below |

The staging gateway tags every response and webhook with
`x-atara-environment: staging`. The `/health` body also includes
`"environment": "staging"`. Treat these as the source of truth — never
infer environment from URL alone.

## 1. Get a test API key

Sign up at `https://app-staging.atara.xyz` and copy the `sk_test_…` key.
Test keys can only call the staging gateway; they will be rejected at
`api.atara.xyz`.

## 2. Configure the CLI

```
atara init
# base URL: https://api-staging.atara.xyz
# api key:  sk_test_…
atara health --pretty
```

Expected:

```json
{
  "status": "ok",
  "environment": "staging",
  "rails": ["crossmint", "tempo"]
}
```

## 3. Fund a Tempo wallet from the faucet

Faucet UI: `https://faucet.moderato.tempo.xyz`.

1. Create a wallet group:
   ```
   atara wallets create --owner-type user --owner-ref demo-1 --crossmint-chain base
   ```
2. The response includes the Tempo wallet address under
   `wallets.tempo.address`. Paste it into the faucet.
3. The faucet drops 100 pathUSD (or current cap). Confirm:
   ```
   atara wallets balance <wallet-group-id> --pretty
   ```

## 4. Buy crypto with a Stripe test card

Run the onramp:

```
atara tx onramp \
  --from         <wallet-group-id> \
  --fiat-amount  25 \
  --fiat-currency USD \
  --asset        USDC
```

Open the returned `checkout_url`. Enter:

| Field        | Value |
| ------------ | ----- |
| Card number  | `4242 4242 4242 4242` |
| Expiry       | any future date (e.g. `12/30`) |
| CVC          | any 3 digits |
| ZIP          | any 5 digits |

Additional Stripe test scenarios:

| Card                  | Expected |
| --------------------- | -------- |
| `4242 4242 4242 4242` | Success |
| `4000 0000 0000 9995` | Insufficient funds → onramp fails, webhook `onramp.failed` |
| `4000 0025 0000 3155` | 3-D Secure challenge |
| `4100 0000 0000 0019` | Fraud block |

CrossMint settles the test purchase into the wallet group within ~30s.
Watch `onramp.completed` on your webhook endpoint, or poll
`atara wallets balance`.

## 5. Send a test transfer

```
atara tx send \
  --from   <wallet-group-id> \
  --to     0xabc...   # any tempo address, or another wallet group's
  --amount 1 \
  --asset  USDC \
  --idempotency-key "$(uuidgen)"
```

## 6. Tear down

Staging data is wiped weekly. Nothing you do on staging touches prod —
test keys, wallets, webhooks, and limits all live in the staging DB.

## Going to live

Once your integration passes the smoke tests:

1. Replace `--base-url https://api.atara.xyz` (or update `~/.atara/config.json`).
2. Mint a live key: `atara keys create --name primary --env live`.
3. Update webhook endpoint URL to your production handler.
4. Set conservative limits before the first real charge:
   `atara limits update --file initial-policy.json`.

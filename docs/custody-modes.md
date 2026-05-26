# Custody Modes & Capability Matrix

Atara-Pay supports two custody modes per wallet today, with a third reserved
for a future MPC release. The mode is chosen at wallet-group creation time
and cannot be flipped later (create a new group and migrate funds if you
change your mind).

Every wallet row in the API carries `custody`; clients should branch their
UI / SDK behavior off that field. The same information is also served
programmatically — see [`GET /v1/wallet-groups/:id/capabilities`](#programmatic-access).

## The three modes

| Mode | Who holds the private key | When to pick |
| --- | --- | --- |
| **platform** (default) | Atara (Tempo: KMS-encrypted in our DB; CrossMint: CrossMint's smart-wallet signers) | End-user has no wallet. AI agent automation. Highest UX (no signing prompts), full server-side flow. |
| **user** | The caller — you control the keypair (MetaMask, Privy, passkey, hardware wallet, …) | You already have wallets elsewhere. Compliance requires no custody by Atara. You want users to verify each transaction. |
| **mpc** | Threshold-split between Atara, your backend, and the user device | Reserved for M15. Not exposed today. |

The choice is a trade-off between **UX automation** and **custody risk**:

```
                more automation                less risk
                       ◄────────────────────────►
   platform ────────── mpc (M15) ────────── user
   (Atara holds keys)                       (you hold keys)
```

## Capability matrix

What works today, per (rail, custody) combination:

| Rail × Custody         | balance | transfer | prepare/submit | onramp | session keys |
| ---------------------- | :-----: | :------: | :------------: | :----: | :----------: |
| **Tempo / platform**       |   ✅    |    ✅    |       —*        |   —     |      ✅       |
| **Tempo / user**           |   ✅    |    🚫¹   |       ✅        |   🚫²   |      ✅³      |
| **CrossMint / platform**   |   ✅    |    ✅    |       —*        |   ✅    |      ✅       |
| **CrossMint / user**       |   ✅    |    🚫¹   |       🚫⁴       |   🚫²   |      🚫³      |
| **any / mpc**              |   ✅    |    🚫⁵   |       🚫⁵       |   🚫⁵   |      🚫⁵      |

Legend:
- ✅ supported today
- — N/A (capability doesn't apply to this combination)
- — \* prepare/submit only makes sense for user-custody; platform-custody
  signs server-side via plain `POST .../transactions`
- 🚫 returns `501 Not Implemented` with a specific `code` (see below)

Footnotes (each maps to an exact error `code` returned by the gateway):

1. `user_custody_signing_required` — server has no key; route through prepare/submit
2. `user_custody_onramp_unsupported` — CrossMint linked-external-wallet flow is Phase 4
3. ✅ Phase 4: minting a session key on a user-custody Tempo wallet now
   returns the unsigned authorizeKey tx; the wallet owner signs and posts
   it back via `POST .../session-keys/{id}/submit-authorize` to activate.
   CrossMint user-custody session keys remain 501 (`user_custody_session_key_rail_unsupported`).
4. Returned by `POST .../transactions/prepare` when the wallet's rail is not
   `tempo`. CrossMint user-custody prepare lands in Phase 4.
5. MPC reserved for M15. All write paths return 501 until then.

## Endpoint behavior cheat-sheet

| You want to … | Platform-custody | User-custody |
| --- | --- | --- |
| Read balance | `GET .../balance` | `GET .../balance` |
| Send funds | `POST .../transactions` (Atara signs) | `POST .../transactions/prepare` → sign client-side → `POST .../transactions/submit` |
| Top up (fiat → crypto) | `POST .../onramp` | Not yet (Phase 4) |
| Delegate to an agent | `POST .../session-keys` (Atara mints + broadcasts authorizeKey) | `POST .../session-keys` returns unsigned authorizeKey → owner signs → `POST .../session-keys/{id}/submit-authorize` activates |
| Receive an external transfer | Just share the address | Just share the address |

The endpoints look identical at the URL level — the difference is purely
which signing flow runs. A correctly-built client checks `wallet.custody`
once and routes its calls accordingly.

## Programmatic access

`GET /v1/wallet-groups/{group_id}/capabilities` returns the matrix scoped
to a single group:

```json
{
  "group_id":   "wg_01HG7N8K2P3Q4R5S6T7V8W9X0Y",
  "owner_type": "user",
  "custody":    "user",
  "wallets": [
    {
      "wallet_id": "w_tp_01HG7N",
      "rail":      "tempo",
      "chain":     "tempo",
      "custody":   "user",

      "can_read_balance":     true,
      "can_transfer":         false,
      "can_transfer_reason":  "user-custody wallet: server has no key. Use POST .../transactions/prepare + .../submit",
      "can_prepare_submit":   true,
      "can_onramp":           false,
      "can_onramp_reason":    "user-custody onramp requires CrossMint linked-external-wallet (Phase 4)",
      "can_mint_session_key": false,
      "can_mint_session_key_reason": "user-custody session keys require on-chain authorizeKey from the owner (Phase 4)"
    }
  ]
}
```

Front-end / SDK consumers should read these booleans rather than reproducing
the capability matrix locally — that way new combinations unlock
automatically once the gateway adds support.

## Picking a mode at create time

```bash
# Platform-custody (default) — Atara generates & holds keys
atara wallets create \
  --owner-type user --owner-ref alice-uid-123

# User-custody — you bring an address you already control
atara wallets create \
  --owner-type user --owner-ref alice-uid-123 \
  --custody user \
  --external-tempo 0xabc123...def
```

`--custody user` requires at least one of `--external-tempo` or
`--external-crossmint`; the validator returns a 400 otherwise.

## What's planned (and what's not)

| Phase | Scope | Status |
| --- | --- | --- |
| Phase 1 | Register-only: create user-custody groups, read balance, gate all writes with 501 | ✅ shipped |
| Phase 2 | Tempo user-custody transfer via prepare/submit | ✅ shipped |
| Phase 3 | Capability matrix + programmatic endpoint | ✅ shipped |
| Phase 4 | Tempo user-custody session keys (two-step authorizeKey) | ✅ shipped |
| Phase 5+ | CrossMint user-custody transfer + onramp + CrossMint user-custody session keys | ⏳ pending customer signal |
| M15      | MPC custody | reserved |

**Phase 5+ is deliberately not in flight**: CrossMint user-custody requires
their linked-external-wallet API and we want a real customer asking before
building. If you're a B-side integrator who needs any of those, file the
request via your account contact — we prioritize by demand, not by backlog
age.

## Phase 4 — Tempo user-custody session keys (two-step flow)

This unlocks the AI-agent automation story on user-custody wallets: the
end-user signs ONE on-chain authorizeKey tx (popup once, ever), then the
agent runtime spends autonomously with the resulting session key — bounded
by on-chain limits the precompile enforces.

```
1. POST /v1/wallet-groups/{wg}/session-keys
   body: { name, limits, expires_at, ... }
   →   201 with status="pending_authorize"
       + private_key (the session-key bytes — store now, never shown again)
       + unsigned_authorize: { raw_unsigned_hex: "0x..." , ... }

2. Wallet owner signs unsigned_authorize.raw_unsigned_hex with the
   WALLET MASTER KEY (e.g. MetaMask, Privy session signer, hardware wallet).

3. POST /v1/wallet-groups/{wg}/session-keys/{sk}/submit-authorize
   body: { signed_tx_hex: "0x..." }
   →   200 with status="active" + rail_tx_hash

4. From now on the agent uses the session-key private bytes in
   POST .../transactions (signing happens in the agent runtime, the
   precompile enforces the on-chain limits).
```

CLI shortcut:

```bash
atara session-keys create --group wg_… --name shopping-bot \
    --per-tx 5 --daily 50 --expires-at 2026-06-21T00:00:00Z
# returns { id: sk_…, unsigned_authorize: {…}, private_key }

# (sign unsigned_authorize.raw_unsigned_hex externally with the wallet key)

atara session-keys submit-authorize sk_… \
    --group wg_… --signed-tx-hex 0x…
# returns { session_key: { status: "active", ... }, rail_tx_hash }
```

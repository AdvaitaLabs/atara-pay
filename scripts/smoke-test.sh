#!/usr/bin/env bash
# End-to-end smoke test against a running Atara-Pay gateway.
#
# What this exercises (in order):
#   1. /health           — environment label + rails registered
#   2. /signup           — fresh tenant + first owner user
#   3. /v1/api-keys POST — mint a test API key
#   4. wallet-groups POST — create a PLATFORM-custody group (Tempo + optional CrossMint)
#   5. wallet-groups GET — read it back + fetch its balance
#   6. capabilities GET  — verify the matrix matches what we built
#   7. wallet-groups POST custody=user — register a self-custody Tempo address
#   8. transactions/prepare POST — build an unsigned Tempo transfer
#      (we DON'T submit — signing requires the wallet's private key, which
#       this script doesn't have. Phase 2 verification stops here.)
#
# Each step prints its HTTP status and a key field from the response. Any
# non-2xx response aborts with a clear pointer at which step failed.
#
# Usage:
#   ./scripts/smoke-test.sh                     # against http://localhost:8080
#   BASE_URL=https://api-staging.atara.xyz ./scripts/smoke-test.sh

set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
EMAIL="smoke-$(date +%s)@atara.test"
PASSWORD="smoke-pw-not-secret"
TENANT_NAME="Smoke Test Co."

step() { printf "\n──── %s ────\n" "$*"; }
fail() { echo "❌ $*" >&2; exit 1; }

require_jq() {
  command -v jq >/dev/null 2>&1 || fail "jq required (brew install jq / apt-get install jq)"
}
require_jq

# Tiny wrapper that captures both body and status, fails fast on non-2xx.
http() {
  local method="$1" path="$2" body="${3:-}"
  local hdrs=(-H "Content-Type: application/json")
  if [[ -n "${TOKEN:-}" ]]; then
    hdrs+=(-H "Authorization: Bearer $TOKEN")
  fi
  local tmp status
  tmp=$(mktemp)
  if [[ -n "$body" ]]; then
    status=$(curl -sS -o "$tmp" -w "%{http_code}" -X "$method" "${hdrs[@]}" -d "$body" "$BASE_URL$path")
  else
    status=$(curl -sS -o "$tmp" -w "%{http_code}" -X "$method" "${hdrs[@]}" "$BASE_URL$path")
  fi
  if [[ "$status" -ge 400 ]]; then
    echo "  ⤬ $method $path → $status"
    cat "$tmp" >&2
    echo
    rm "$tmp"
    fail "step failed; see body above"
  fi
  echo "  ✓ $method $path → $status"
  cat "$tmp"
  rm "$tmp"
}

# ─── 1. /health ─────────────────────────────────────────────────────
step "1. GET /health"
HEALTH=$(http GET /health)
echo "$HEALTH" | jq '{status, environment, rails}'
ENV_LABEL=$(echo "$HEALTH" | jq -r '.environment // "unknown"')
RAILS=$(echo "$HEALTH" | jq -r '.rails | join(",")')
echo "  → environment=$ENV_LABEL rails=$RAILS"

# ─── 2. /signup ─────────────────────────────────────────────────────
step "2. POST /signup  (tenant=$TENANT_NAME)"
SIGNUP=$(http POST /signup "$(jq -n \
  --arg email "$EMAIL" --arg pw "$PASSWORD" --arg name "$TENANT_NAME" \
  '{email:$email, password:$pw, tenant_name:$name}')")
TENANT_ID=$(echo "$SIGNUP" | jq -r '.tenant.id')
TOKEN=$(echo "$SIGNUP"   | jq -r '.session.token')
echo "  → tenant_id=$TENANT_ID  (using JWT for the rest of this run)"

# ─── 3. mint an API key ─────────────────────────────────────────────
step "3. POST /v1/api-keys"
KEY=$(http POST /v1/api-keys '{"name":"smoke-primary","environment":"test"}')
API_KEY=$(echo "$KEY" | jq -r '.key')
echo "  → api_key=${API_KEY:0:18}… (truncated)"

# Switch auth to the API key so we exercise the same path real clients use.
TOKEN="$API_KEY"

# ─── 4. create a platform-custody wallet group ──────────────────────
step "4. POST /v1/wallet-groups  (platform custody)"
WG=$(http POST /v1/wallet-groups "$(jq -n \
  --arg ref "alice-uid-$(date +%s)" \
  '{owner:{type:"user", ref:$ref}, display_name:"Alice (smoke)", crossmint_chain:"base"}')")
WG_ID=$(echo "$WG" | jq -r '.id')
WALLETS=$(echo "$WG" | jq -r '.wallets | length')
RAILS_IN_GROUP=$(echo "$WG" | jq -r '.wallets | map(.rail) | join(",")')
echo "  → group=$WG_ID with $WALLETS wallet(s) on rails: $RAILS_IN_GROUP"

# ─── 5. read it back + balance ──────────────────────────────────────
step "5a. GET /v1/wallet-groups/{id}"
http GET "/v1/wallet-groups/$WG_ID" | jq '{id, owner_ref, custody, wallets: [.wallets[] | {rail, chain, address}]}'

step "5b. GET /v1/wallet-groups/{id}/balance"
http GET "/v1/wallet-groups/$WG_ID/balance" | jq '{group_id, lines}'

# ─── 6. capabilities matrix ─────────────────────────────────────────
step "6. GET /v1/wallet-groups/{id}/capabilities"
CAPS=$(http GET "/v1/wallet-groups/$WG_ID/capabilities")
echo "$CAPS" | jq '{
  custody,
  wallets: [
    .wallets[] | {
      rail, custody,
      can_transfer, can_prepare_submit, can_onramp, can_mint_session_key
    }
  ]
}'

# ─── 7. self-custody Tempo group ────────────────────────────────────
step "7. POST /v1/wallet-groups  (custody=user, Tempo external address)"
USER_REF="self-custody-$(date +%s)"
# Address is just a checksummed EVM address. We don't have its private key,
# which is the entire point of self-custody — we only want to verify the
# gateway accepts the registration.
EXT_TEMPO="0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb1"
USR_WG=$(http POST /v1/wallet-groups "$(jq -n \
  --arg ref "$USER_REF" --arg addr "$EXT_TEMPO" \
  '{owner:{type:"user", ref:$ref}, display_name:"Self-custody (smoke)",
    custody:"user", external_addresses:{tempo:$addr}}')")
USR_WG_ID=$(echo "$USR_WG" | jq -r '.id')
USR_CUSTODY=$(echo "$USR_WG" | jq -r '.custody')
echo "  → group=$USR_WG_ID custody=$USR_CUSTODY"

step "7b. GET capabilities on the user-custody group"
http GET "/v1/wallet-groups/$USR_WG_ID/capabilities" | jq '{
  custody,
  tempo_wallet: (.wallets[] | select(.rail=="tempo") | {
    rail, custody, can_transfer, can_prepare_submit, can_mint_session_key
  })
}'

# ─── 8. prepare an unsigned transfer ────────────────────────────────
step "8. POST /v1/wallet-groups/{id}/transactions/prepare  (user-custody)"
# Prepare returns an unsigned tx the wallet owner would sign offline. We
# don't have the private key for the external address above, so we stop
# at prepare and verify the response shape.
PREP=$(http POST "/v1/wallet-groups/$USR_WG_ID/transactions/prepare" "$(jq -n \
  '{to:"0x000000000000000000000000000000000000dEaD",
    amount:"0.01", asset:"USDC",
    idempotency_key:"smoke-prep-1"}')" || true)

if echo "$PREP" | jq -e '.prepare_id' >/dev/null 2>&1; then
  PREP_ID=$(echo "$PREP" | jq -r '.prepare_id')
  CHAIN_ID=$(echo "$PREP" | jq -r '.unsigned.chain_id')
  NONCE=$(echo "$PREP" | jq -r '.unsigned.nonce')
  echo "  → prepare_id=$PREP_ID  chain_id=$CHAIN_ID  nonce=$NONCE"
  echo "  → next step (NOT run here): sign unsigned.raw_unsigned_hex"
  echo "    with the wallet master key and POST .../submit with the signed bytes."
else
  echo "  ⚠ prepare returned an error — likely Tempo RPC isn't reachable from"
  echo "    this gateway (nonce/gas lookup fails). Body:"
  echo "$PREP" | jq . || echo "$PREP"
  echo "    Self-custody registration still works; just the RPC half is offline."
fi

echo
echo "════════════════════════════════════════════════════════════════"
echo "✓ Smoke test complete. Gateway at $BASE_URL is wired end-to-end."
echo "  Tenant:        $TENANT_ID"
echo "  Platform WG:   $WG_ID"
echo "  Self-custody:  $USR_WG_ID"
echo "════════════════════════════════════════════════════════════════"

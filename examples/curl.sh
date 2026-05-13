#!/usr/bin/env bash
# Atara-Pay — quickstart curl examples.
#
# Prereqs:
#   1. cp .env.example .env  &&  fill CROSSMINT_API_KEY (use a staging key)
#   2. go run ./cmd/sangam   (server on :8080)

set -euo pipefail
BASE="${BASE:-http://localhost:8080}"

echo "== health =="
curl -s "$BASE/health" | jq .

echo
echo "== registered rails =="
curl -s "$BASE/v1/rails" | jq .

echo
echo "== create a wallet on CrossMint (base / smart) =="
WALLET=$(curl -s -X POST "$BASE/v1/wallets" \
  -H "Content-Type: application/json" \
  -d '{
    "rail":  "crossmint",
    "chain": "base",
    "type":  "smart",
    "owner": { "type": "email", "value": "alice@example.com" }
  }')
echo "$WALLET" | jq .
WALLET_ID=$(echo "$WALLET" | jq -r .id)
ADDR=$(echo "$WALLET" | jq -r .address)
echo "wallet id:      $WALLET_ID"
echo "wallet address: $ADDR"

echo
echo "== fetch balances =="
curl -s "$BASE/v1/wallets/$WALLET_ID/balances?rail=crossmint" | jq .

echo
echo "== start a fiat onramp order (USD 25 → USDC on Base) =="
curl -s -X POST "$BASE/v1/onramp/orders?rail=crossmint" \
  -H "Content-Type: application/json" \
  -d "{
    \"wallet_id\":      \"$WALLET_ID\",
    \"wallet_address\": \"$ADDR\",
    \"fiat\":           { \"amount\": \"25\", \"currency\": \"USD\" },
    \"asset\":          \"USDC\",
    \"chain\":          \"base\"
  }" | jq .

echo
echo "== send 1 USDC to another address =="
curl -s -X POST "$BASE/v1/transactions?rail=crossmint" \
  -H "Content-Type: application/json" \
  -d "{
    \"from\":   \"$WALLET_ID\",
    \"to\":     \"0x0000000000000000000000000000000000000001\",
    \"amount\": \"1\",
    \"asset\":  \"USDC\",
    \"chain\":  \"base\"
  }" | jq .

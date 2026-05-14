#!/usr/bin/env bash
# Atara-Pay — Tempo rail quickstart.
#
# Prereqs:
#   1. cp .env.example .env  &&  set TEMPO_RPC_URL + TEMPO_CHAIN_ID
#      (defaults point at testnet Moderato)
#   2. go run ./cmd/atara-pay   (server on :8080)
#
# Note: Tempo wallets created here are ATARA-Pay-issued (keys held in the
# gateway's in-memory keystore). Fund the address from a Tempo faucet before
# attempting a transfer.

set -euo pipefail
BASE="${BASE:-http://localhost:8080}"

echo "== health (should list tempo) =="
curl -s "$BASE/health" | jq .

echo
echo "== create a Tempo wallet =="
WALLET=$(curl -s -X POST "$BASE/v1/wallets" \
  -H "Content-Type: application/json" \
  -d '{
    "rail":  "tempo",
    "chain": "tempo",
    "owner": { "type": "external", "value": "agent-001" }
  }')
echo "$WALLET" | jq .
ADDR=$(echo "$WALLET" | jq -r .address)
echo "tempo address: $ADDR  (fund this from a faucet)"

echo
echo "== fetch pathUSD balance =="
curl -s "$BASE/v1/wallets/$ADDR/balances?rail=tempo" | jq .

echo
echo "== send 0.50 pathUSD to another address =="
curl -s -X POST "$BASE/v1/transactions?rail=tempo" \
  -H "Content-Type: application/json" \
  -d "{
    \"from\":   \"$ADDR\",
    \"to\":     \"0x0000000000000000000000000000000000000001\",
    \"amount\": \"0.5\",
    \"asset\":  \"pathUSD\",
    \"chain\":  \"tempo\"
  }" | jq .

echo
echo "== onramp on tempo should return 501 (unsupported) =="
curl -s -o /dev/null -w "HTTP %{http_code}\n" -X POST "$BASE/v1/onramp/orders?rail=tempo" \
  -H "Content-Type: application/json" \
  -d '{ "wallet_address": "'"$ADDR"'", "fiat": {"amount":"10","currency":"USD"}, "asset":"pathUSD" }'

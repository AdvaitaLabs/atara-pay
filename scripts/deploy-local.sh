#!/usr/bin/env bash
# Bring up Atara-Pay locally via docker compose.
#
#   ./scripts/deploy-local.sh           # build + run + migrate + healthcheck
#   ./scripts/deploy-local.sh --rebuild # force a fresh image build
#   ./scripts/deploy-local.sh --down    # tear down (keep volumes)
#   ./scripts/deploy-local.sh --nuke    # tear down + wipe volumes
#
# Pre-conditions:
#   - Docker Desktop / Docker Engine running
#   - `.env` exists with SESSION_SIGNING_KEY and ATARA_PAY_MASTER_KEY_V1 set
#     (copy from .env.example and edit, or pass --bootstrap to generate them)

set -euo pipefail

cd "$(dirname "$0")/.."

mode="up"
case "${1:-}" in
  --rebuild)   mode="rebuild" ;;
  --down)      mode="down"    ;;
  --nuke)      mode="nuke"    ;;
  --bootstrap) mode="bootstrap-then-up" ;;
  -h|--help)
    grep '^# ' "$0" | sed 's/^# \{0,1\}//'
    exit 0
    ;;
esac

bootstrap_env() {
  if [[ -f .env ]]; then
    echo "⚠️  .env already exists; refusing to overwrite. Edit it manually if needed."
    return 0
  fi
  echo "→ generating .env from .env.example with fresh secrets"
  cp .env.example .env
  local sign_key ks_key
  sign_key=$(openssl rand -base64 48)
  ks_key=$(openssl rand -base64 32)

  # Portable in-place edit: write to a temp + mv. sed -i differs across mac/linux.
  awk -v sk="$sign_key" -v ks="$ks_key" '
    /^SESSION_SIGNING_KEY=/        { print "SESSION_SIGNING_KEY=" sk; next }
    /^ATARA_PAY_MASTER_KEY_V1=/    { print "ATARA_PAY_MASTER_KEY_V1=" ks; next }
    { print }
  ' .env > .env.tmp && mv .env.tmp .env
  chmod 600 .env
  echo "✓ .env written (secrets generated, 0600 perms)"
}

if [[ "$mode" == "bootstrap-then-up" ]]; then
  bootstrap_env
  mode="up"
fi

if [[ ! -f .env ]]; then
  echo "❌ .env not found. Run \`./scripts/deploy-local.sh --bootstrap\` or copy .env.example manually."
  exit 1
fi

# Defensive: refuse to boot the gateway with blank required vars. Docker
# would silently substitute "" and the service would crash-loop with a
# cryptic keystore error.
require_var() {
  local name="$1"
  if ! grep -qE "^${name}=.+$" .env; then
    echo "❌ ${name} is empty in .env. Generate with: openssl rand -base64 32 (or 48 for SESSION_SIGNING_KEY)"
    exit 1
  fi
}
require_var SESSION_SIGNING_KEY
require_var ATARA_PAY_MASTER_KEY_V1

case "$mode" in
  down)
    docker compose --env-file .env down
    exit 0
    ;;
  nuke)
    docker compose --env-file .env down -v
    echo "✓ stack down, volumes wiped"
    exit 0
    ;;
  rebuild)
    echo "→ rebuilding atara-pay image (no-cache)"
    docker compose --env-file .env build --no-cache atara-pay
    ;;
esac

echo "→ booting postgres + redis"
docker compose --env-file .env up -d postgres redis

echo "→ running migrations"
docker compose --env-file .env run --rm migrate

echo "→ starting atara-pay gateway"
docker compose --env-file .env up -d atara-pay

echo "→ waiting for /health to return ok..."
for i in $(seq 1 30); do
  if curl -fsS http://localhost:8080/health >/dev/null 2>&1; then
    echo "✓ gateway healthy at http://localhost:8080"
    curl -s http://localhost:8080/health | python3 -m json.tool || curl -s http://localhost:8080/health
    echo
    echo "next: ./scripts/smoke-test.sh   # run an end-to-end signup→wallet→balance probe"
    exit 0
  fi
  sleep 2
done

echo "❌ /health never returned 200 within 60s"
docker compose --env-file .env logs --tail=80 atara-pay
exit 1

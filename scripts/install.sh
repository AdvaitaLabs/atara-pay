#!/usr/bin/env bash
# Atara-Pay CLI installer.
#
#   curl -sSf https://get.atara.xyz/install.sh | bash
#
# What it does:
#   1. Detect OS/arch and download the matching `atara` release tarball.
#   2. Drop the binary at $ATARA_HOME/bin/atara (default ~/.atara/bin).
#   3. Copy the bundled skills/ to $ATARA_HOME/skills (read by agent runtimes).
#   4. Print PATH instructions if ~/.atara/bin isn't already on PATH.
#
# Env overrides:
#   ATARA_HOME       install prefix          (default ~/.atara)
#   ATARA_VERSION    release tag to fetch    (default latest)
#   ATARA_BASE_URL   gateway URL to bake into config  (optional)

set -euo pipefail

ATARA_HOME="${ATARA_HOME:-$HOME/.atara}"
ATARA_VERSION="${ATARA_VERSION:-latest}"
RELEASE_HOST="${ATARA_RELEASE_HOST:-https://github.com/atara-xyz/atara-pay/releases}"

uname_s=$(uname -s | tr '[:upper:]' '[:lower:]')
uname_m=$(uname -m)
case "$uname_m" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) echo "unsupported arch: $uname_m" >&2; exit 1 ;;
esac
case "$uname_s" in
  darwin) os="darwin" ;;
  linux)  os="linux"  ;;
  *) echo "unsupported os: $uname_s" >&2; exit 1 ;;
esac

mkdir -p "$ATARA_HOME/bin" "$ATARA_HOME/skills"

if [[ "$ATARA_VERSION" == "latest" ]]; then
  url="$RELEASE_HOST/latest/download/atara_${os}_${arch}.tar.gz"
else
  url="$RELEASE_HOST/download/$ATARA_VERSION/atara_${os}_${arch}.tar.gz"
fi

echo "→ downloading $url"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

curl -sSfL "$url" -o "$tmp/atara.tgz"
tar -xzf "$tmp/atara.tgz" -C "$tmp"

install -m 0755 "$tmp/atara" "$ATARA_HOME/bin/atara"

if [[ -d "$tmp/skills" ]]; then
  cp -R "$tmp/skills/." "$ATARA_HOME/skills/"
fi

echo "✓ installed atara $($ATARA_HOME/bin/atara --version 2>/dev/null || echo dev) to $ATARA_HOME/bin/atara"

case ":$PATH:" in
  *":$ATARA_HOME/bin:"*) ;;
  *)
    echo
    echo "Add Atara to your shell profile:"
    echo "  export PATH=\"$ATARA_HOME/bin:\$PATH\""
    ;;
esac

if [[ -n "${ATARA_BASE_URL:-}" ]]; then
  mkdir -p "$ATARA_HOME"
  cat > "$ATARA_HOME/config.json" <<JSON
{"base_url": "$ATARA_BASE_URL", "api_key": ""}
JSON
  chmod 600 "$ATARA_HOME/config.json"
  echo "✓ wrote $ATARA_HOME/config.json (run \`atara init\` to add an API key)"
fi

echo
echo "Next:  atara init   # set base URL + API key"

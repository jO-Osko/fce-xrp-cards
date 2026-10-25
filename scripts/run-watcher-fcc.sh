#!/usr/bin/env bash
# Start watcher in FCC mode against the local fce proxy.
# sync-config writes wallets.json, the deploy record and the fce run stack into watcher/.env.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

node scripts/sync-config.mjs

# watcher/.env (TEE_MODE, proxy URL, API key, settler key) — do not echo secrets
set -a
# shellcheck disable=SC1091
source "$ROOT/watcher/.env"
set +a

echo "TEE_MODE=${TEE_MODE:-} TEE_CARD_URL=${TEE_CARD_URL:-} DEPOSIT=${XRPL_DEPOSIT_ACCOUNT:-}"
echo "API_KEY_set=$([ -n "${TEE_DIRECT_API_KEY:-}" ] && echo yes || echo no)"

cd "$ROOT/watcher"
exec node --env-file-if-exists=.env src/index.js

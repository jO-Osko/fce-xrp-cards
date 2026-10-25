#!/usr/bin/env bash
# Load XRPL demo wallet env from watcher/wallets.json.
# Usage (from repo root, zsh or bash):
#   source scripts/export-wallets-env.sh
#
# Do NOT use set -e here — when sourced, that would kill the interactive shell.

# Resolve this file's directory when sourced or executed (bash + zsh).
if [ -n "${BASH_SOURCE[0]:-}" ]; then
  _EXPORT_SCRIPT="${BASH_SOURCE[0]}"
elif [ -n "${ZSH_VERSION:-}" ]; then
  # zsh: %x = path of file being sourced/executed
  # shellcheck disable=SC2296
  _EXPORT_SCRIPT="${(%):-%x}"
else
  _EXPORT_SCRIPT="$0"
fi
ROOT="$(cd "$(dirname "$_EXPORT_SCRIPT")/.." && pwd)"
unset _EXPORT_SCRIPT

FILE="${WALLETS_FILE:-$ROOT/watcher/wallets.json}"
if [ ! -f "$FILE" ]; then
  echo "missing $FILE" >&2
  echo "run: cd \"$ROOT/watcher\" && npm install && node src/fund-wallets.js" >&2
  return 1 2>/dev/null || exit 1
fi

eval "$(node --input-type=module -e "
import fs from 'fs';
const w = JSON.parse(fs.readFileSync(process.argv[1], 'utf8'));
console.log('export XRPL_DEPOSIT_ACCOUNT=' + JSON.stringify(w.deposit.address));
console.log('export XRPL_SETTLEMENT_ADDRESS=' + JSON.stringify(w.settlement.address));
console.log('export XRPL_POOL_SEED=' + JSON.stringify(w.pool.seed));
console.log('export XRPL_POOL_ADDRESS=' + JSON.stringify(w.pool.address));
" "$FILE")" || {
  echo "failed to read $FILE" >&2
  return 1 2>/dev/null || exit 1
}

export CARD_PROCESSOR_CREDENTIAL="${CARD_PROCESSOR_CREDENTIAL:-swell-demo-card-credential}"
export TEE_CARD_URL="${TEE_CARD_URL:-http://127.0.0.1:8080}"

# FCC on-chain settle — synced from deployments/coston2.json into web/lib/deployment.gen.json.
node "$ROOT/scripts/sync-config.mjs" >&2 || true
eval "$(node --input-type=module -e "
import fs from 'fs';
const d = JSON.parse(fs.readFileSync(process.argv[1], 'utf8'));
if (d.fcc?.instructionSender) {
  console.log('export INSTRUCTION_SENDER=' + JSON.stringify(d.fcc.instructionSender));
}
" "$ROOT/web/lib/deployment.gen.json")" || true
export FLARE_RPC_URL="${FLARE_RPC_URL:-https://coston2-api.flare.network/ext/C/rpc}"

echo "XRPL_DEPOSIT_ACCOUNT=$XRPL_DEPOSIT_ACCOUNT"
echo "XRPL_SETTLEMENT_ADDRESS=$XRPL_SETTLEMENT_ADDRESS"
echo "XRPL_POOL_ADDRESS=$XRPL_POOL_ADDRESS  ← refill with: cd watcher && npm run mint-rlusd -- <address> <amount>"
echo "XRPL_POOL_SEED=(set)"
echo "TEE_CARD_URL=$TEE_CARD_URL"
echo "INSTRUCTION_SENDER=$INSTRUCTION_SENDER"
if [ -n "${FLARE_SETTLER_PRIVATE_KEY:-}" ]; then
  echo "FLARE_SETTLER_PRIVATE_KEY=(set)"
else
  echo "FLARE_SETTLER_PRIVATE_KEY=(unset — required for TEE_MODE=fcc settle)"
fi

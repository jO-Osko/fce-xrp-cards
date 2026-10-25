#!/usr/bin/env bash
# Mock TEE: CARD ledger on :8080 (no tee-node).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT/go"
export EXTENSION_PORT="${EXTENSION_PORT:-8080}"
export CARD_PROCESSOR_CREDENTIAL="${CARD_PROCESSOR_CREDENTIAL:-swell-demo-card-credential}"
export STATE_SNAPSHOT_PATH="${STATE_SNAPSHOT_PATH:-/tmp/card-state.json}"
echo "Mock TEE (CARD) on :${EXTENSION_PORT}"
echo "  deposit=${XRPL_DEPOSIT_ACCOUNT:-<unset>}"
echo "  settle=${XRPL_SETTLEMENT_ADDRESS:-<unset>}"
exec go run ./cmd/main.go

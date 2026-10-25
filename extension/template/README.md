# XRPL credit-card extension

CARD virtual loan ledger for the Flare Confidential Compute TEE. Register,
deposit, reserve and get-position are **direct actions**. Daily settlement
(`SETTLE_DAILY` / `SETTLE_CONFIRM`) is an **on-chain instruction** via
`InstructionSender`, so every settlement has a public Coston2 proof. The contract also
registers the extension and owns machines.

Shared ledger: [`../pkg/card`](../pkg/card) (also used by the mock TEE in `go/`).

## Layout

- `cmd/extension/main.go`: entrypoint; reads env, registers CARD handlers, serves tee-node.
- `internal/app`: FCC direct + instruction handlers + conformance fixture generator.
  `GET /state` is empty until a state verifier is registered (non-empty state blocks `toProduction`).
- `conformance/`: golden fixtures for `fce conformance`.
- `contracts/`: `InstructionSender` with payable settle senders, deploy / post-register scripts.
- `Dockerfile`: TEE image (launcher + tee-node + extension).

## Ops

| Op type | Op command | Channel | Auth |
|---|---|---|---|
| `CARD` | `REGISTER` | direct | none |
| `CARD` | `CREDIT_DEPOSIT` | direct | none (watcher-trusted) |
| `CARD` | `RESERVE` | direct | `credential` |
| `CARD` | `GET_POSITION` | direct | none |
| `CARD` | `SETTLE_DAILY` | instruction | contract `onlyOwner` (handler injects credential) |
| `CARD` | `SETTLE_CONFIRM` | instruction | contract `onlyOwner` (handler injects credential) |

Direct payloads are JSON (`pkg/card.Request`). Settle instruction messages are ABI:
`abi.encode(uint8 version)` for daily, `abi.encode(string nonce, string txHash)` for confirm.

Env: `XRPL_DEPOSIT_ACCOUNT`, `XRPL_SETTLEMENT_ADDRESS`, `CARD_PROCESSOR_CREDENTIAL`, `DEFAULT_XRP_USD`.
The account defaults are generated from `watcher/wallets.json` into `pkg/card/defaults_gen.go` by `scripts/sync-config.mjs`; see the top-level README's Configuration section.

## Testing

```bash
go test ./template/... ./pkg/card/...
go test ./template/internal/app -update   # rewrite conformance fixtures
fce conformance ./template
fce image ./template
cd contracts && forge test
```

## Deploying

```bash
fce deploy -chain coston2 -account <name> ./template
fce add-version -chain coston2 -account <name> -platform GCP_AMD_SEV ./template
fce governance -chain coston2 -account <name> -signers 0x… -threshold 1 ./template
fce status -chain coston2 ./template
# after settle ABI is live:
fce send -chain coston2 -account <name> ./template 'settleDaily()'
```

GCP bring-up: see [docs/fcc-compose.md](../../docs/fcc-compose.md) and
`tee-node-suite-deployment/envs/coston2-xrpl-credit-card`.

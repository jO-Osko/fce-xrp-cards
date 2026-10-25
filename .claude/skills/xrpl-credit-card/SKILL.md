---
name: xrpl-credit-card
description: >
  Work on the XRPL Credit Card Swell demo in this repository: non-custodial XRP
  collateral on XRPL Devnet inside Flare Confidential ComputeExtension, a virtual RLUSD loan ledger, and a mocked card
  processor plus mocked frontend to show card processor status. Use this skill whenever the task touches this
  repo, the CARD ledger, REGISTER, CREDIT_DEPOSIT, RESERVE, GET_POSITION,
  SETTLE_DAILY, SETTLE_CONFIRM, the mock TEE, the XRPL deposit watcher, the
  Next.js demo (Onboard, Position, Mock Card, Mock Card interface), Xaman, RLUSD
  settlement, extension/pkg/card, extension/template, or fce for this project.
  Read it before changing ledger behavior, the watcher, the web app, or the
  FCC extension.
---

# XRPL Credit Card

This repo is a **Swell demo**. XRP collateral and the daily RLUSD payment are real XRPL Devnet transactions (Devnet because the Lending Protocol, XLS-66, is not enabled on Testnet). The card processor and the RLUSD issuer are mocks; whoever owns the settlement address (card issuer, acquirer, off-ramp) is out of scope. The loan book lives in one Go ledger, `extension/pkg/card`, served two ways:

- **Mock TEE** (`go/`, `POST /card` on :8080) — the default local path. No tee-node. All six CARD commands stay sync HTTP.
- **FCC extension** (`extension/template/`) — same ledger. REGISTER / CREDIT_DEPOSIT / RESERVE / GET_POSITION are **direct actions** (`POST /direct`). SETTLE_DAILY / SETTLE_CONFIRM are **on-chain instructions** via `InstructionSender` (watcher key + `submissionTag=threshold`).

Keep those two surfaces on the same ledger. Do not copy business rules into `go/` or the watcher.

Humans start at [README.md](../../../README.md) and [demo-playbook.md](../../../demo-playbook.md). This skill is the map for agents.

## What to edit

| Path | Change it when |
|---|---|
| `extension/pkg/card/` | Loan rules, money math, CARD request/response types, price cache. This is the product. |
| `extension/template/internal/app/` | A new CARD command needs a handler here (direct or instruction), plus a conformance fixture. One op pair is one channel only. |
| `extension/template/contracts/` | `InstructionSender`: registration/machine ownership plus payable `settleDaily` / `settleConfirm`. Other CARD ops stay off-chain (direct). |
| `go/` | The mock HTTP server (`POST /card`, `GET /state`, snapshot). It already dispatches every command through `card.Ledger.Dispatch`. |
| `watcher/` | XRPL deposit listener and the daily settler. FCC settle uses `flare-instruction.js` (ethers → InstructionSender). |
| `web/` | Next.js UI and the `/api/card` proxy. |
| `docs/`, `scripts/`, `.env.example` | Runbook and demo wiring, when behavior the operator sees actually changed. |

Leave the FCC scaffold alone: `extension/pkg/extension`, `extension/pkg/node`, `extension/pkg/payload`, `extension/sol`, `extension/launcher`, `extension/conformance`, `extension/docs`, and the copies under `tooling/` except `tooling/cmd/register-gcp` when the task is GCP registration. `extension/pkg/card` is the exception to the scaffold's "do not touch `pkg/`" rule. Upstream merges will conflict if shared scaffold files are edited here.

`go/README.md` still describes the old multi-language spine. Ignore it. Constants and types live in `extension/pkg/card`.

When the task is inside `extension/` or `tooling/` as scaffold work (lint, image reproducibility, `fce` itself), also follow that tree's `AGENTS.md`. `fce deploy`, `add-version`, `governance`, `run`, `send`, and any command that signs a chain transaction run only when the user asks.

## Demo shape

Config never gets hard-coded: `scripts/sync-config.mjs` derives `extension/pkg/card/defaults_gen.go`, `web/lib/deployment.gen.json`, `watcher/.env` and `web/.env.local` from `watcher/wallets.json`, `extension/template/deployments/coston2.json` and the `fce run` stack (see README "Configuration"). The watcher and web start scripts run it first. Add a new address or deployment id there, not as a literal. Coston2 (real TEE) values come only from a deploy record or `fce run` stack and are empty otherwise; the Settlement page then notes "local mock TEE" in small print (`TeeStatus`, `FCC_DEPLOYED` in `web/lib/fcc.ts`); no other page mentions the TEE mode. The mock TEE needs no contract: `InstructionSender` is only for `TEE_MODE=fcc` (playbook Part D).

`demo-playbook.md` is the tested setup and demo walkthrough; keep it in step when setup or the demo flow changes. `npm run demo-user -- session | deposit <xrp> | pay-installment` (in `watcher/`) plays the cardholder with the demo `user` wallet when Xaman is not available.

Three processes, started from the repo root after `source scripts/export-wallets-env.sh`:

| Process | Start | Port |
|---|---|---|
| Mock TEE | `./scripts/run-demo-extension.sh` | 8080 |
| Watcher | `cd watcher && npm start` | 8090 |
| UI | `cd web && pnpm dev` | 3000 |

`TEE_MODE=mock` (default) talks to `POST /card`. `TEE_MODE=fcc` uses tee-proxy `POST /direct` for non-settle ops and on-chain settle instructions (see [demo-playbook.md](../../../demo-playbook.md), Part D). The browser never calls the TEE directly. Pages call same-origin `/api/card`, which proxies and, in FCC mode, keeps `TEE_DIRECT_API_KEY` on the server. Deposits use `watcher/src/tee.js`; settle uses `watcher/src/settle.js` → `flare-instruction.js` when `TEE_MODE=fcc` (`FLARE_SETTLER_PRIVATE_KEY`, `INSTRUCTION_SENDER`).

UI routes, as three apps (`web/components/shell.tsx`, `AREAS`; a top-right switch moves between them and a badge names the current one): the Cardholder app has `/` Onboard, `/position` Position (with the Convert panel), `/card` Mock Card, `/loans` Loans, `/stats` My statistics, `/faucets` Faucets; the Liquidity provider app has `/lp` Yield, `/lp/liquidity` Liquidity and `/lp/next-round` Next round (an off-chain pledge of part of the deposit to uncollateralized lending: `GET/POST /lp/allocation`, `watcher/src/lp-allocations.js`, `lp-allocations.json`; never a transaction); the Operator app has `/global` Global statistics, `/global/settlement` Settlement, `/global/broker` Mock Broker. `/settle`, `/broker` and `/liquidity` redirect to the new routes (`web/next.config.ts`). Every page that signs (Onboard, Loans, Faucets, Yield, Liquidity) does it through `useSigner()` (`web/lib/wallet.ts`): an address connected with "Connect Ledger" (`components/ledger-button.tsx`) is remembered in `localStorage` key `xrpl-cc-ledger-accounts` and signs on the Ledger everywhere, any other address signs with Xaman, and connecting an address through Xaman forgets its Ledger. Ledger signing goes over WebHID (`web/lib/ledger.ts`): the watcher autofills and encodes the transaction (`POST /hw/prepare`, only TrustSet and Payment, and only for the key's own account), the device signs those bytes, and the watcher submits (`POST /hw/submit`); see `watcher/src/hardware-wallet.js`. Every XRPL transaction the app submits carries the `SourceTag` from `app.config.json` (`xrplSourceTag`, 2610250019; sync-config copies it to `XRPL_SOURCE_TAG` in `watcher/.env` and to `deployment.gen.json`): the watcher adds it in xrpl.js `autofill` (`watcher/src/source-tag.js`, loaded by `network.js` and the setup scripts), and user-signed transactions get it forced in the Xaman payload route and the Ledger relay. Never hard-code it. Browser calls to the watcher go through the allow-listed proxy `web/app/api/watcher/[...path]`. Only the watcher talks to XRPL, over one connection per process (`watcher/src/xrpl-client.js`): public nodes throttle by IP, which also breaks Xaman on a phone sharing it. New lookups go through `xrplClient()` / `withClient()` and a watcher route, never a new `xrpl.Client` or an RPC `fetch` per request; one-shot scripts call `closeXrplClient()` before exiting. The signed-in user is `localStorage` key `xrpl-cc-session` (`xrplAddress`, `tag`, `depositAccount`).

Wallets and the network come from `watcher/wallets.json`, produced by `node src/fund-wallets.js [devnet|testnet]` (it refuses to overwrite without `--force`). Its `network` block (ws, rpc, explorer, Xaman network) and `rlusd` block are the only place the network is chosen; `watcher/src/network.js` and `web/lib/network.ts` read the synced copies. Devnet has no real RLUSD, so `fund-wallets` creates a mock issuer (DefaultRipple), trust lines for pool / settlement / user, and mints demo RLUSD; add pool liquidity with an operator deposit (`POST /operator/deposit`, the Operator app's Settlement page button); `npm run mint-rlusd -- <address> <amount>` mints to other accounts. That file, `watcher/processed.json`, all seeds, and every `.env` except the examples are local secrets. Do not commit them. Testnet-era files are in `watcher/archive/testnet/`.

## The loan

Accounts are keyed by an XRPL destination tag. `REGISTER` is idempotent per classic address (must start with `r`). Tags are allocated from 1001 upward. The deposit account and settlement address come from `XRPL_DEPOSIT_ACCOUNT` and `XRPL_SETTLEMENT_ADDRESS`; unset, the ledger uses `DefaultDepositAccount` / `DefaultSettlementAddress` from the generated `defaults_gen.go` (the FCC path always does, since `fce run` passes no extension env).

`CREDIT_DEPOSIT` adds XRP drops to `XrpAvailable` for a known tag. It is idempotent on `txHash`: a repeat returns `duplicate: true` and does not credit again. The watcher only forwards Payments to the deposit account that have a destination tag and a native XRP amount (`Amount` or `DeliverMax` as a string of drops). It records the hash in `processed.json` only after the TEE accepts it.

`RESERVE` is the card payment. It requires `credential` equal to `CARD_PROCESSOR_CREDENTIAL`. It locks enough available XRP to cover the new RLUSD debt at a **75% LTV** (`LTVCapBps = 7500`), rounded up, then checks that total debt against total collateral (available + locked) still fits under that cap. Amounts are decimal strings (`"25.00"`); the ledger stores RLUSD at 6 decimals and XRP/USD at 8 decimals. A business decline (`insufficient available XRP`, `would breach LTV cap`) is `approved: false` inside a successful envelope. A bad credential, unknown tag, or unknown command is an error (`ok: false` on the mock). Do not collapse those two outcomes.

Each approved reserve also adds the amount net of the 1% merchant fee (`CardFeeBps`) to the global `virtualSettlement` accumulator, and the fee to the pool's `TreasuryOwed`. That is what the daily batch pays (merchants and treasury), not a per-user settlement.

`SETTLE_DAILY` moves `virtualSettlement` (merchants) and `TreasuryOwed` (TEE profit, `treasuryRlusd`) into one pending settlement and zeroes both. The watcher pays the treasury, then the merchants, recording each payment per nonce in `settle-progress.json` so a retry never pays twice, then confirms. Calling it again while a settlement is pending returns the **same** nonce and amount. `SETTLE_CONFIRM` (matching `settleNonce`, non-empty `txHash`) clears that pending item and sets `wireQueued: true`. On the mock path both take `credential`. On FCC they are on-chain instructions (`onlyOwner`); the extension handler injects the configured credential into `Dispatch`. The ledger does not check the XRPL transaction. The watcher does: it submits the RLUSD Payment from `XRPL_POOL_SEED` to `settlementAddress`, waits for `tesSUCCESS`, then confirms. `wireQueued` only marks the settlement done; what the owner of the settlement address does with the RLUSD is out of scope.

XRP/USD comes from a CoinGecko poller, with `DEFAULT_XRP_USD` (demo default `2.50`) when the feed is down. Tests should `SetPrice` rather than depend on the network.

The mock TEE snapshots the ledger to `STATE_SNAPSHOT_PATH` (default `/tmp/card-state.json`) after mutations. A stale snapshot is a common reason a local demo "remembers" tags the UI no longer has.

## Loan conversion

Read the README's "Loan conversion specifics" first. The rules that matter when changing it:

- The TEE never confirms a conversion. `CONVERT_REQUEST` reserves debt for `ConversionTTL` (15 min); `CREDIT_RLUSD_REPAYMENT` (tagged RLUSD that reached the **pool**, sent by the watcher from `meta.delivered_amount`, never `Amount`) lowers debt, unlocks locked XRP in proportion (all of it on full repayment, rounded down otherwise), releases reservations oldest first, and records excess instead of crediting it. Do not add a confirm step.
- The watcher watches two accounts: XRP with a tag to the deposit account (`CREDIT_DEPOSIT`) and RLUSD with a tag to the pool (`CREDIT_RLUSD_REPAYMENT`). Untagged RLUSD to the pool is liquidity.
- `watcher/src/convert.js` is the Credit Server: one repayment account per user (`repayment-accounts.json`, verified on chain by `isRepaymentAccountFor` before reuse), mock broker countersignature (`broker.js`, checks the TEE's open conversion via `GET_POSITION`), `LoanSet`, then the tagged Payment of exactly the principal to the pool. Progress is saved per step in `conversions.json`; `POST /conversions/:id/resume` continues a failed one.
- Devnet facts found the hard way: `LoanSet`/`LoanPay` (and all vault / loan-broker transactions) cannot be Batch inners before `LendingProtocolV1_2`, so conversion and repayment are two transactions each. The broker's `CounterpartySignature` is hashed with the `CPT\0` prefix, not `STX\0` (`lending.js counterpartySignature`); xrpl.js `signLoanSetByCounterparty` produces an invalid one. `LoanBrokerSet` needs a closed-ended vault (`VaultKind: 1`, `SubscriptionDate`, `RedemptionDate`), deposits only before `SubscriptionDate`, loans only after it. xrpl.js `autofill` does not set `tfInnerBatchTxn` on inner transactions. A Batch with SignerListSet / AccountSet(asfDisableMaster) inners applied the outer but none of the inners, so repayment-account setup is sequential.
- Sign the pool repayment only after `LoanSet` validates; a pre-signed one can pass its `LastLedgerSequence` while waiting.
- `POST /loans/pay {txHash, loanId}` only signs `LoanPay` for the RLUSD a validated user→repayment-account Payment delivered, once per funding hash (`loan-payments.json`).

## Lending pool and LPs

Card spending is funded by one pool inside the TEE (`extension/pkg/card/vault.go`); the operator and third-party LPs both hold its shares (a trust-line token, currency XCLP, issued by the LP deposit account; not an MPT for wallet compatibility). README "The TEE lending pool and liquidity providers" has the model. Rules that matter when changing it:

- Card debt is stored scaled (`Account.DebtScaled`, JSON `rlusdDebt`) against `Pool.BorrowIndex`; always read it with `debtNow` / `debtOf`, never as RLUSD. Accrue (`pool.accrue`) at the start of every write that touches debt or pool value; reads use `indexAt` / `treasuryAt` without mutating.
- Interest accrues in whole `InterestAccrualPeriod` (1 minute) steps so conformance fixtures stay exact within a run. Fixed `BorrowAPRBps` 12%, `ReserveFactorBps` 10% and `CardFeeBps` 1% (merchant-paid) go to the treasury, paid out at `SETTLE_DAILY` (`treasuryRlusd`); the settle instruction ABI did not change.
- `totalAssets = cash − owed to merchants − owed to treasury + debt`. Cash changes only through ledger events (LP deposit/redeem, repayment, settlement); RLUSD reaching the pool any other way is outside the accounting on purpose.
- Out of free liquidity is a revert (`insufficient pool liquidity`), not a decline: the demo relies on operator deposits (`POST /operator/deposit`, which mints through the normal LP path). Never mint straight into the pool.
- Watcher side is `watcher/src/lp.js`: tagged RLUSD to the LP deposit account → `LP_DEPOSIT` → issue shares + sweep; shares back to it → `LP_REDEEM` → payout, or return the shares on revert. XRPL actions are queued in `lp-jobs.json` and retried every minute (share issues wait for the holder's XCLP trust line). Shares are integer units (1e6 per share) in the TEE and decimal token amounts on XRPL; `lp.js` `unitsToValue` / `valueToUnits` convert. A transaction that already has a job is never handled again (the watcher re-scans all accounts every 10 seconds (`WATCHER_RESCAN_MS`) as a safety net for missed events). `settle-progress.json` keeps a treasury payment from being sent twice on a retried settlement.
- xrpl.js reconnects dropped WebSockets without restoring subscriptions; the watcher re-backfills and resubscribes on `connected`. Keep that if you touch `listenXrpl`.

## Adding or changing a CARD command

1. Change `extension/pkg/card` and its tests. Both runtimes import this package, so one change covers mock and FCC.
2. If the command is new, register a direct or instruction handler in `extension/template/internal/app` and add a conformance fixture. Regenerate with `go test ./template/internal/app -update` from `extension/`. Instruction ops also need a payable sender on `InstructionSender.sol`.
3. Teach `watcher/` only if XRPL must observe or submit something (deposits and settlement already do).
4. Teach `web/` if a person triggers it. Keep using `teeCard()` in `web/lib/api.ts` so mock and FCC stay behind `/api/card`.
5. Update `demo-playbook.md` when the operator steps change.

Payload field names for direct/mock are the JSON in `extension/pkg/card/types.go`. FCC direct actions put that JSON in `message` and take the command from `opCommand` (`CARD` / `RESERVE`, UTF-8 left-padded into bytes32). Settle instructions use ABI (`abi.encode(uint8)` / `abi.encode(string,string)`). The mock body includes `"command"` as well.

## Verify

Match the check to the files you changed.

- Ledger or FCC handlers, from `extension/`: `go test ./pkg/card/... ./template/...`. After a fixture change, `go test ./template/internal/app -update`, then `fce conformance ./template` (`make -C tooling install` if `fce` is missing).
- Mock server: `cd go && go test ./...`.
- Watcher: there is no test suite. Read the payment filter and the settle path against the ledger responses, and do not print seeds.
- Web UI or `/api` routes: `cd web && pnpm exec tsc --noEmit`. Then run the dev server and click the affected flow (Onboard, Position, Mock Card, Loans, Liquidity, My statistics; Global statistics, Settlement or Mock Broker), including the empty and error states that flow can hit. The package manager for `web/` is pnpm; the watcher uses npm.

Do not start a live FCC deploy, a GCP apply, or an XRPL payment while verifying a code change. Those spend Devnet/Coston2 funds and need an explicit ask.

## Secrets and trust boundaries

`CARD_PROCESSOR_CREDENTIAL` is a demo shared secret, already in `.env.example`. Gate `RESERVE` (and mock settle) with it. FCC settle is gated by InstructionSender `onlyOwner` (`FLARE_SETTLER_PRIVATE_KEY`); do not put the credential in on-chain calldata. Leave `REGISTER`, `CREDIT_DEPOSIT`, and `GET_POSITION` open: the watcher is the trusted depositor, and the UI reads positions without the processor secret.

Never log a pool seed, an API secret, or a full TEE payload. Never commit `watcher/wallets.json` (it now also holds the issuer, broker and Credit Server seeds). `conversions.json`, `repayment-accounts.json`, `loan-payments.json` and `broker-log.json` in `watcher/` are local demo state. Xaman keys (`XAMAN_API_KEY`, `XAMAN_API_SECRET`) stay in `web/.env.local`.

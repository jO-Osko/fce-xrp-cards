# Demo playbook

This is a setup guide and a demo script for the XRPL Credit Card.
Part A sets up a fresh local instance from nothing.
Part B is the demo itself: a long walkthrough for a technical audience that covers every feature, with what to show, what to say, and what to check.
Each Part B step ends with a **Check** line, so you can also use it as a self-test.
Part C covers troubleshooting and resetting between rehearsals.
Part D (optional) deploys the real TEE on Coston2; no demo step needs it.

Plan about 90–105 minutes for the full demo, plus 15 minutes for setup the first time.

## What is real and what is mocked

| Piece | In this demo | Notes |
|---|---|---|
| XRPL | **Real**, XRPL Devnet | Every deposit, settlement, loan and repayment is a real Devnet transaction you can open in the explorer |
| RLUSD | **Mocked** | Devnet has no RLUSD; a demo issuer created by `fund-wallets` mints an IOU with the RLUSD currency code |
| Credit Server (the TEE) | **Mocked**, local Go server on :8080 | Same ledger code (`extension/pkg/card`) that the real TEE extension runs; the real one is packaged in `extension/template/Dockerfile` but is not used in any demo |
| Card network / processor | **Mocked** | The Mock Card page sends `RESERVE` with the processor credential |
| Daily settlement | **Real** payment, mocked counterparty | Real RLUSD payment on XRPL to the settlement address; whoever owns it (card issuer, acquirer, off-ramp) and what they do next is out of scope |
| TEE lending pool | **Mocked TEE**, real XRPL side | The pool that funds card spending lives in the TEE ledger; LP deposits, the XCLP share token, payouts and the treasury are real Devnet transactions |
| XLS-66 loans and vault | **Real**, on Devnet | Closed-ended vault, loan broker, first-loss cover, loans |
| Loan broker | **Mocked** | A separate wallet that the watcher operates, approving automatically |
| Credit Server's XRPL signing key | Held by the watcher | Should move into the TEE (README, Current Limits) |
| Xaman | Real app, optional | `npm run demo-user` does the same steps with a demo wallet |

```
 Xaman / demo-user ──XRP + tag──▶ deposit account ─┐
                                                   │  watcher (:8090) sees XRPL,
 Mock Card page ──RESERVE──▶ web (:3000) ──/api/card──▶ mock TEE (:8080)
                                                   │  calls CREDIT_DEPOSIT,
 Settlement page ──/settle──▶ watcher ──RLUSD──▶ settlement account
                                                   │  CREDIT_RLUSD_REPAYMENT
 Convert panel ──/convert──▶ watcher ──LoanSet + tagged RLUSD──▶ pool ─┘
 Yield / Liquidity ──RLUSD + LP tag──▶ LP deposit account ──▶ watcher: LP_DEPOSIT, issue shares, sweep to pool
```

The web app is three apps, with a switch at the top right and a badge saying which one you are in:

- **Cardholder app** (`/`): Onboard, Position, Mock Card, Loans, My statistics, Faucets.
- **Liquidity provider app** (`/lp`): Yield (the APY, a Ledger or Xaman wallet, and the three steps to deposit), Liquidity (the full position: withdrawals and history) and Next round (mark part of the deposit for uncollateralized lending, off chain).
- **Operator app** (`/global`): Global statistics, Settlement, Mock Broker; the card processor, operator and loan broker.

---

# Part A: set up a fresh instance

## A0. Prerequisites

- **Node 22 or newer.** The watcher uses `node --env-file-if-exists`.
- **pnpm 10** for `web/`: `corepack enable`, or `npm i -g pnpm@10`.
- **Go 1.26.9 or newer** for the mock TEE. If your Go is older, `GOTOOLCHAIN=auto` (the default) downloads the right toolchain the first time.
- **Internet access** to `s.devnet.rippletest.net` (XRPL Devnet: WebSocket on 51233, JSON-RPC on 51234), the Devnet faucet, and `api.coingecko.com` (XRP/USD price; it falls back to $2.50 when unreachable).
- **Optional: Xaman.**
  - The phone app, plus API keys from <https://apps.xumm.dev/>.
  - The app has to be able to sign on Devnet; payloads are sent with `force_network: "DEVNET"`.
  - Before a live demo, sign in once on the Onboard page to confirm this works.
  - Without Xaman, use `npm run demo-user` (shown in each step).

## A1. Install dependencies

```bash
cd watcher && npm install && cd ..
cd web && pnpm install && cd ..
```

## A2. Start from a clean slate

Skip this the very first time.
For a completely fresh instance, move the old wallets out of the way and clear the demo state:

```bash
mkdir -p watcher/archive/$(date +%Y%m%d-%H%M)
mv watcher/wallets.json watcher/archive/$(date +%Y%m%d-%H%M)/ 2>/dev/null
rm -f watcher/processed.json watcher/settlements.json watcher/last-settle.json \
      watcher/conversions.json watcher/repayment-accounts.json \
      watcher/loan-payments.json watcher/broker-log.json \
      watcher/lp-jobs.json watcher/settle-progress.json watcher/lp-allocations.json \
      /tmp/card-state.json
```

`/tmp/card-state.json` is the mock TEE's ledger (accounts, tags, debt).
Delete it whenever you start with new wallets, or old tags will point at nothing.
The browser needs no cleanup: the Settlement page only shows settlements to the current settlement address, and the card session re-registers if its tag is unknown.

## A3. Create the Devnet wallets and mock RLUSD

```bash
cd watcher && npm run fund-wallets      # defaults to devnet; refuses to overwrite wallets.json; use -- --force to overwrite
```

After an overwrite it lists the old state files to clear (A2); `setup-lending` and `setup-vault` must then run again.

<!-- -->

This creates five faucet-funded accounts:

| Account | Role |
|---|---|
| `issuer` | Mock RLUSD issuer (DefaultRipple) |
| `deposit` | Receives XRP collateral; the destination tag identifies the user |
| `pool` | The Credit Server's RLUSD pool: pays settlements, receives tagged repayments |
| `settlement` | Receives the daily settlement (stands in for the card issuer's / acquirer's account) |
| `user` | Demo cardholder for `npm run demo-user` |

It sets RLUSD trust lines for the pool, settlement and user accounts, mints 1,000 RLUSD to the user, and writes everything (including seeds) to `watcher/wallets.json`.
The pool starts empty on purpose: its liquidity comes in through LP deposits (A4b, A7b) so the TEE accounts for every RLUSD in it.
That file is a secret: never commit or share it.

**Check:** the output ends with `wrote …/wallets.json`, and every step prints a transaction hash.

## A4. Set up lending (vault, loan broker, first-loss cover)

```bash
npm run setup-lending                   # still in watcher/
```

This creates two more wallets:

- **`broker`**: the mock loan broker, standing in for the underwriting bank.
- **`creditServer`**: the second signer on every user's repayment account.

It also mints 525,000 RLUSD to the broker, creates a **closed-ended vault**, deposits 500,000 RLUSD into it, creates the loan broker, and deposits 25,000 RLUSD of first-loss cover.

**Run this at least 5 minutes before you demo conversions.**
The vault is in its subscription phase for 5 minutes, and new loans are refused (`tecTOO_SOON`) until it ends.
The script prints the time loans open (`loans open at …`).

## A4b. Set up the TEE lending pool's XRPL side

```bash
npm run setup-vault                     # still in watcher/
```

This creates three more accounts:

- **`treasury`** receives the TEE's profit (card fees and its 10% cut of interest) at each settlement.
- **`lpDeposit`** is where LPs send RLUSD with their tag. It issues the pool's share token (a transferable trust-line token, currency XCLP) and takes shares back for withdrawals; the watcher sweeps its RLUSD into the main pool.
- **`operator`** is the operator's own LP position, the "internal" liquidity.

It also sets XCLP trust lines for the operator and the demo user, so they can hold shares.

**Check:** the output ends with `share token XCLP (58434C50…) issued by r…` and `wrote …/wallets.json`.

## A5. Sync the configuration

```bash
cd .. && node scripts/sync-config.mjs
```

This writes every derived copy of the addresses, IDs and keys:

- `extension/pkg/card/defaults_gen.go`, compiled into the TEE
- `web/lib/deployment.gen.json`
- the keys the script owns in `watcher/.env` and `web/.env.local`

Two warnings are expected locally: `real TEE not deployed` and `no real TEE machine`.
Both concern the real TEE on Coston2 (Part D), which the demo doesn't use; the Settlement page then notes "Credit Server: local mock TEE" in small print.
The script also says the TEE defaults changed; for the mock TEE, restarting it is enough.

The watcher and web start scripts run this sync automatically, but run it once now so the Go defaults are in place before the mock TEE starts.

**Check:** `node scripts/sync-config.mjs --check` prints `sync-config: up to date`.

## A6. Web settings

`web/.env.local` was created from `web/.env.example`.
To use Xaman, set:

```
XAMAN_API_KEY=…
XAMAN_API_SECRET=…
```

Leave `TEE_MODE` unset (mock).
`watcher/.env` should have `TEE_MODE=mock` (the default from `.env.example`).

## A7. Start the three processes

Use three terminals, all from the repository root:

```bash
./scripts/run-demo-extension.sh         # 1. mock TEE (Credit Server) on :8080, state in /tmp/card-state.json
cd watcher && npm start                 # 2. watcher on :8090: XRPL listener, settlement, conversions, mock broker
cd web && pnpm dev                      # 3. web app on http://localhost:3000
```

**Check** (in a fourth terminal):

```bash
curl -s localhost:8090/health
# {"ok":true,"deposit":"r…","pool":"r…","processed":0}

curl -s -X POST localhost:8080/card \
  -d '{"command":"GET_STATS","credential":"swell-demo-card-credential"}' | head -c 120
# {"ok":true,"command":"GET_STATS","data":{"userCount":0,…

curl -s localhost:3000/api/pool
# {"ok":true,"address":"r…","balanceRlusd":"0","network":"XRPL Devnet",…}   (empty until A7b)
```

The watcher log should show `watching deposit account r…, pool r…, LP deposit account r…`.

## A7b. Fund the pool (operator deposit)

The pool is empty, and card payments revert with `insufficient pool liquidity` until it has RLUSD.
Press **"Deposit 10,000 RLUSD"** on Operator app → Settlement (`/global/settlement`), or:

```bash
curl -s -X POST localhost:8090/operator/deposit -d '{"amount":"500"}'
```

The mock issuer mints the RLUSD into the LP deposit account under the operator's LP tag, the TEE credits it and mints the operator's shares, and the watcher sends them and sweeps the RLUSD into the pool.

Size matters for the yield story. Supply rate = 12% × utilization × 90%, and one demo cardholder with 50 XRP can borrow only about $48, so the pool should be small:

| Pool | Borrowed | Utilization | LP supply rate |
|---|---|---|---|
| 2,500 (button + curl) | 16.5 (B5 only) | 0.66% | 0.07% |
| 2,500 | about 135 (B5 + A8 seeded cardholders) | 4.5% | 0.5% |
| 500 | about 135 | about 25% | about 3% |

So for the LP part of the demo, make one 500 RLUSD deposit with the curl command (the button adds 10,000) and seed cardholders (A8) before B7.
There is always enough liquidity for the demo's card payments and withdrawals either way.

It takes about 20 seconds: the deposit validates, the watcher credits it, then sends the operator's shares and sweeps the RLUSD into the pool.

**Check:** the watcher logs `LP deposit tag=500001 …`, `LP issue → r…` and `swept … into the pool`; Operator app → Global statistics → Lending pool shows the internal liquidity.

## A8. Optional: populate the operator view

```bash
node scripts/seed-cardholders.mjs 5     # 5 fake cardholders with deposits and card swipes, via the web proxy
```

These cardholders exist only inside the TEE ledger: their deposits are credited directly, not on XRPL.
Their swipes (about 100–120 RLUSD for 3–5 cardholders) borrow from the pool too, which raises its utilization for the LP story (B7).
Seed them **after** you register the demo user (step B3) if you want the demo user to have tag 1001, and before B7.

---

# Part B: the demo

Each step has three parts:

- **Show:** what to click or run.
- **Say:** the point to make.
- **Check:** what you should see.

The explorer is <https://devnet.xrpl.org>; every hash in the UI links to it.

## B1. Architecture and trust model (5–10 min)

**Show:** the README diagrams, the table at the top of this file, and `extension/pkg/card/ledger.go`.

**Say:**

- The Credit Server runs inside a TEE (Flare Confidential Compute).
- Each user's balances, card payments and their timing stay inside it; the chain only ever sees deposits, one aggregated settlement per day, and loans the user chose to make public.
- Approvals are computed in memory, so they are fast enough for a card network: well under the roughly one-second budget.
- Collateral is XRP, deposited with a destination tag that identifies the user.
- Card spend is a **virtual RLUSD loan** at a 75% loan-to-value cap.
- No credit check: the limit is the collateral.

Then explain the two ways the same ledger code is served:

- **Mock TEE** (`go/`): a plain HTTP server, used today.
- **FCC extension** (`extension/template/`): the same ledger behind tee-node.
  - Every command except settlement is a direct action (card, conversion and liquidity commands: eleven in all).
  - Daily settlement (`SETTLE_DAILY`, `SETTLE_CONFIRM`) is an on-chain instruction on Coston2.
  - Its Dockerfile is what would run inside the TEE.

## B2. Configuration without drift (3 min)

**Show:**

- `watcher/wallets.json`: the `network`, `rlusd`, `lending` and `vault` blocks only. **Don't show the seeds.**
- `scripts/sync-config.mjs`
- `extension/pkg/card/defaults_gen.go`

**Say:**

- There are three sources of truth: the wallets, the `fce` deploy record, and the `fce run` stack. Every other copy is generated.
- The TEE's deposit, settlement, pool, LP deposit, treasury and operator accounts, the RLUSD issuer, the share token and the loan broker are compiled into the image, so they are part of what the TEE attests to. A different settlement address means a different code hash.

**Check:** `node scripts/sync-config.mjs --check` reports `up to date`.

## B3. Onboarding (5 min)

**Show:** the Onboard page.

- **With Xaman:** "Connect Xaman", scan, sign in. The app calls `REGISTER` and stores the session.
- **Without Xaman:** run this, paste the printed line into the browser console on `localhost:3000`, and reload:

  ```bash
  cd watcher && npm run demo-user -- session
  ```

**Say:**

- `REGISTER` gives the XRPL address a destination tag (1001 and up), the user's identity in the ledger.
- It is idempotent: the same address always gets the same tag.

**Check:** the Onboard page shows the tag and the deposit account. Position shows zero balances.

## B4. Deposit collateral (5 min)

**Show:**

- **With Xaman:** deposit 50 XRP on Onboard.
- **Without Xaman:** `npm run demo-user -- deposit 50`.

Watch the watcher terminal, then open the Position page.

**Say:**

- A plain XRPL Payment to the deposit account with the user's tag.
- The watcher sees it, calls `CREDIT_DEPOSIT`, and the TEE credits the drops.
- Crediting is idempotent on the transaction hash: a replay or a watcher restart never credits twice.
- Withdrawing collateral is not implemented in this demo.

**Check:**

- The watcher logs `deposit tag=1001 drops=50000000 …` and then `credited`.
- Position shows 50 XRP available.
- The transaction on the explorer has `DestinationTag: 1001`.

## B5. Card payments (10 min)

**Show:** the Mock Card page.

1. Pay "Coffee at Swell Café" (4.50).
2. Pay "Uber to the venue" (12.00).
3. Try "Hotel deposit hold" (50.00).

Then open Position.

**Say:**

- `RESERVE` is the card network's authorization request.
- It locks enough XRP to cover the new debt at 75% LTV, rounded up, and borrows the amount from the TEE's lending pool.
- The cardholder owes the full amount, accruing a fixed 12% a year from now (in one-minute steps). The merchant is owed 99%; the 1% merchant fee is TEE profit.
- If the pool lacks free cash the payment reverts; the operator keeps it funded, so in the demo it never does.
- Nothing touches the chain: amount, merchant and time stay inside the TEE.
- The limit is `collateral × XRP price × 0.75`. Work it out live: 50 XRP at $1.30 is $65 of collateral, a $48.75 limit. After 16.50 of spend, the $50 hotel hold would take total debt to $66.50, so it is declined.
- A decline is a successful answer (`approved: false` with a reason), which is different from an error (bad credential, unknown tag). The card network needs that distinction.
- The demo's price comes from CoinGecko (or the $2.50 fallback), and stale prices are not rejected (Current Limits).

**Check:**

- The first two payments are approved, and Position shows locked XRP and about 16.50 RLUSD of debt.
- The hotel hold is declined with `insufficient available XRP` or `would breach LTV cap`.
- If XRP is above about $1.77, 66.50 still fits under the limit. Then try a larger amount, or show a decline with a smaller deposit.

## B6. Daily settlement (7 min)

**Show:** Operator app → Settlement (the switch at the top right moves between apps).

- Fund the pool card: balance, address, issuer, and the "Deposit 10,000 RLUSD" operator-deposit button.
- "Settle".

**Say:**

- `SETTLE_DAILY` freezes the day's total into one pending settlement with a nonce.
- The watcher pays it in RLUSD from the pool on XRPL: the merchants' total (card spend less the 1% fee) to the settlement account, and the TEE's profit (card fees plus 10% of the interest accrued so far) to the treasury account. Then `SETTLE_CONFIRM` clears it.
- The chain sees **one** aggregate payment a day, not the individual card transactions. That is the confidentiality story.
- Calling settle again while a settlement is pending returns the same nonce, so a crash between steps can be retried.
- On the real TEE, the two settle steps are on-chain instructions through `InstructionSender` on Coston2, which gives a public proof. The mock does the same steps over HTTP.
- Devnet RLUSD is mocked: the operator-deposit button stands in for the issuer, and it goes through the LP path so the TEE knows about every RLUSD in the pool.

**Check:**

- Settling takes about 5 seconds: one Devnet payment plus the TEE confirmation.
- The settlement history shows the merchants' amount (16.335 if you only did B5: 16.5 less 1%), "+ 0.165 to treasury" (plus a few micro-RLUSD of interest), a link to the RLUSD payment, and "Settled".
- The treasury account on the explorer received the profit.
- The pool balance went down by both amounts (merchants and treasury).

## B7. Liquidity provisioning (10 min)

**Show:** the Liquidity provider app. It is independent of the card: its own wallet connection, its own session.

- **Yield** (`/lp`): the APY at the top, then "Connect Ledger" (Chrome, Edge or Brave; the Ledger unlocked with the XRP app open) or "Use Xaman instead". The account needs XRP first (Faucets). Then the three steps, each confirmed on the device: "Add RLUSD trust line", "Add XCLP trust line" (get RLUSD on Faucets in between), and "Deposit". "Your earnings" appears once the shares are in, and grows every minute.
- **With Xaman on Liquidity:** "Connect Xaman" and "Add XCLP trust line"; on Faucets, "Add RLUSD trust line" and "Get 5,000 RLUSD"; back on Liquidity, deposit 100. Then withdraw some shares.
- **Without Xaman:** paste the snippet into the console and reload, then deposit and redeem from the terminal:

  ```bash
  cd watcher
  npm run demo-lp -- session          # reserves the LP tag, prints the snippet
  npm run demo-lp -- deposit 200
  npm run demo-lp -- redeem 50        # 50 shares back
  npm run demo-lp -- position
  ```

- **Next round** (`/lp/next-round`), once there is a deposit: drag the slider to mark, say, 40% for uncollateralized lending, watch the blended APY rise, and "Save my choice". Say that this is a pledge for the next round, saved off chain: nothing is signed and nothing moves.
- Any page that signs also takes a Ledger: "Connect Ledger" on Onboard, Liquidity and Faucets ("Use my Ledger"). From then on that address signs on the device everywhere (collateral deposit, loan installments, trust lines, LP deposits and withdrawals).

**Say:**

- One pool funds every card payment. The operator's liquidity and LPs' liquidity are mixed in it and earn the same, like suppliers to a Morpho vault; Global statistics shows the split as internal vs LP.
- Depositing: RLUSD to the LP deposit account with your reserved tag. The TEE (`LP_DEPOSIT`) mints shares at the current share price and the watcher sends them; the RLUSD is swept into the main pool.
- Shares are a trust-line token on XRPL (XCLP), transferable like any other token; you add a trust line once before you can hold them. (An MPT would fit better, but trust lines work in every wallet: README.)
- Earning: card debt accrues 12% a year; 90% of that raises the share price (the TEE keeps 10%). Supply rate = 12% × utilization × 90%.
- Withdrawing: send shares back to the LP deposit account; they are burned, the TEE (`LP_REDEEM`) prices them, and the pool pays the RLUSD. If free cash were short, the TEE reverts and the shares are returned.
- Interest is real but small at demo sizes. A minute after card spending the debt is a few micro-RLUSD higher (16.500003 after 70 seconds) and "Interest earned by LPs" starts counting. The share price stays at 1 to six decimals unless the pool is small and well used; do the arithmetic out loud with the supply rate instead (12% × utilization × 90%).

**Check:**

- After the deposit (about 5 seconds): "shares sent" in History, your shares and their value at the top, "Pool liquidity" up by the deposit. Right after depositing the share issue can show as pending for a moment.
- After the withdrawal: "paid", fewer shares, and the RLUSD back in the wallet.
- Operator app → Global statistics → Lending pool: internal vs LP liquidity, utilization, rates, TEE profit.

## B8. Converting card debt to an XLS-66 loan (15 min)

Make sure at least 5 minutes have passed since `setup-lending` (step A4).

**Show:** first Operator app → Mock Broker (the broker, its terms, the vault). Then switch back to the Cardholder app → Position → "Convert to an XLS-66 loan": convert part of the debt (for example 10), then the rest.

Expect the first conversion to take about 35 seconds, because it creates the repayment account (four transactions), and later ones about 12 seconds.
Converting "all" includes the interest accrued so far (for example 6.500015 instead of 6.5), so the card debt ends at exactly 0.
Every step waits for a validated Devnet ledger, so use that time to talk through the steps.

**Say:** walk through the step tracker as it fills in.

1. **TEE reserved the debt.**
   - `CONVERT_REQUEST` reserves the amount for 15 minutes and names the exact loan to take: broker, terms, pool and tag.
   - The same debt cannot be converted twice.
2. **Repayment account.**
   - One per user, created on the first conversion and reused after that.
   - A 1-of-2 multisig of the user's key and the Credit Server's key, with its own master key disabled.
   - Open it on the explorer: the signer list and the disabled master key are visible.
3. **Loan broker approved.**
   - The mock broker countersigns only a loan that matches an open TEE conversion for this user's repayment account, with the standard terms.
   - Every decision is on the Mock Broker page.
4. **XLS-66 loan taken.**
   - `LoanSet` with the broker's `CounterpartySignature`; the principal comes from the vault into the repayment account.
5. **Principal repaid to the pool with your tag.**
   - Sent immediately after, for exactly the principal. The user signs nothing in this step and can't add anything to it.
6. **TEE credited the repayment.**
   - The watcher sees tagged RLUSD arrive at the pool and calls `CREDIT_RLUSD_REPAYMENT`.
   - That lowers the debt and unlocks the same share of the locked XRP.

Then make the design points:

- **The TEE never confirms a conversion.**
  - It only sees debt repaid to the pool under the user's tag.
  - If that payment never happens, the reservation lapses and nothing changed.
  - Conversion is just one way to start repaying; the loan itself is between the user and the broker, on XRPL.
- **Steps 4 and 5 should be one atomic Batch.**
  - Devnet's rippled refuses lending transactions inside a Batch until the `LendingProtocolV1_2` amendment.
  - Until then they are two transactions, and a stalled conversion can be resumed.
  - README: "Lending protocol versions and the underwriting model".
- **The vault is closed-ended** (`LendingProtocolV1_1`).
  - Liquidity is fixed after its subscription window.
  - Loans must end before its redemption date.
- **Real-world model.** A bank would underwrite: run the broker, provide most of the liquidity, put up the first-loss cover, and open new vaults as old ones run out. This is an example structure; the issuer decides the real one.

**Check:**

- The tracker shows every step as done, with links to the `LoanSet` and the pool payment.
- Position: debt drops by the converted amount, locked XRP drops proportionally, and the repayment appears under "Debt repayments".
- The second conversion shows "(reused)" on the repayment account.
- The Mock Broker page shows two approvals.
- **Optional failure case:** enter more than the convertible amount; the TEE refuses with `amount exceeds convertible debt`.

## B9. Repaying the loan (7 min)

**Show:**

- **With Xaman:** the Loans page.
  - If the Xaman account has no RLUSD yet, open Faucets: "Add RLUSD trust line" (sign in Xaman), then "Get 5,000 RLUSD".
  - Then "Pay installment" and sign.
- **Without Xaman:** `npm run demo-user -- pay-installment`.

**Say:**

- An installment is two steps: the user sends RLUSD to their repayment account, then the Credit Server signs `LoanPay` for exactly the amount that arrived.
- It checks that the funding payment is validated, comes from the user, and goes to the user's repayment account, that the loan belongs to that account, and that the funding payment was not already used.
- After `LendingProtocolV1_2` this becomes one Batch signed in Xaman.
- The TEE is not involved: by now the loan is an ordinary XRPL loan.

**Check:**

- The loan's "Payments left" drops from 12 to 11.
- The `LoanPay` transaction is on the explorer.
- Cardholder app → My statistics shows it under "Installments paid".

## B10. Statistics (5 min)

**Show:** both statistics pages.

- **Cardholder app → My statistics:**
  - Card account tiles: collateral, locked, debt, credit used with its meter, total spend, debt repaid.
  - Loan tiles and the loan table with progress.
  - The activity timeline with its filters.
- **Operator app → Global statistics:** the aggregate view over every cardholder, and the **Lending pool**: internal (operator) vs LP liquidity, utilization, the 12% borrow and resulting supply rate, and TEE profit (card fees + interest cut) with what was already paid to the treasury. Seed cardholders with A8 if it looks empty.

**Say:**

- The operator view comes from the TEE (`GET_STATS`) and the per-user view from `GET_POSITION`; both are open in this demo (Current Limits).
- Loans come from XRPL directly, and conversions and installments from the watcher.

**Check:** the timeline lists the deposit, the card payments, the conversions, the repayments with their unlocked XRP, and the installment, newest first.

## B11. Engineering deep dive (10–15 min, pick what the audience wants)

- **Ledger and tests:** `extension/pkg/card/ledger.go` and `conversion.go`, with fixed-point big-integer money math. Run:
  ```bash
  cd extension && go test ./pkg/card/... ./template/...
  ```
- **Conformance against the real extension interface:**
  ```bash
  cd tooling && go build -o /tmp/fce ./cmd/fce
  cd ../extension && /tmp/fce conformance ./template
  ```
  It builds the extension and replays the fixtures in `template/conformance/` through tee-node's interface.
  Expect "every required check passed" with two warnings. Both are documented in `template/fce-i-know-what-im-doing`: `RESERVE` and `CONVERT_REQUEST` aren't idempotent.
- **What would run inside the TEE:** `extension/template/Dockerfile` (launcher + tee-node + extension, reproducible build) and `template/internal/app` (direct handlers and settle instructions). This demo doesn't deploy it.
- **The XRPL details found while building:**
  - The broker's countersignature uses the `CPT` signing prefix, which xrpl.js's helper doesn't (`watcher/src/lending.js`).
  - Lending transactions can't go inside a Batch before `LendingProtocolV1_2`.
  - `LoanBrokerSet` requires a closed-ended vault.
- **Current Limits** in the README: what was left out on purpose, and the TODOs for moving the Credit Server key and the broker check into the TEE.

## B12. Likely questions

| Question | Answer |
|---|---|
| What does the chain learn about my card use? | Your deposits, the day's total for everyone (not per user), and any loan you convert to. Not individual payments, merchants or times. |
| What if the watcher lies? | Today it is trusted: it reports deposits and repayments and holds the cosigner key. Production: the TEE checks XRPL itself and holds the key (Current Limits). |
| What if XRP drops? | New payments are limited by the 75% LTV cap at the current price. Liquidations are not implemented. |
| Why convert to a loan at all? | It frees the XRP collateral and turns revolving card debt into a fixed schedule with interest, underwritten by a lender. |
| Who takes the default risk? | The broker's first-loss cover first, then the vault's depositors. The loan is unsecured once the collateral is freed. |
| Why two transactions instead of a Batch? | Devnet's rippled doesn't allow lending transactions inside a Batch until `LendingProtocolV1_2`. |
| Is RLUSD real? | Not on Devnet: a mock issuer with the same currency code. Lending isn't enabled on Testnet, where real test RLUSD exists. |
| Where does LP yield come from? | Cardholders' 12% interest on what they borrowed; LPs get 90% of it, in proportion to their shares, through the share price. |
| What does the TEE earn? | 1% of every card payment (paid by the merchant) and 10% of the interest, paid to the treasury account at settlement. |
| Can LPs lose money? | In this demo debt is overcollateralized and never defaults, but there are no liquidations; a real pool would need them (Current Limits). |
| Does collateral have to be XRP? | No. Any XRPL token can be collateral, trust-line or MPT. The strongest case is RWA (tokenized treasuries, money funds, commodities): XRPL already hosts a lot of RWA capital, and RWA allow a much better risk model (per-asset LTV and haircuts, NAV and oracle prices, issuer controls) than a volatile asset. README: "Collateral beyond XRP". |
| Can a user have several loans? | Yes, all on the same repayment account, possibly from different vaults. |

---

# Part C: troubleshooting and reset

| Symptom | Cause and fix |
|---|---|
| Conversion fails at "taking loan" with `tecTOO_SOON` | The vault is still in its subscription window: wait until the time `setup-lending` printed, then press Resume. |
| Conversion fails with `broker refused: no open TEE conversion` | The 15-minute reservation lapsed (or the mock TEE restarted without its state). Start a new conversion. |
| `loan conversion not configured` | `setup-lending` or the sync wasn't run before the mock TEE started: run `node scripts/sync-config.mjs` and restart it. |
| Deposits not credited | The watcher re-scans its accounts every 10 seconds, so a missed event is picked up within about 10 seconds. Otherwise check the watcher terminal. `unknown tag` means the mock TEE lost its state, or the tag belongs to an old ledger; delete `/tmp/card-state.json` and register again. A refused deposit is retried every 5 minutes, so one sent before its tag existed is credited once it does. |
| Settlement fails with `tecPATH_DRY` or "pool has …" | The pool is short of RLUSD: press "Deposit 10,000 RLUSD" (operator deposit) on the Settlement page. |
| Card payment fails with `insufficient pool liquidity` | Same: make an operator deposit (A7b). |
| LP shares stay "pending" | The holder has no XCLP trust line; add it ("Add XCLP trust line"), and the watcher retries within a minute. |
| `tecUNFUNDED_PAYMENT` from `demo-user` / `demo-lp` | The demo wallet ran out of XRP after many rehearsals: `npm run demo-user -- faucet`. |
| Loans page: installment fails with `tecPATH_DRY` | The user has no RLUSD or no trust line: use the two buttons on the Loans page. |
| Repayment account runs out of XRP | The watcher tops it up from the pool before each loan. If the pool runs low on XRP, send it Devnet XRP from the faucet. |
| Xaman doesn't show the request | Xaman must be able to sign on Devnet; check its network settings. Use `npm run demo-user` as a fallback. |
| `XRPL_WS is not set` when the watcher starts | Run `node scripts/sync-config.mjs` (`npm start` does this automatically). |
| Web pages show no data | The watcher or the mock TEE isn't running; check the A7 health checks. |

**Reset between rehearsals.**

- **Keep the wallets and vault**, so there is no 5-minute wait:
  - Stop the mock TEE and the watcher.
  - Delete `/tmp/card-state.json`, plus `watcher/conversions.json`, `watcher/loan-payments.json`, `watcher/broker-log.json`, `watcher/lp-jobs.json`, `watcher/settle-progress.json` and `watcher/lp-allocations.json`.
  - Keep `watcher/processed.json`: it stops the watcher from replaying old deposits, repayments and LP transactions into the fresh TEE ledger.
  - Start both again, then make a new operator deposit (A7b): the TEE's pool starts empty again.
  - The demo user must register and deposit XRP again (B3, B4): the old collateral belongs to the old ledger.
  - LP shares issued before the reset are still on XRPL, but the new TEE ledger doesn't know them; redeeming them is refused and they are sent back. For a clean LP demo, use a fully fresh instance.
  - Old loans and the repayment account stay on Devnet, and the Loans page still shows them.
- **Fully fresh:** repeat A2–A5.

**Devnet resets.** Devnet is reset from time to time; all accounts disappear.
If the faucet accounts no longer exist (`actNotFound` in the logs), repeat A2–A5.

**Known issue.** `go/Dockerfile.demo` does not build as it stands: its build image uses an older Go than `go.mod` requires, and its build context doesn't include `../extension`.
Run the mock TEE with `scripts/run-demo-extension.sh` instead.

---

# Part D (optional): deploy the real TEE on Coston2

None of Part B needs this: the mock TEE settles over HTTP, with no contract and no chain.
Deploy the real TEE (Flare Confidential Compute) when you want card commands to run as direct actions on a real tee-node and daily settlement to go through on-chain instructions with a public Coston2 proof.
Run it on your own machine; it signs Coston2 transactions with your key.

## D0. Prerequisites

- `fce`, built from this repository: `cd tooling && go build -o ~/bin/fce ./cmd/fce` (or `make install`).
- Foundry (`forge`, `cast`) and a keystore account funded with Coston2 C2FLR: `cast wallet import <name> --interactive`. This account becomes the owner of `InstructionSender` and of the extension.
- Docker (image build and the local machine).
- An indexer database description for tee-proxy (`indexer.toml`: host, port, database, username, password).
- Parts A3–A5 done, so `defaults_gen.go` holds the right Devnet accounts: they are compiled into the image.

## D1. Deploy and register (where `InstructionSender` is deployed)

```bash
cd extension
fce deploy -chain coston2 -account <name> ./template
```

This deploys `InstructionSender` (from `template/contracts`), registers the extension on Coston2 and writes `template/deployments/coston2.json` with the sender address, extension id, deployer and registration transaction.
Re-run it (with the old record moved away) whenever you need a new contract.

## D2. Register the image, set governance

```bash
fce add-version -chain coston2 -account <name> -test ./template     # builds the image, registers its code hash for a test machine
fce governance -chain coston2 -account <name> -signers 0x… -threshold 1 ./template
```

Use `-platform GCP_AMD_SEV` instead of `-test` for real confidential hardware.
Any change to the extension code or to `defaults_gen.go` changes the code hash, so it needs a new `add-version`.

## D3. Run a machine

```bash
fce run -chain coston2 -account <name> -indexer-db indexer.toml -detach ./template
```

It starts tee-node and tee-proxy locally, registers the machine, takes it to production and opens a quick tunnel; `fce stop ./template` stops it.

## D4. Point the demo at it

```bash
cd .. && node scripts/sync-config.mjs
```

This fills the web app's Coston2 details (sender, extension id, registration and production transactions, tunnel) and, in `watcher/.env`, `INSTRUCTION_SENDER` and the direct-action API key.
Then set, by hand:

- `TEE_MODE=fcc` in `watcher/.env` and `web/.env.local` (sync-config then also writes the proxy URL as `TEE_CARD_URL`; run it again).
- `FLARE_SETTLER_PRIVATE_KEY` in `watcher/.env`: the key of the `-account` above, which owns `InstructionSender` (settlement calls are owner-only).

Start the watcher and the web app as in A7 (not the mock TEE), then make an operator deposit (A7b): the real TEE's pool starts empty too.

**Check:** the header shows the instruction sender and the machine as `PRODUCTION`; `fce status -chain coston2 ./template` matches the record; after a settlement, its history row links the `SETTLE_DAILY` and `SETTLE_CONFIRM` Coston2 transactions; card payments link their TEE direct actions.

The real TEE keeps its ledger in memory only (README, Current Limits): restarting the machine loses accounts, debt and the pool.

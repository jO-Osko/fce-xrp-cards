# Feedback on docs, libraries and protocols

What we ran into while building the XRPL credit card: XRPL itself and its amendments, Devnet, xrpl.js, Xaman, Ledger, and Flare Confidential Compute.
Each item says what happened, what it cost us, and what would have helped. The last section is what worked well, and there is a lot of it.

Versions as we used them (October 2026): rippled 3.4.1 on Devnet, xrpl.js 4.7.0, `@ledgerhq/hw-app-xrp` 6.38.0, `@ledgerhq/hw-transport-webhid` 6.36.0, the Xaman platform API, and FCC with tee-node v0.0.29.

## The short list

1. **Lending can't go in a Batch yet**, so taking a loan and repaying it are two transactions with a gap between them (`temINVALID_INNER_BATCH`).
2. **xrpl.js signs a loan broker's counterparty signature with the wrong prefix**, so Devnet rejects it; we compute it ourselves.
3. **Devnet has one public endpoint** and nothing to fall back on, and it resets.
4. **Closed-ended vaults** (`LendingProtocolV1_1`) changed what a vault can be, and the errors only make sense after reading rippled's source.
5. **xrpl.js drops subscriptions on reconnect**, silently.
6. **Wallet support for MPTs is uneven**, so we used a trust-line token where an MPT was the better fit. (Ledger still does not support it which means that you are stuck with old-school trust lines if you want to use a hardware wallet.)
7. **A Single Asset Vault is not an EVM vault.** It has no strategy of its own: its only source of yield is XLS-66 loans, so on its own it is a box that holds assets.
Neither can it have a manager that can withdraw

## XRPL protocol and amendments

### No lending inside Batch before `LendingProtocolV1_2`

We wanted to take the loan atomically: one Batch with `LoanSet` (principal from the vault into the user's repayment account) and a Payment of exactly that principal to the card's pool.
Devnet answered `temINVALID_INNER_BATCH`.
rippled's `Batch.cpp` keeps a list of transaction types refused as inner transactions until `LendingProtocolV1_2` is enabled: every `Vault…`, `LoanBroker…` and `Loan…` transaction, including `LoanSet` and `LoanPay`.

- **Cost:** taking a loan is now two transactions. Between them, borrowed RLUSD sits in an account the user can also sign for. We send the second one immediately and added a resume step, and the README lists the gap as a limit. Repaying a loan has the same split (a Payment, then `LoanPay`).
- **What would have helped:** the XLS-56 (Batch) docs and the XLS-66 (lending) docs saying, next to each other, which transaction types a Batch refuses and which amendment lifts it. We found out from the error, then from the source.
- **Note for after V1_2:** an inner transaction can't carry a signature, so the broker no longer signs the inner `LoanSet`. The inner `LoanSet` names it in `Counterparty`, and the broker signs the whole Batch as a `BatchSigner`. That is a different approval flow from the non-Batch one, and worth a worked example.

### A Batch that succeeds while its inner transactions don't

Setting up a repayment account was meant to be one Batch: a trust line, a 1-of-2 signer list, and disabling the master key.
The outer transaction validated with `tesSUCCESS`, but the account came out unchanged: master key still enabled, no signer list.

- We did not pin down why (the inner transactions' flags, signers or ordering) and moved to separate transactions, since setup is safe to retry.
- **Lesson worth a prominent note in the Batch docs:** the outer `tesSUCCESS` says nothing about the inner transactions. Check each inner transaction's own result (and the mode: all-or-nothing, only-one, until-failure, independent) before trusting a Batch.

### Closed-ended vaults (`LendingProtocolV1_1`)

Our first `LoanBrokerSet` failed with `tecNO_PERMISSION`.
`LendingProtocolV1_1` now requires a **closed-ended** vault (`VaultKind: 1`, with `SubscriptionDate` and `RedemptionDate`) before a loan broker can use it. The rules only became clear from `VaultCreate.cpp` and `LoanSet.cpp`:

- `temMALFORMED` when a closed-ended vault lacks either date, an open-ended one has them, or the dates are less than 180 seconds or more than 30 years apart;
- `tecTOO_SOON` for a loan during the subscription window, and `tecEXPIRED` for a deposit after it;
- a loan is refused unless it ends (start + `PaymentTotal × PaymentInterval` + 60 s) before `RedemptionDate`.

- **Cost:** a failed setup, then a redesign. The vault's liquidity is fixed once loans start, so it can't be topped up, and a vault stops taking 12-month loans a year before it ends. For a credit card that converts debt continuously, that means a series of vaults (README, "Rolling over to a new vault"), which the demo leaves out of scope.
- **What would have helped:** `tecNO_PERMISSION` names nothing; an error that says "vault is not closed-ended" would have saved an hour. A page on the vault lifecycle, with a table of which transaction is allowed in which phase, would be the best possible doc here. We wrote one for our README.

### A Single Asset Vault is not a vault in the EVM sense

On EVM chains, "vault" means ERC-4626: a standard interface (`deposit`, `withdraw`, `convertToShares`, `totalAssets`) in front of a contract that can do anything with the assets. It can lend them on Aave, provide liquidity, run a strategy, or route them to another vault. Shares are an ERC-20 that other contracts can hold, price and use as collateral. The vault is a building block, and its yield comes from whatever its code does.

XRPL's Single Asset Vault (XLS-65) shares the name and the share math, but not the role:

- **No strategy.** XRPL has no contracts, so a vault can't put its assets to work. They sit in the vault's pseudo-account. The only way they leave, other than a depositor withdrawing (or the issuer clawing back), is as a loan drawn through an XLS-66 loan broker.
- **So its only yield is the lending protocol's.** The share price moves when loan interest comes back and when losses are recognised, and for no other reason. A vault is, in effect, the funding side of XLS-66, not a general-purpose building block.
- **Since `LendingProtocolV1_1`, the two kinds have split.** A loan broker only accepts a closed-ended vault, and a closed-ended vault takes deposits only before its subscription date. An open-ended vault still takes deposits and withdrawals at any time, but no loan broker can use it. That leaves it with nothing to earn: it is custody with a share token.
- **No composability.** Nothing on chain can call `VaultDeposit` on someone's behalf, react to a deposit, or chain vaults together; every step is a transaction signed by an account. Shares are an MPT, so holding them brings MPT's wallet gaps (item 6 above): a Ledger can't hold them today.

**What this meant for us.** The card's lending pool is a vault in every sense that matters. LPs deposit RLUSD and receive shares whose price grows with the interest cardholders pay, ERC-4626 style. But a Single Asset Vault could not be that pool, because its money can only go out as XLS-66 loans with fixed terms and schedules. Card debt is drawn per swipe, repaid at any time, and accrues by the minute. So the pool lives in the TEE, with its own share math and its own trust-line token (XCLP). The XRPL vault is used for exactly one thing, the XLS-66 loans card debt converts into.

**The hoops to use one on its own.** To feed a vault from anything other than ordinary loans, you would set up a loan broker and its first-loss cover, wrap your strategy as a borrower account, and turn its returns into `LoanSet` and `LoanPay` on a fixed schedule. On top of that, a closed-ended vault means every new round of deposits needs a new vault. That is a lot of machinery to get what a few lines of an ERC-4626 strategy give you.

**What would help:** say plainly in the docs that a Single Asset Vault is the lending protocol's funding vehicle and not a general vault. Then either give open-ended vaults a use again, or let a vault accept yield from somewhere other than a loan broker, such as an explicit "add to assets" transaction, so it could stand on its own.

### Counterparty signing uses its own prefix

A broker's `CounterpartySignature` on `LoanSet` is hashed with the `CPT\0` prefix (`CounterpartyTxSign`), not the usual transaction prefix (see the xrpl.js item below).
That is a reasonable protocol choice (a counterparty signature can't be replayed as a transaction signature), but we only found it in rippled's source.

### Small things

- **`delivered_amount`, not `Amount`.** Crediting a payment needs the amount actually delivered, because partial payments exist. That is well documented, and we followed it everywhere; it is still the first trap for anyone crediting deposits.
- **Rippling flags on a token issuer.** Our LP share token (XCLP) is issued by an account that also holds an RLUSD trust line. The issuer needs `DefaultRipple` so holders can transfer shares to each other, while its RLUSD line needs NoRipple so RLUSD can't route through it. Correct, but easy to get wrong, and a short "issuing a token from an account that holds other tokens" recipe would help.
- **IOU precision.** Trust-line amounts carry 15 significant digits. Fine at demo sizes, but large share balances can lose the last digits of share math, which MPT integers wouldn't.
- **Finding out what a network supports.** Lending is enabled on Devnet but not on Testnet, which we found from the `feature` method. Amendment names are not always what you'd guess: Batch shows up as `BatchV1_1`. A per-network table of enabled amendments on xrpl.org would save a round of RPC calls.

## Devnet

- **One public endpoint.** Devnet is `s.devnet.rippletest.net`, over WebSocket (`:51233`) and JSON-RPC (`:51234`), and that's it: no second provider and no alternative cluster. When it misbehaves, there is nowhere else to point. Testnet and Mainnet have several providers; Devnet, where the new amendments live, needs at least a second one.
- **Connections drop.** Our watcher's WebSocket closed from time to time ("websocket was closed"). xrpl.js reconnects, but see below about subscriptions.
- **Load from one IP.** At one point Xaman stopped being able to sign. We first suspected throttling of our IP, because the demo opened a new connection per page refresh. It turned out not to be that (it failed over mobile data too, and Devnet was healthy), but we couldn't tell from outside. We moved everything to one connection per process anyway. Published rate limits for the public Devnet node would remove the guessing.
- **Resets.** Devnet is wiped from time to time, which removes every account, vault and loan. Our playbook has a section on recovering. A published reset schedule, or a notice in `server_info`, would help anyone running a demo on Devnet.
- **Faucet.** The Devnet faucet (`client.fundWallet`, and the xrpl.org faucet page) worked every time we asked. Demo wallets ran out of XRP after many rehearsals, which is expected; a top-up helper (`npm run demo-user -- faucet`) fixed that.

## xrpl.js (4.7.0)

- **`signLoanSetByCounterparty` signs with the wrong prefix.** It produces a signature rippled on Devnet rejects. xrpl.js's own local check already reports "Counterparty: Invalid signature", and the follow-up Payment then expired (`terPRE_SEQ` past `LastLedgerSequence`). rippled hashes the counterparty signature with `CPT\0`. Our fix computes it ourselves: `encodeForSigning`, swap the first four bytes for `43505400`, sign with ripple-keypairs (`watcher/src/lending.js`, `counterpartySignature`). This one deserves an issue upstream; a library that helps sign a new transaction type is only useful if the signature verifies.
- **Reconnect drops subscriptions.** After a dropped WebSocket, xrpl.js reconnects on its own but does not restore `subscribe`. Our watcher silently stopped seeing deposits and repayments until we noticed. We now catch up with `account_tx` and subscribe again on every `connected` event, and rescan every 10 seconds as a safety net. Either restoring subscriptions or documenting loudly that it doesn't would help.
- **API version 2 by default.** xrpl.js requests use API v2, where `account_tx` rows carry `tx_json` and `hash`. A plain JSON-RPC call defaults to v1, where they carry `tx`. Code that mixes both, as ours did, has to handle both shapes or pass `api_version` explicitly.
- **`autofill` gives 20 ledgers.** That is about a minute, which is too short for a hardware wallet, where the user reads the transaction on a small screen. We raise `LastLedgerSequence` for Ledger signing. A documented option for it would be nicer.
- **Helpful warning.** On a multisigned `LoanSet`, autofill notes that the fee accounts for the counterparty's signers. That is exactly the kind of hint that saves time.

## Xaman

- **Open question:** whether Xaman signs a Batch that already holds another account's `BatchSigner`. That matters once lending can go in a Batch.

## Ledger (WebHID)

- **The libraries expect Node's `Buffer`.** `hw-app-xrp` and the transports use a global `Buffer` without importing it, so a browser app needs a polyfill set before they load. A note in the README, or importing `buffer` explicitly, would save every web developer the same debugging.
- **The example in `hw-app-xrp` mentions the U2F transport**, which browsers have dropped; WebHID (Chrome, Edge, Brave) is what works today.
- **What it signs is clear.** `signTransaction(path, encode(tx))`, with `SigningPubKey` set to the device's key, returns the DER signature for `TxnSignature`. That is all documented. We built the transaction on a server (the browser has no XRPL connection), had the device sign those exact bytes, and submitted from the server; a tampered transaction fails the signature check.
- **Not yet tested:** how the XRP app shows a 40-hex currency code such as our XCLP. It may show hex rather than letters.

## Docs and specs in general

- **The source was the spec.** For Batch, vaults and lending, the deciding rules came from rippled's `develop` branch (`Batch.cpp`, `VaultCreate.cpp`, `LoanSet.cpp`). The XLS specs and xrpl.org were behind the amendments enabled on Devnet. That is understandable for features in flight, but a "what changed in V1_1 / V1_2" page per amendment would let builders follow along without reading C++.
- **Source files move.** Links into rippled's source broke while we used them (`Batch.cpp` moved between directories). Docs that link to source should link to a tag or commit.
- **Error codes are shared and generic.** `tecNO_PERMISSION`, `temMALFORMED` and `tecEXPIRED` each mean several different things in the new transactors. Short reason strings in the result metadata, even only on test networks, would make the new amendments much easier to adopt.


## What is good

Plenty worked well, and some of it shaped the design:

- **Destination tags.** One deposit account and one pool, with a tag per user, gave us per-user accounting with no per-user accounts: collateral deposits, debt repayments and LP deposits are all just tagged payments. It is the simplest possible integration point between a ledger and an off-chain system.
- **Multisig and a disabled master key.** A repayment account that is a 1-of-2 of the user and the Credit Server, with its own key disabled, took four transactions and no smart contract. -- This also just worked when implementing the configuration -- the agent directly did the whole process without having to remind it about disabling stuff etc. It was a very clean implementation.
- **New features on Devnet early.** Lending, vaults, Batch and MPTs were all there to build against before Testnet had them.
- **Speed and cost.** Ledgers close every few seconds, and fees are fractions of a cent, so every step of the demo happens live on stage.
- **Trust lines just work.** Every wallet and explorer handles them. RLUSD on mainnet is a trust-line token too, so the demo's mock RLUSD behaves the same way.
- **xrpl.js covers the new transaction types.** It has types for `LoanSet`, `LoanPay` and the vault transactions, and `autofill`, `submitAndWait`, `fundWallet`, `encode` and `encodeForSigning` made the rest straightforward.
- **The explorer.** devnet.xrpl.org showed every transaction we sent, with its result and metadata, which made debugging much faster.
- **rippled's source is readable.** When docs lagged, the transactors were clear enough to answer our questions directly.

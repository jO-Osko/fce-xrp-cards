/**
 * One-time XLS-66 lending setup for loan conversion, on the network in
 * ../wallets.json. Adds to that file:
 *   broker        mock loan broker (stands in for an outside party): owns the
 *                 RLUSD vault and the LoanBroker; the watcher's mock broker
 *                 countersigns LoanSets with it
 *   creditServer  Credit Server cosigner on each user's repayment account
 *                 (held by the watcher for the demo; TODO: a TEE wallet key)
 *   lending       vault id, loan broker id and the standard loan terms
 *
 * Usage: node src/setup-lending.js [--force]   then: node ../scripts/sync-config.mjs
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import xrpl from "xrpl";
import "./source-tag.js";

// LendingProtocolV1_1 needs a closed-ended vault: deposits only before
// SubscriptionDate, loans only between it and RedemptionDate (and must finish
// before it). So all liquidity goes in now; the vault cannot be topped up later.
const VAULT_LIQUIDITY = "500000";
const FIRST_LOSS_COVER = "25000";
const SUBSCRIPTION_WINDOW = 5 * 60; // seconds until loans can start
const VAULT_LIFETIME = 5 * 365 * 24 * 3600; // RedemptionDate: no loan may end after it
const VAULT_KIND_CLOSED_ENDED = 1;

// Standard terms the mock broker approves. InterestRate is in 1/10 basis points.
const TERMS = {
  interestRate: 10000, // 10% a year
  paymentTotal: 12,
  paymentInterval: 30 * 24 * 3600, // monthly, in seconds
  gracePeriod: 7 * 24 * 3600,
};

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const FILE = process.env.WALLETS_FILE || path.join(__dirname, "..", "wallets.json");
const w = JSON.parse(fs.readFileSync(FILE, "utf8"));
// Also accept `npm run setup-lending --force`, which npm turns into npm_config_force.
const force = process.argv.includes("--force") || process.env.npm_config_force === "true";
if (w.lending && !force) {
  console.error(`${FILE} already has lending setup; pass --force to redo it`);
  process.exit(1);
}

const RLUSD = { currency: w.rlusd.currency, issuer: w.rlusd.issuer };

async function submit(client, wallet, tx, what) {
  const res = await client.submitAndWait({ ...tx, Account: wallet.address }, { wallet });
  const code = res.result.meta?.TransactionResult;
  if (code !== "tesSUCCESS") throw new Error(`${what}: ${code}`);
  console.log(`${what}: ${res.result.hash}`);
  return res.result.meta;
}

function createdId(meta, type) {
  const node = meta.AffectedNodes.find((n) => n.CreatedNode?.LedgerEntryType === type);
  if (!node) throw new Error(`no ${type} created`);
  return node.CreatedNode.LedgerIndex;
}

const client = new xrpl.Client(w.network.ws);
await client.connect();

const { wallet: broker } = await client.fundWallet();
const { wallet: creditServer } = await client.fundWallet();
console.log(`broker: ${broker.address}\ncreditServer: ${creditServer.address}`);

await submit(client, broker, {
  TransactionType: "TrustSet",
  LimitAmount: { ...RLUSD, value: "1000000000" },
  Flags: xrpl.TrustSetFlags.tfSetNoRipple,
}, "broker trustline");
const issuer = xrpl.Wallet.fromSeed(w.issuer.seed);
const needed = String(Number(VAULT_LIQUIDITY) + Number(FIRST_LOSS_COVER));
await submit(client, issuer, {
  TransactionType: "Payment",
  Destination: broker.address,
  Amount: { ...RLUSD, value: needed },
}, `mint ${needed} RLUSD to broker`);

const now = xrpl.unixTimeToRippleTime(Date.now());
const subscriptionDate = now + SUBSCRIPTION_WINDOW;
const redemptionDate = subscriptionDate + VAULT_LIFETIME;
const vaultMeta = await submit(client, broker, {
  TransactionType: "VaultCreate",
  Asset: RLUSD,
  WithdrawalPolicy: xrpl.VaultWithdrawalPolicy.vaultStrategyFirstComeFirstServe,
  VaultKind: VAULT_KIND_CLOSED_ENDED,
  SubscriptionDate: subscriptionDate,
  RedemptionDate: redemptionDate,
}, "VaultCreate (closed-ended)");
const vaultId = createdId(vaultMeta, "Vault");

await submit(client, broker, {
  TransactionType: "VaultDeposit",
  VaultID: vaultId,
  Amount: { ...RLUSD, value: VAULT_LIQUIDITY },
}, `VaultDeposit ${VAULT_LIQUIDITY}`);

const brokerMeta = await submit(client, broker, {
  TransactionType: "LoanBrokerSet",
  VaultID: vaultId,
}, "LoanBrokerSet");
const loanBrokerId = createdId(brokerMeta, "LoanBroker");

await submit(client, broker, {
  TransactionType: "LoanBrokerCoverDeposit",
  LoanBrokerID: loanBrokerId,
  Amount: { ...RLUSD, value: FIRST_LOSS_COVER },
}, `LoanBrokerCoverDeposit ${FIRST_LOSS_COVER}`);

await client.disconnect();

w.broker = { label: "broker", address: broker.address, seed: broker.seed };
w.creditServer = { label: "creditServer", address: creditServer.address, seed: creditServer.seed };
w.lending = {
  vaultId,
  loanBrokerId,
  terms: TERMS,
  // Ripple-epoch seconds: loans start after the first, must end before the second.
  subscriptionDate,
  redemptionDate,
};
fs.writeFileSync(FILE, JSON.stringify(w, null, 2), { mode: 0o600 });
const opens = new Date(xrpl.rippleTimeToUnixTime(subscriptionDate)).toISOString();
console.log(`vault ${vaultId} (loans open at ${opens})\nloanBroker ${loanBrokerId}\nwrote ${FILE}; now run: node ../scripts/sync-config.mjs`);

/**
 * One-time setup of the TEE lending pool's XRPL side, on the network in
 * ../wallets.json. Adds to that file:
 *   treasury   receives TEE profit (card fees, interest reserve) at each settlement
 *   lpDeposit  LPs send RLUSD here with their tag; it issues the LP share token and
 *              takes shares back for redemption; the watcher sweeps its RLUSD to the pool
 *   operator   the operator's own LP position ("internal" liquidity)
 *   vault      the share token's currency code (issuer: lpDeposit)
 *
 * The share token is a trust-line token (issued currency), not an MPT: wallets
 * such as Xaman and explorers support trust lines everywhere (README, "The TEE
 * lending pool and liquidity providers"). Holders opt in with a TrustSet.
 *
 * The operator and the demo user are opted in to the share token. Add liquidity
 * afterwards with an operator deposit (watcher POST /operator/deposit, or the
 * button on the Operator app's Settlement page), once the TEE and watcher are running.
 *
 * Usage: node src/setup-vault.js [--force]   then: node ../scripts/sync-config.mjs
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import xrpl from "xrpl";
import "./source-tag.js";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const FILE = process.env.WALLETS_FILE || path.join(__dirname, "..", "wallets.json");
const w = JSON.parse(fs.readFileSync(FILE, "utf8"));
// Also accept `npm run setup-vault --force`, which npm turns into npm_config_force.
const force = process.argv.includes("--force") || process.env.npm_config_force === "true";
if (w.vault && !force) {
  console.error(`${FILE} already has the LP vault set up; pass --force to redo it`);
  process.exit(1);
}

const RLUSD = { currency: w.rlusd.currency, issuer: w.rlusd.issuer };
// "XCLP" is longer than three letters, so XRPL needs it as a 40-hex currency code.
const SHARE_CODE = "XCLP";
const SHARE_CURRENCY = xrpl.convertStringToHex(SHARE_CODE).padEnd(40, "0");
// Shares never need more than this; the TEE tracks the real supply.
const SHARE_LIMIT = "1000000000000";

async function submit(client, wallet, tx, what) {
  const res = await client.submitAndWait({ ...tx, Account: wallet.address }, { wallet });
  const code = res.result.meta?.TransactionResult;
  if (code !== "tesSUCCESS") throw new Error(`${what}: ${code}`);
  console.log(`${what}: ${res.result.hash}`);
  return res.result.meta;
}

const client = new xrpl.Client(w.network.ws);
await client.connect();

const fund = async (label) => {
  const { wallet } = await client.fundWallet();
  console.log(`${label}: ${wallet.address}`);
  return wallet;
};
const treasury = await fund("treasury");
const lpDeposit = await fund("lpDeposit");
const operator = await fund("operator");

// The issuer ripples its own token, so holders can transfer shares to each other.
// Its RLUSD trust line below is NoRipple, so RLUSD never routes through it.
await submit(client, lpDeposit, {
  TransactionType: "AccountSet",
  SetFlag: xrpl.AccountSetAsfFlags.asfDefaultRipple,
}, "lpDeposit DefaultRipple (share token issuer)");

for (const [label, wallet] of [["treasury", treasury], ["lpDeposit", lpDeposit], ["operator", operator]]) {
  await submit(client, wallet, {
    TransactionType: "TrustSet",
    LimitAmount: { ...RLUSD, value: "1000000000" },
    Flags: xrpl.TrustSetFlags.tfSetNoRipple,
  }, `${label} RLUSD trust line`);
}

// Holders opt in to the share token with a trust line to its issuer.
const shareLine = { currency: SHARE_CURRENCY, issuer: lpDeposit.address, value: SHARE_LIMIT };
await submit(client, operator, { TransactionType: "TrustSet", LimitAmount: shareLine }, "operator share trust line");
if (w.user?.seed) {
  await submit(client, xrpl.Wallet.fromSeed(w.user.seed), { TransactionType: "TrustSet", LimitAmount: shareLine }, "demo user share trust line");
}

await client.disconnect();

w.treasury = { label: "treasury", address: treasury.address, seed: treasury.seed };
w.lpDeposit = { label: "lpDeposit", address: lpDeposit.address, seed: lpDeposit.seed };
w.operator = { label: "operator", address: operator.address, seed: operator.seed };
w.vault = { shareCurrency: SHARE_CURRENCY, shareCode: SHARE_CODE };
fs.writeFileSync(FILE, JSON.stringify(w, null, 2), { mode: 0o600 });
console.log(`share token ${SHARE_CODE} (${SHARE_CURRENCY}) issued by ${lpDeposit.address}\nwrote ${FILE}; now run: node ../scripts/sync-config.mjs`);

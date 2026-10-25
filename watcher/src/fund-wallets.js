/**
 * Generate + fund the demo wallets and write ../wallets.json, the source of
 * truth for the XRPL network and accounts (scripts/sync-config.mjs spreads it).
 *
 * Creates a mock RLUSD issuer (DefaultRipple), deposit, pool, settlement and
 * user accounts; pool, settlement and user trust the issuer; the user gets
 * demo RLUSD. The pool starts empty: liquidity comes in through LP deposits
 * (setup-vault.js, then an operator deposit) so the TEE accounts for it.
 *
 * Usage: node src/fund-wallets.js [devnet|testnet] [--force]
 * Writes seeds — treat wallets.json as a secret. Refuses to overwrite it without --force.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import xrpl from "xrpl";
import "./source-tag.js";

const NETWORKS = {
  devnet: {
    name: "XRPL Devnet",
    ws: "wss://s.devnet.rippletest.net:51233",
    rpc: "https://s.devnet.rippletest.net:51234",
    explorer: "https://devnet.xrpl.org",
    xamanNetwork: "DEVNET",
  },
  testnet: {
    name: "XRPL Testnet",
    ws: "wss://s.altnet.rippletest.net:51233",
    rpc: "https://s.altnet.rippletest.net:51234",
    explorer: "https://testnet.xrpl.org",
    xamanNetwork: "TESTNET",
  },
};

const RLUSD_CURRENCY = "524C555344000000000000000000000000000000";
const USER_RLUSD = "1000";

const args = process.argv.slice(2);
const netKey = args.find((a) => !a.startsWith("--")) || "devnet";
const network = NETWORKS[netKey];
if (!network) {
  console.error(`unknown network ${netKey}; use ${Object.keys(NETWORKS).join(" or ")}`);
  process.exit(1);
}

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const OUT = process.env.WALLETS_FILE || path.join(__dirname, "..", "wallets.json");
// `npm run fund-wallets --force` hands --force to npm, which sets npm_config_force
// instead of passing it on; accept both that and `npm run fund-wallets -- --force`.
const force = args.includes("--force") || process.env.npm_config_force === "true";
if (fs.existsSync(OUT) && !force) {
  console.error(`${OUT} exists; move it away or pass --force`);
  process.exit(1);
}

async function fund(client, label) {
  const { wallet, balance } = await client.fundWallet();
  console.log(`${label}: ${wallet.address} balance=${balance}`);
  return { label, address: wallet.address, seed: wallet.seed };
}

async function submit(client, seed, tx, what) {
  const wallet = xrpl.Wallet.fromSeed(seed);
  const res = await client.submitAndWait({ ...tx, Account: wallet.address }, { wallet });
  const code = res.result.meta?.TransactionResult;
  if (code !== "tesSUCCESS") throw new Error(`${what}: ${code}`);
  console.log(`${what}: ${res.result.hash}`);
}

const client = new xrpl.Client(network.ws);
await client.connect();

const issuer = await fund(client, "issuer");
const deposit = await fund(client, "deposit");
const pool = await fund(client, "pool");
const settlement = await fund(client, "settlement");
const user = await fund(client, "user");

// Holders must be able to pay each other through the issuer.
await submit(client, issuer.seed, {
  TransactionType: "AccountSet",
  SetFlag: xrpl.AccountSetAsfFlags.asfDefaultRipple,
}, "issuer DefaultRipple");

for (const w of [pool, settlement, user]) {
  await submit(client, w.seed, {
    TransactionType: "TrustSet",
    LimitAmount: { currency: RLUSD_CURRENCY, issuer: issuer.address, value: "1000000000" },
    Flags: xrpl.TrustSetFlags.tfSetNoRipple,
  }, `trustline ${w.label}`);
}

for (const [w, value] of [[user, USER_RLUSD]]) {
  await submit(client, issuer.seed, {
    TransactionType: "Payment",
    Destination: w.address,
    Amount: { currency: RLUSD_CURRENCY, issuer: issuer.address, value },
  }, `mint ${value} RLUSD to ${w.label}`);
}

await client.disconnect();

const out = {
  network,
  rlusd: { currency: RLUSD_CURRENCY, issuer: issuer.address },
  issuer,
  deposit,
  pool,
  settlement,
  user,
  note: `Mock RLUSD issued by ${issuer.address} on ${network.name}. Run node scripts/sync-config.mjs next.`,
};

fs.writeFileSync(OUT, JSON.stringify(out, null, 2), { mode: 0o600 });
console.log(`wrote ${OUT}; now run: node ../scripts/sync-config.mjs`);

// New wallets make the old demo state meaningless; it is not removed automatically.
const STATE_FILES = [
  "processed.json", "settlements.json", "last-settle.json", "conversions.json",
  "repayment-accounts.json", "loan-payments.json", "broker-log.json", "lp-jobs.json", "settle-progress.json",
  "lp-allocations.json",
];
const stale = STATE_FILES.filter((f) => fs.existsSync(path.join(__dirname, "..", f)));
if (stale.length || fs.existsSync("/tmp/card-state.json")) {
  console.log(
    `\nOld demo state from the previous wallets is still there; clear it (playbook A2):\n` +
      stale.map((f) => `  watcher/${f}`).join("\n") +
      (fs.existsSync("/tmp/card-state.json") ? "\n  /tmp/card-state.json (mock TEE ledger)" : "") +
      "\nand run npm run setup-lending and npm run setup-vault again (they belong to the old wallets).",
  );
}

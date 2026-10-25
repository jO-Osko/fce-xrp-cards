/**
 * Plays a liquidity provider with the demo `user` wallet from ../wallets.json,
 * for walking through the Liquidity tab without Xaman. Never prints the seed.
 *
 * Usage (from watcher/, services running):
 *   npm run demo-lp -- session              reserve an LP tag, print the browser session snippet
 *   npm run demo-lp -- optin                trust line to the share token (setup-vault already did this)
 *   npm run demo-lp -- deposit <rlusd>      send RLUSD to the LP deposit account with the LP tag
 *   npm run demo-lp -- redeem <shares>      send shares back (e.g. 100 = 100 displayed shares)
 *   npm run demo-lp -- position             shares held and what the TEE recorded
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import xrpl from "xrpl";
import { RLUSD_CURRENCY, RLUSD_ISSUER, WS } from "./network.js";
import { card } from "./tee.js";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const W = JSON.parse(fs.readFileSync(process.env.WALLETS_FILE || path.join(__dirname, "..", "wallets.json"), "utf8"));
const WATCHER = `http://127.0.0.1:${process.env.WATCHER_HTTP_PORT || 8090}`;
const SHARE_CURRENCY = process.env.SHARE_CURRENCY || W.vault?.shareCurrency;
const lp = xrpl.Wallet.fromSeed(W.user.seed);

async function submit(tx) {
  const client = new xrpl.Client(WS);
  await client.connect();
  try {
    const res = await client.submitAndWait({ ...tx, Account: lp.address }, { wallet: lp });
    const code = res.result.meta?.TransactionResult;
    if (code !== "tesSUCCESS") throw new Error(`${tx.TransactionType}: ${code}`);
    return res.result.hash;
  } finally {
    await client.disconnect();
  }
}

const [cmd, arg] = process.argv.slice(2);
if (!SHARE_CURRENCY) throw new Error("no share token — run npm run setup-vault and sync-config");
// The share token's issuer is the LP deposit account.
const shareIssuer = () => card("LP_REGISTER", { xrplAddress: lp.address }).then((r) => r.depositAccount);

if (cmd === "session") {
  const reg = await card("LP_REGISTER", { xrplAddress: lp.address });
  console.log(`LP ${reg.xrplAddress} has deposit tag ${reg.tag} at ${reg.depositAccount}`);
  console.log("Paste into the browser console on the web app, then reload the Liquidity page:\n");
  console.log(`localStorage.setItem("xrpl-cc-lp-session", ${JSON.stringify(JSON.stringify({ xrplAddress: lp.address }))});`);
} else if (cmd === "optin") {
  const issuer = await shareIssuer();
  const hash = await submit({ TransactionType: "TrustSet", LimitAmount: { currency: SHARE_CURRENCY, issuer, value: "1000000000000" } });
  console.log(`share trust line set: ${hash}`);
} else if (cmd === "deposit") {
  const value = arg || "100";
  const reg = await card("LP_REGISTER", { xrplAddress: lp.address });
  const hash = await submit({
    TransactionType: "Payment",
    Destination: reg.depositAccount,
    DestinationTag: reg.tag,
    Amount: { currency: RLUSD_CURRENCY, issuer: RLUSD_ISSUER, value },
  });
  console.log(`deposited ${value} RLUSD with LP tag ${reg.tag}: ${hash}`);
  console.log("the watcher credits it, issues the shares and sweeps the RLUSD into the pool");
} else if (cmd === "redeem") {
  const displayed = arg || "10";
  const issuer = await shareIssuer();
  // Sending the token back to its issuer burns it.
  const hash = await submit({
    TransactionType: "Payment",
    Destination: issuer,
    Amount: { currency: SHARE_CURRENCY, issuer, value: displayed },
  });
  console.log(`sent ${displayed} shares back: ${hash}; the watcher pays out the RLUSD`);
} else if (cmd === "position") {
  const pos = await (await fetch(`${WATCHER}/lp/position?address=${lp.address}`)).json();
  const vault = await card("GET_VAULT", { xrplAddress: lp.address });
  const shares = Number(pos.shares) / 1e6;
  console.log(`${lp.address}: ${shares} shares × ${vault.sharePrice} = ${(shares * Number(vault.sharePrice)).toFixed(6)} RLUSD`);
  console.log(`opted in: ${pos.authorized}; pending jobs: ${pos.jobs.filter((j) => j.status !== "done").length}`);
  console.log(`vault: ${vault.totalAssetsRlusd} RLUSD, free ${vault.freeLiquidityRlusd}, supply APR ${vault.supplyAprBps / 100}%`);
} else {
  console.error("usage: npm run demo-lp -- session | optin | deposit <rlusd> | redeem <shares> | position");
  process.exit(1);
}

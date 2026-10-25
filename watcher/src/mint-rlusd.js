/**
 * Mint demo RLUSD from the mock issuer (Devnet has no real RLUSD).
 *
 * Usage: node --env-file=.env src/mint-rlusd.js <address> <amount>
 *        (or npm run mint-rlusd -- <address> <amount>)
 * The destination needs a trust line to the issuer.
 */
import { pathToFileURL } from "node:url";
import xrpl from "xrpl";
import { ISSUER_SEED, RLUSD_CURRENCY, RLUSD_ISSUER } from "./network.js";
import { closeXrplClient, xrplClient } from "./xrpl-client.js";

/** Pays `amount` RLUSD from the issuer to `destination` (with an optional tag); returns the tx hash. */
export async function mintRlusd(destination, amount, destinationTag) {
  if (!ISSUER_SEED) throw new Error("XRPL_ISSUER_SEED not set — run sync-config");
  const n = Number(amount);
  if (!Number.isFinite(n) || n <= 0) throw new Error("amount must be positive");
  const wallet = xrpl.Wallet.fromSeed(ISSUER_SEED);
  if (wallet.address !== RLUSD_ISSUER) {
    throw new Error("XRPL_ISSUER_SEED does not match RLUSD_ISSUER");
  }
  const client = await xrplClient();
  const res = await client.submitAndWait(
    {
      TransactionType: "Payment",
      Account: wallet.address,
      Destination: destination,
      ...(destinationTag ? { DestinationTag: destinationTag } : {}),
      Amount: { currency: RLUSD_CURRENCY, issuer: RLUSD_ISSUER, value: String(amount) },
    },
    { wallet },
  );
  const code = res.result.meta?.TransactionResult;
  if (code !== "tesSUCCESS") throw new Error(`mint failed: ${code}`);
  return res.result.hash;
}

/**
 * What a faucet needs to know about `address`: whether it exists (holds XRP),
 * its XRP balance, and its RLUSD trust line and balance.
 */
export async function rlusdAccount(address) {
  const client = await xrplClient();
  let xrp;
  try {
    const info = await client.request({ command: "account_info", account: address, ledger_index: "validated" });
    xrp = xrpl.dropsToXrp(info.result.account_data.Balance);
  } catch (err) {
    if (err.data?.error === "actNotFound") return { funded: false, xrp: "0", trustLine: false, rlusd: "0" };
    throw err;
  }
  const res = await client.request({ command: "account_lines", account: address, peer: RLUSD_ISSUER, ledger_index: "validated" });
  const line = (res.result.lines || []).find((l) => l.currency === RLUSD_CURRENCY);
  return { funded: true, xrp: String(xrp), trustLine: !!line, rlusd: line ? line.balance : "0" };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const [destination, amount] = process.argv.slice(2);
  if (!destination || !amount) {
    console.error("usage: node src/mint-rlusd.js <address> <amount>");
    process.exit(1);
  }
  const hash = await mintRlusd(destination, amount);
  console.log(`minted ${amount} RLUSD to ${destination}: ${hash}`);
  await closeXrplClient();
}

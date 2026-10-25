/**
 * Liquidity providers, watcher side. The TEE decides; this executes on XRPL:
 *
 *   RLUSD + LP tag → LP deposit account     LP_DEPOSIT, then issue the share tokens to the LP
 *                                            and sweep the RLUSD into the main pool
 *   share tokens → LP deposit account       LP_REDEEM, then pay the RLUSD from the pool,
 *   (their issuer, so they are burned)      or return the shares if the TEE reverted
 *
 * Shares are a trust-line token (currency SHARE_CURRENCY, issuer: the LP deposit
 * account) for wallet compatibility. The TEE counts integer share units (1e6 per
 * share); on XRPL they are decimal amounts, converted here.
 *
 * XRPL actions are queued in lp-jobs.json and retried, so a share issue that
 * waits for the holder's trust line to the share token goes through once it exists.
 *
 * TODO(TEE): the LP deposit account (share issuer) and pool keys are held here,
 * like the other watcher keys; they belong in the TEE.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import xrpl from "xrpl";
import { mintRlusd } from "./mint-rlusd.js";
import { RLUSD_CURRENCY, RLUSD_ISSUER } from "./network.js";
import { xrplClient } from "./xrpl-client.js";
import { card } from "./tee.js";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const JOBS_FILE = process.env.WATCHER_LP_JOBS_FILE || path.join(__dirname, "..", "lp-jobs.json");
export const LP_DEPOSIT = process.env.XRPL_LP_DEPOSIT_ADDRESS || "";
const LP_DEPOSIT_SEED = process.env.XRPL_LP_DEPOSIT_SEED || "";
const POOL_SEED = process.env.XRPL_POOL_SEED || "";
const SHARE_CURRENCY = process.env.SHARE_CURRENCY || "";
const OPERATOR = process.env.XRPL_OPERATOR_ADDRESS || "";

export const lpConfigured = () => !!(LP_DEPOSIT && LP_DEPOSIT_SEED && SHARE_CURRENCY);

const rlusd = (value) => ({ currency: RLUSD_CURRENCY, issuer: RLUSD_ISSUER, value: String(value) });
const UNITS_PER_SHARE = 1_000_000n;

/** TEE share units → the token's decimal amount ("150000000" → "150"). */
export function unitsToValue(units) {
  const n = BigInt(units);
  const frac = (n % UNITS_PER_SHARE).toString().padStart(6, "0").replace(/0+$/, "");
  return `${n / UNITS_PER_SHARE}${frac ? `.${frac}` : ""}`;
}

/** A decimal amount from XRPL ("150", "0.5", "1e-6") → TEE share units, rounded down. */
export function valueToUnits(value) {
  const m = /^(\d*)(?:\.(\d*))?(?:e([+-]?\d+))?$/i.exec(String(value).trim());
  if (!m) throw new Error(`bad share amount ${value}`);
  const [, int = "", frac = "", exp = "0"] = m;
  const shift = Number(exp) + 6 - frac.length;
  const digits = BigInt(`${int}${frac}` || "0");
  return (shift >= 0 ? digits * 10n ** BigInt(shift) : digits / 10n ** BigInt(-shift)).toString();
}

const shares = (units) => ({ currency: SHARE_CURRENCY, issuer: LP_DEPOSIT, value: unitsToValue(units) });

function loadJobs() {
  try {
    return JSON.parse(fs.readFileSync(JOBS_FILE, "utf8"));
  } catch {
    return [];
  }
}

function saveJobs(jobs) {
  // Keep every pending job; only old finished ones are pruned.
  const pending = jobs.filter((j) => j.status !== "done");
  const done = jobs.filter((j) => j.status === "done").slice(0, 500);
  const keep = new Set([...pending, ...done]);
  fs.writeFileSync(JOBS_FILE, JSON.stringify(jobs.filter((j) => keep.has(j)), null, 2));
}

function addJob(job) {
  const jobs = loadJobs();
  if (jobs.some((j) => j.id === job.id)) return;
  jobs.unshift({ ...job, status: "pending", createdAt: Math.floor(Date.now() / 1000) });
  saveJobs(jobs);
}

/** Jobs for one address, newest first (for the Liquidity page). */
export function jobsFor(address) {
  return loadJobs().filter((j) => j.to === address);
}

// The TEE could not be reached (as opposed to answering with an error).
export const unreachable = (err) => err instanceof TypeError || /fetch failed|ECONNREFUSED|timed out/i.test(err.message);

/**
 * Handles a validated transaction to the LP deposit account. Returns true once
 * it is decided for good (credited, redeemed, refused or ignored), so the
 * watcher records it in processed.json and never handles it again; false when
 * the TEE was unreachable, so the next rescan retries it.
 */
export async function onLPPayment(tx, meta) {
  const delivered = meta?.delivered_amount ?? meta?.DeliveredAmount;
  if (typeof delivered !== "object" || delivered === null) return true;
  // Already queued (e.g. before processed.json was written): never act twice.
  if (loadJobs().some((j) => j.sourceTx === tx.hash)) return true;

  // Shares coming back to their issuer are burned: a redemption.
  if (delivered.currency === SHARE_CURRENCY && delivered.issuer === LP_DEPOSIT) {
    const units = valueToUnits(delivered.value);
    if (units === "0") {
      console.log(`LP redeem ${tx.hash}: ${delivered.value} shares is below one share unit (1e-6); burned without payout`);
      return true;
    }
    try {
      // A duplicate answer carries the original payout: the first answer was lost
      // (timeout, crash) before the job was queued, so queue it now.
      const out = await card("LP_REDEEM", { xrplAddress: tx.Account, shares: units, txHash: tx.hash });
      if (out.duplicate && !(Number(out.rlusdAmount) > 0)) return true;
      console.log(`LP redeem ${delivered.value} shares from ${tx.Account} → ${out.rlusdAmount} RLUSD`);
      addJob({ id: `payout-${tx.hash}`, kind: "payout", to: tx.Account, amount: out.rlusdAmount, sourceTx: tx.hash });
    } catch (err) {
      if (unreachable(err)) {
        console.error(`LP_REDEEM ${tx.hash}: TEE unreachable (${err.message}); retrying on the next rescan`);
        return false;
      }
      // Demo: the TEE refused (e.g. not enough free liquidity), so the shares go back.
      console.error(`LP_REDEEM ${tx.hash} reverted: ${err.message}; returning the shares`);
      addJob({ id: `return-${tx.hash}`, kind: "return", to: tx.Account, shares: units, sourceTx: tx.hash, reason: err.message });
    }
    await runJobs();
    return true;
  }

  if (delivered.currency !== RLUSD_CURRENCY || delivered.issuer !== RLUSD_ISSUER) return true;
  if (typeof tx.DestinationTag !== "number") {
    console.log(`untagged RLUSD to the LP deposit account ignored (${tx.hash}); it is swept into the pool unaccounted`);
    return true;
  }
  try {
    // As above: a duplicate carries the original shares, which may never have been issued.
    const out = await card("LP_DEPOSIT", { tag: tx.DestinationTag, rlusdAmount: delivered.value, txHash: tx.hash });
    if (out.duplicate && !(Number(out.shares) > 0)) return true;
    console.log(`LP deposit tag=${tx.DestinationTag} ${delivered.value} RLUSD → ${out.shares} shares for ${out.xrplAddress}`);
    addJob({ id: `issue-${tx.hash}`, kind: "issue", to: out.xrplAddress, shares: out.shares, sourceTx: tx.hash });
  } catch (err) {
    if (unreachable(err)) {
      console.error(`LP_DEPOSIT ${tx.hash}: TEE unreachable (${err.message}); retrying on the next rescan`);
      return false;
    }
    // e.g. an unknown LP tag: the RLUSD stays outside the pool's accounting.
    console.error(`LP_DEPOSIT ${tx.hash} refused: ${err.message}`);
  }
  await runJobs();
  return true;
}

async function submit(client, wallet, tx) {
  const res = await client.submitAndWait({ ...tx, Account: wallet.address }, { wallet });
  const code = res.result.meta?.TransactionResult;
  if (code !== "tesSUCCESS") throw new Error(code);
  return res.result.hash;
}

let running = null;

/** Runs pending jobs, then sweeps the LP deposit account. One run at a time. */
export function runJobs() {
  if (!lpConfigured()) return Promise.resolve();
  running = running ?? doRun().finally(() => {
    running = null;
  });
  return running;
}

async function doRun() {
  const issuer = xrpl.Wallet.fromSeed(LP_DEPOSIT_SEED);
  const pool = POOL_SEED ? xrpl.Wallet.fromSeed(POOL_SEED) : null;
  const client = await xrplClient();
  for (const job of loadJobs().filter((j) => j.status !== "done")) {
    try {
      let hash;
      if (job.kind === "issue" || job.kind === "return") {
        hash = await submit(client, issuer, { TransactionType: "Payment", Destination: job.to, Amount: shares(job.shares) });
      } else if (job.kind === "payout") {
        if (!pool) throw new Error("XRPL_POOL_SEED not set");
        hash = await submit(client, pool, { TransactionType: "Payment", Destination: job.to, Amount: rlusd(job.amount) });
      }
      updateJob(job.id, { status: "done", txHash: hash, error: undefined });
      console.log(`LP ${job.kind} → ${job.to}: ${hash}`);
    } catch (err) {
      // tecPATH_DRY / tecNO_LINE: the holder has no trust line to the share token yet.
      updateJob(job.id, { status: "pending", error: err.message, triedAt: Math.floor(Date.now() / 1000) });
    }
  }
  await sweep(client, issuer, pool);
}

function updateJob(id, patch) {
  saveJobs(loadJobs().map((j) => (j.id === id ? { ...j, ...patch } : j)));
}

/** Moves every RLUSD the LP deposit account holds into the main pool. */
async function sweep(client, issuer, pool) {
  if (!pool) return;
  const lines = await client.request({ command: "account_lines", account: issuer.address, peer: RLUSD_ISSUER });
  const line = (lines.result.lines || []).find((l) => l.currency === RLUSD_CURRENCY);
  const balance = Number(line?.balance || 0);
  if (!(balance > 0)) return;
  const hash = await submit(client, issuer, { TransactionType: "Payment", Destination: pool.address, Amount: rlusd(line.balance) });
  console.log(`swept ${line.balance} RLUSD from the LP deposit account into the pool: ${hash}`);
}

/**
 * The holder's share balance (in TEE share units) and whether they have a
 * trust line to the share token, read from XRPL.
 */
export async function sharePosition(address) {
  const client = await xrplClient();
  try {
    const res = await client.request({ command: "account_lines", account: address, peer: LP_DEPOSIT });
    const line = (res.result.lines || []).find((l) => l.currency === SHARE_CURRENCY);
    if (!line) return { authorized: false, shares: "0" };
    return { authorized: true, shares: valueToUnits(line.balance) };
  } catch (err) {
    if (err.data?.error === "actNotFound") return { authorized: false, shares: "0" };
    throw err;
  }
}

/**
 * Demo: the operator adds liquidity. The mock issuer mints RLUSD straight to
 * the LP deposit account under the operator's LP tag, so it goes through the
 * normal deposit path and the operator gets shares for it.
 */
export async function operatorDeposit(amount) {
  if (!OPERATOR) throw new Error("XRPL_OPERATOR_ADDRESS not set — run setup-vault and sync-config");
  if (!lpConfigured()) throw new Error("LP vault not configured — run setup-vault and sync-config");
  const reg = await card("LP_REGISTER", { xrplAddress: OPERATOR });
  const txHash = await mintRlusd(LP_DEPOSIT, amount, reg.tag);
  return { tag: reg.tag, txHash };
}

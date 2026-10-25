/**
 * Daily settlement: TEE SETTLE_DAILY → XRPL RLUSD Payments to the treasury and the settlement address → SETTLE_CONFIRM.
 * TEE_MODE=mock → POST /card. TEE_MODE=fcc → on-chain InstructionSender + threshold poll.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import xrpl from "xrpl";
import { settleInstruction } from "./flare-instruction.js";
import { card } from "./tee.js";
import { EXPLORER, RLUSD_CURRENCY, RLUSD_ISSUER } from "./network.js";
import { accountTx, closeXrplClient, xrplClient } from "./xrpl-client.js";

const TEE_MODE = (process.env.TEE_MODE || "mock").toLowerCase();

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const LAST_SETTLE_FILE =
  process.env.WATCHER_LAST_SETTLE_FILE ||
  path.join(__dirname, "..", "last-settle.json");
const SETTLEMENTS_FILE =
  process.env.WATCHER_SETTLEMENTS_FILE ||
  path.join(__dirname, "..", "settlements.json");
const POOL_SEED = process.env.XRPL_POOL_SEED;
const SETTLEMENT_ADDRESS = process.env.XRPL_SETTLEMENT_ADDRESS || "";
const RIPPLE_EPOCH = 946684800;

function normalizeSettle(row) {
  if (!row?.txHash) return null;
  return {
    rlusdAmount: String(row.rlusdAmount ?? ""),
    txHash: row.txHash,
    explorerUrl: row.explorerUrl || `${EXPLORER}/${row.txHash}`,
    settlementAddress: row.settlementAddress || SETTLEMENT_ADDRESS || "",
    treasuryRlusd: String(row.treasuryRlusd ?? ""),
    treasuryTxHash: row.treasuryTxHash || "",
    wireQueued: row.wireQueued === true,
    settleDailyActionId: row.settleDailyActionId || "",
    settleConfirmActionId: row.settleConfirmActionId || "",
    settleDailyFlareTx: row.settleDailyFlareTx || "",
    settleConfirmFlareTx: row.settleConfirmFlareTx || "",
    at: typeof row.at === "number" ? row.at : 0,
  };
}

/** SETTLE_DAILY / SETTLE_CONFIRM — mock POST /card or FCC on-chain instruction. */
async function settleOp(command, body = {}) {
  if (TEE_MODE === "fcc") {
    const { data, actionId, flareTxHash } = await settleInstruction(command, body);
    return { ...data, actionId, flareTxHash };
  }
  return card(command, body);
}

/** Settlements to the current settlement address only: records left by older wallets are not this ledger's. */
function current(rows) {
  return SETTLEMENT_ADDRESS ? rows.filter((r) => r.settlementAddress === SETTLEMENT_ADDRESS) : rows;
}

function readSettlementsFile() {
  try {
    const raw = JSON.parse(fs.readFileSync(SETTLEMENTS_FILE, "utf8"));
    if (Array.isArray(raw)) {
      return current(raw.map(normalizeSettle).filter(Boolean));
    }
  } catch {
    /* missing or invalid */
  }
  // Older watchers kept only the latest settlement, in last-settle.json.
  try {
    const last = normalizeSettle(JSON.parse(fs.readFileSync(LAST_SETTLE_FILE, "utf8")));
    if (last) return current([last]);
  } catch {
    /* none */
  }
  return [];
}

/** Prefer non-empty string fields so XRPL backfill does not wipe TEE / Flare proofs. */
function mergeSettle(prev, next) {
  const out = { ...prev, ...next, at: next.at || prev.at };
  for (const k of [
    "settleDailyActionId",
    "settleConfirmActionId",
    "settleDailyFlareTx",
    "settleConfirmFlareTx",
    "explorerUrl",
    "settlementAddress",
    "rlusdAmount",
    "treasuryRlusd",
    "treasuryTxHash",
  ]) {
    if (!next[k] && prev[k]) out[k] = prev[k];
  }
  if (prev.wireQueued && !next.wireQueued) out.wireQueued = true;
  return out;
}

function writeSettlements(rows) {
  const byHash = new Map();
  for (const row of rows) {
    const n = normalizeSettle(row);
    if (!n) continue;
    const prev = byHash.get(n.txHash);
    byHash.set(n.txHash, prev ? mergeSettle(prev, n) : n);
  }
  const list = [...byHash.values()].sort((a, b) => (b.at || 0) - (a.at || 0));
  fs.writeFileSync(SETTLEMENTS_FILE, JSON.stringify(list, null, 2));
  return list;
}

export function loadSettlements() {
  return readSettlementsFile();
}

function saveSettle(result) {
  const row = normalizeSettle({
    ...result,
    at: result.at || Math.floor(Date.now() / 1000),
  });
  const existing = readSettlementsFile();
  return writeSettlements([row, ...existing]);
}

/** Pool's RLUSD balance on the issuer trust line (null if query fails). */
export async function poolRlusdBalance(client, address) {
  try {
    const lines = await client.request({
      command: "account_lines",
      account: address,
      ledger_index: "validated",
    });
    const line = (lines.result.lines || []).find(
      (l) =>
        l.account === RLUSD_ISSUER &&
        (l.currency === RLUSD_CURRENCY || l.currency === "RLUSD"),
    );
    if (!line) return 0;
    const n = Number(line.balance);
    return Number.isFinite(n) ? n : null;
  } catch {
    return null;
  }
}

/** RLUSD payments from the pool to the settlement address (on-chain history). */
export async function backfillSettlementsFromXrpl() {
  if (!POOL_SEED || !SETTLEMENT_ADDRESS) return loadSettlements();
  let poolAddress;
  try {
    poolAddress = xrpl.Wallet.fromSeed(POOL_SEED).address;
  } catch {
    return loadSettlements();
  }

  try {
    const fromChain = [];
    for (const row of await accountTx(poolAddress, 50)) {
      if (row.validated === false) continue;
      const meta = row.meta || {};
      if (meta.TransactionResult && meta.TransactionResult !== "tesSUCCESS") continue;
      const tx = row.tx || {};
      if (tx.TransactionType !== "Payment") continue;
      if (tx.Destination !== SETTLEMENT_ADDRESS) continue;
      const amt = tx.Amount;
      if (!amt || typeof amt !== "object") continue;
      if (amt.currency !== RLUSD_CURRENCY && amt.currency !== "RLUSD") continue;
      if (amt.issuer !== RLUSD_ISSUER) continue;
      const hash = tx.hash;
      if (!hash) continue;
      fromChain.push({
        rlusdAmount: String(amt.value),
        txHash: hash,
        explorerUrl: `${EXPLORER}/${hash}`,
        settlementAddress: SETTLEMENT_ADDRESS,
        wireQueued: true,
        at: typeof tx.date === "number" ? tx.date + RIPPLE_EPOCH : 0,
      });
    }
    return writeSettlements([...fromChain, ...readSettlementsFile()]);
  } catch (err) {
    console.error("settlement XRPL backfill failed:", err.message);
    return loadSettlements();
  }
}

/** Pays value RLUSD from the pool to destination; returns the tx hash. */
async function payFromPool(client, wallet, destination, value, currency, issuer) {
  const submitted = await client.submitAndWait(
    {
      TransactionType: "Payment",
      Account: wallet.address,
      Destination: destination,
      Amount: { currency, issuer, value },
    },
    { wallet },
  );
  const code = submitted.result.meta?.TransactionResult ?? "tesSUCCESS";
  if (code !== "tesSUCCESS") {
    if (code === "tecPATH_PARTIAL" || code === "tecPATH_DRY") {
      throw new Error(
        `XRPL ${code}: pool likely lacks RLUSD for ${value} ` +
          "(add liquidity with an operator deposit, then retry)",
      );
    }
    throw new Error(`XRPL payment failed: ${code}`);
  }
  return submitted.result.hash;
}

// Payments already made per settle nonce (treasury and merchants), so a retried
// settlement (same pending nonce) never pays either twice.
const PROGRESS_FILE = process.env.WATCHER_SETTLE_PROGRESS_FILE || path.join(__dirname, "..", "settle-progress.json");
function loadProgress() {
  try {
    return JSON.parse(fs.readFileSync(PROGRESS_FILE, "utf8"));
  } catch {
    return {};
  }
}
function saveProgress(p) {
  fs.writeFileSync(PROGRESS_FILE, JSON.stringify(p, null, 2));
}

/**
 * Daily settlement: TEE SETTLE_DAILY freezes what merchants and the TEE
 * treasury are owed; the pool pays the treasury (card fees + interest reserve)
 * and the merchants (card spend less the fee); SETTLE_CONFIRM clears it.
 */
// One settlement at a time: a second request while one runs (e.g. a re-click
// after the browser gave up waiting) gets the same run instead of paying again.
let settling = null;
export function settleDaily() {
  settling = settling ?? runSettlement().finally(() => {
    settling = null;
  });
  return settling;
}

async function runSettlement() {
  if (!POOL_SEED) {
    throw new Error("XRPL_POOL_SEED not set — cannot submit RLUSD Payment");
  }

  const prepared = await settleOp("SETTLE_DAILY");
  const amount = prepared.rlusdAmount;
  const destination = prepared.settlementAddress;
  const currency = prepared.rlusdCurrency;
  const issuer = prepared.rlusdIssuer;
  const settleNonce = prepared.settleNonce;
  const treasuryAmount = prepared.treasuryRlusd || "";
  const treasuryAddress = prepared.treasuryAddress || "";
  const settleDailyActionId = prepared.actionId || "";
  const settleDailyFlareTx = prepared.flareTxHash || "";

  console.log(
    `settling ${amount} RLUSD → ${destination}` +
      (treasuryAmount ? `, ${treasuryAmount} RLUSD → treasury ${treasuryAddress}` : "") +
      (settleDailyActionId ? ` tee=${settleDailyActionId}` : "") +
      (settleDailyFlareTx ? ` flare=${settleDailyFlareTx}` : ""),
  );

  const client = await xrplClient();
  const wallet = xrpl.Wallet.fromSeed(POOL_SEED);
  // Payments already made for this nonce (a retry after a failed confirm) are not made again.
  const progress = loadProgress();
  const paid = progress[settleNonce] || {};
  const need =
    (paid.merchantTxHash ? 0 : Number(amount)) + (paid.treasuryTxHash ? 0 : Number(treasuryAmount || 0));
  const bal = await poolRlusdBalance(client, wallet.address);
  if (Number.isFinite(need) && bal != null && bal + 1e-9 < need) {
    throw new Error(
      `pool has ${bal} RLUSD but settlement needs ${need} — ` +
        "add liquidity with an operator deposit, then retry",
    );
  }

  const record = (patch) => {
    Object.assign(paid, patch);
    saveProgress({ ...loadProgress(), [settleNonce]: paid });
  };
  let treasuryTxHash = paid.treasuryTxHash || "";
  if (Number(treasuryAmount) > 0 && treasuryAddress && !treasuryTxHash) {
    treasuryTxHash = await payFromPool(client, wallet, treasuryAddress, treasuryAmount, currency, issuer);
    record({ treasuryTxHash });
  }
  let txHash = paid.merchantTxHash || "";
  if (Number(amount) > 0 && !txHash) {
    txHash = await payFromPool(client, wallet, destination, amount, currency, issuer);
    record({ merchantTxHash: txHash });
  }
  // Only interest/fees to pay out: the treasury payment is the settlement's proof.
  txHash = txHash || treasuryTxHash;

  const confirmed = await settleOp("SETTLE_CONFIRM", {
    settleNonce,
    txHash,
  });
  const done = loadProgress();
  delete done[settleNonce];
  saveProgress(done);

  const result = {
    rlusdAmount: amount,
    txHash,
    explorerUrl: `${EXPLORER}/${txHash}`,
    settlementAddress: destination,
    treasuryRlusd: treasuryAmount,
    treasuryTxHash,
    wireQueued: confirmed.wireQueued === true,
    settleDailyActionId,
    settleConfirmActionId: confirmed.actionId || "",
    settleDailyFlareTx,
    settleConfirmFlareTx: confirmed.flareTxHash || "",
    at: Math.floor(Date.now() / 1000),
  };
  saveSettle(result);
  console.log(
    `settled ${amount} RLUSD tx=${txHash}` +
      (treasuryTxHash ? ` treasury=${treasuryAmount} tx=${treasuryTxHash}` : "") +
      (result.settleConfirmActionId ? ` confirm=${result.settleConfirmActionId}` : "") +
      (result.settleConfirmFlareTx ? ` flareConfirm=${result.settleConfirmFlareTx}` : ""),
  );
  return result;
}

const isMain =
  process.argv[1] &&
  fileURLToPath(import.meta.url) === path.resolve(process.argv[1]);

if (isMain) {
  settleDaily()
    .then(async (r) => {
      console.log(JSON.stringify(r, null, 2));
      await closeXrplClient();
      process.exit(0);
    })
    .catch((e) => {
      console.error(e);
      process.exit(1);
    });
}

/**
 * XRPL watcher → TEE (idempotent by tx hash):
 *   XRP with a tag to the deposit account  → CREDIT_DEPOSIT (collateral)
 *   RLUSD with a tag to the pool           → CREDIT_RLUSD_REPAYMENT (debt repaid,
 *                                            e.g. by a loan conversion)
 * Also exposes HTTP helpers for daily settlement, loan conversion and liquidity providers.
 */
import fs from "node:fs";
import http from "node:http";
import path from "node:path";
import { fileURLToPath } from "node:url";
import xrpl from "xrpl";
import { card } from "./tee.js";
import { NETWORK, RLUSD_CURRENCY, RLUSD_ISSUER, WS } from "./network.js";
import { xrplClient } from "./xrpl-client.js";
import { mintRlusd, rlusdAccount } from "./mint-rlusd.js";
import { prepareForDevice, submitFromDevice } from "./hardware-wallet.js";
import { allocationFor, saveAllocation } from "./lp-allocations.js";
import { LP_DEPOSIT, jobsFor, lpConfigured, onLPPayment, operatorDeposit, runJobs, sharePosition, unreachable } from "./lp.js";
import { brokerLog } from "./broker.js";
import {
  loadConversions,
  loanPaymentsForUser,
  loansForUser,
  payLoan,
  resumeConversion,
  startConversion,
} from "./convert.js";
import {
  backfillSettlementsFromXrpl,
  settleDaily,
  poolRlusdBalance,
} from "./settle.js";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const STATE_FILE = process.env.WATCHER_STATE_FILE || path.join(__dirname, "..", "processed.json");
const DEPOSIT = process.env.XRPL_DEPOSIT_ACCOUNT;
const POOL_SEED = process.env.XRPL_POOL_SEED;
const POOL = process.env.XRPL_POOL_ADDRESS || (POOL_SEED ? xrpl.Wallet.fromSeed(POOL_SEED).address : "");
const HTTP_PORT = Number(process.env.WATCHER_HTTP_PORT || 8090);
// How often the watcher re-reads its accounts' history to catch missed events.
const RESCAN_MS = Number(process.env.WATCHER_RESCAN_MS || 10_000);

if (!DEPOSIT) {
  console.error("XRPL_DEPOSIT_ACCOUNT is required");
  process.exit(1);
}

function poolInfo() {
  if (!POOL_SEED) return null;
  try {
    const wallet = xrpl.Wallet.fromSeed(POOL_SEED);
    return {
      address: wallet.address,
      rlusd: { currency: RLUSD_CURRENCY, issuer: RLUSD_ISSUER },
      network: NETWORK,
      note: `${NETWORK} demo RLUSD comes from the mock issuer; add liquidity with an operator deposit (POST /operator/deposit).`,
    };
  } catch {
    return null;
  }
}

function loadProcessed() {
  try {
    return new Set(JSON.parse(fs.readFileSync(STATE_FILE, "utf8")));
  } catch {
    return new Set();
  }
}

function saveProcessed(set) {
  fs.writeFileSync(STATE_FILE, JSON.stringify([...set], null, 2));
}

const processed = loadProcessed();
// Ignored payments are only logged once per run (the rescan sees them every 10 seconds).
const ignoredOnce = new Set();
const logOnce = (hash, ...msg) => {
  if (!ignoredOnce.has(hash)) console.log(...msg);
  ignoredOnce.add(hash);
};

// A deposit or repayment the TEE refused (e.g. a tag not registered yet) is
// retried, but only every few minutes: on the real TEE each try is a direct action.
const REFUSED_RETRY_MS = 5 * 60_000;
const refusedAt = new Map();
const retryDue = (hash) => !refusedAt.has(hash) || Date.now() - refusedAt.get(hash) >= REFUSED_RETRY_MS;
const noteFailure = (hash, err) => {
  if (!unreachable(err)) refusedAt.set(hash, Date.now());
};

// Collateral is XRP here. Any allow-listed XRPL token could be collateral too
// (e.g. RWA): a trust-line token arrives as {currency, issuer, value} and an MPT
// as {mpt_issuance_id, value} in meta.delivered_amount, like the RLUSD and share
// paths below (README, "Collateral beyond XRP").
function xrpDrops(tx) {
  // Newer rippled/ws payloads often use DeliverMax; classic Amount string = XRP drops.
  if (typeof tx.Amount === "string") return tx.Amount;
  if (typeof tx.DeliverMax === "string") return tx.DeliverMax;
  return null;
}

/** Tagged RLUSD that reached the pool repays that user's debt. */
async function onPoolPayment(tx, meta) {
  if (typeof tx.DestinationTag !== "number") return; // untagged: pool liquidity (mint, top-up)
  // delivered_amount, never Amount: a partial payment can deliver less than it names.
  const delivered = meta?.delivered_amount ?? meta?.DeliveredAmount;
  if (
    typeof delivered !== "object" ||
    delivered.currency !== RLUSD_CURRENCY ||
    delivered.issuer !== RLUSD_ISSUER
  ) {
    logOnce(tx.hash, "ignoring non-RLUSD payment to pool", tx.hash);
    return;
  }
  if (processed.has(tx.hash) || !retryDue(tx.hash)) return;
  console.log(`repayment tag=${tx.DestinationTag} rlusd=${delivered.value} hash=${tx.hash}`);
  try {
    const data = await card("CREDIT_RLUSD_REPAYMENT", {
      tag: tx.DestinationTag,
      rlusdAmount: delivered.value,
      txHash: tx.hash,
    });
    processed.add(tx.hash);
    saveProcessed(processed);
    console.log("repayment credited", data);
  } catch (err) {
    console.error("CREDIT_RLUSD_REPAYMENT failed:", err.message);
    noteFailure(tx.hash, err);
  }
}

async function onPayment(tx, meta) {
  if (!tx || tx.TransactionType !== "Payment") return;
  if (meta?.TransactionResult && meta.TransactionResult !== "tesSUCCESS") return;
  if (POOL && tx.Destination === POOL) return onPoolPayment(tx, meta);
  if (LP_DEPOSIT && tx.Destination === LP_DEPOSIT) {
    // Recorded in processed.json like deposits and repayments, so a restart,
    // the periodic rescan or a reset that clears lp-jobs.json never replays it.
    if (processed.has(tx.hash)) return;
    if (await onLPPayment(tx, meta)) {
      processed.add(tx.hash);
      saveProcessed(processed);
    }
    return;
  }
  if (tx.Destination !== DEPOSIT) return;
  if (typeof tx.DestinationTag !== "number") {
    logOnce(tx.hash, "ignoring payment without DestinationTag", tx.hash);
    return;
  }
  const drops = xrpDrops(tx);
  if (!drops) {
    logOnce(tx.hash, "ignoring non-XRP payment", tx.hash, {
      amountType: typeof tx.Amount,
      deliverMaxType: typeof tx.DeliverMax,
    });
    return;
  }
  if (processed.has(tx.hash) || !retryDue(tx.hash)) return;

  console.log(`deposit tag=${tx.DestinationTag} drops=${drops} hash=${tx.hash}`);
  try {
    const data = await card("CREDIT_DEPOSIT", {
      tag: tx.DestinationTag,
      drops,
      txHash: tx.hash,
    });
    processed.add(tx.hash);
    saveProcessed(processed);
    console.log("credited", data);
  } catch (err) {
    console.error("CREDIT_DEPOSIT failed:", err.message);
    noteFailure(tx.hash, err);
  }
}

async function backfill(client, account, { quiet = false } = {}) {
  let marker;
  let pages = 0;
  do {
    const req = {
      command: "account_tx",
      account,
      ledger_index_min: -1,
      ledger_index_max: -1,
      limit: 100,
    };
    if (marker) req.marker = marker;
    const res = await client.request(req);
    const rows = res.result.transactions || [];
    pages++;
    if (!quiet) console.log(`backfill ${account} page ${pages}: ${rows.length} txs`);
    for (const row of rows) {
      if (row.validated === false) continue;
      const meta = row.meta || row.metaData;
      if (meta?.TransactionResult && meta.TransactionResult !== "tesSUCCESS") continue;
      const tx = row.tx_json || row.tx || row.transaction;
      if (!tx) continue;
      const hash = row.hash || tx.hash;
      if (hash && !tx.hash) tx.hash = hash;
      await onPayment(tx, meta);
    }
    marker = res.result.marker;
  } while (marker && pages < 5);
}

async function listenXrpl() {
  const client = await xrplClient();
  console.log(`XRPL connected ${WS}`);
  const accounts = [DEPOSIT, POOL, LP_DEPOSIT].filter(Boolean);
  console.log(
    `watching deposit account ${DEPOSIT}` +
      (POOL ? `, pool ${POOL}` : " (no pool: repayments not watched)") +
      (LP_DEPOSIT ? `, LP deposit account ${LP_DEPOSIT}` : ""),
  );

  // Backfill first so nothing sent while we were away is missed, then subscribe.
  const catchUp = async () => {
    for (const account of accounts) await backfill(client, account);
    await client.request({ command: "subscribe", accounts });
  };
  await catchUp();

  // Safety net: if a subscription event is ever missed, the next rescan finds
  // the transaction (everything is idempotent on the tx hash). A rescan that is
  // still running when the next one is due is not doubled up.
  let rescanning = false;
  setInterval(async () => {
    if (rescanning) return;
    rescanning = true;
    try {
      for (const account of accounts) await backfill(client, account, { quiet: true });
    } catch (e) {
      console.error("rescan:", e.message);
    } finally {
      rescanning = false;
    }
  }, RESCAN_MS);

  // xrpl.js reconnects on its own after a dropped connection but does not
  // restore subscriptions, so the watcher would silently stop seeing payments.
  client.on("connected", () => {
    console.log("XRPL reconnected; catching up and resubscribing");
    catchUp().catch((e) => console.error("resubscribe failed:", e.message));
  });

  client.on("transaction", (ev) => {
    const tx = ev.tx_json || ev.transaction;
    const hash = ev.hash || tx?.hash;
    if (tx && hash && !tx.hash) tx.hash = hash;
    onPayment(tx, ev.meta).catch((e) => console.error(e));
  });
}

const FAUCET_MAX = 5000;

function sendJSON(res, status, body) {
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(JSON.stringify(body));
}

async function readBody(req) {
  let body = "";
  for await (const chunk of req) body += chunk;
  return JSON.parse(body || "{}");
}

/** Loan conversion routes; returns false when the request is not one of them. */
async function loanRoutes(req, res) {
  const url = new URL(req.url, "http://watcher");
  const route = `${req.method} ${url.pathname}`;
  try {
    if (route === "POST /convert") {
      const { tag, rlusdAmount } = await readBody(req);
      if (!Number.isInteger(tag) || tag <= 0) throw new Error("tag required");
      const conversion = await startConversion({ tag, rlusdAmount: rlusdAmount ? String(rlusdAmount) : "" });
      sendJSON(res, conversion.status === "failed" ? 502 : 200, { ok: conversion.status !== "failed", conversion });
      return true;
    }
    const resume = url.pathname.match(/^\/conversions\/([^/]+)\/resume$/);
    if (req.method === "POST" && resume) {
      const conversion = await resumeConversion(decodeURIComponent(resume[1]));
      sendJSON(res, conversion.status === "failed" ? 502 : 200, { ok: conversion.status !== "failed", conversion });
      return true;
    }
    if (route === "GET /conversions") {
      const tag = Number(url.searchParams.get("tag"));
      const conversions = loadConversions().filter((c) => !tag || c.tag === tag);
      sendJSON(res, 200, { ok: true, conversions });
      return true;
    }
    if (route === "GET /loans") {
      const user = url.searchParams.get("user") || "";
      if (!user.startsWith("r")) throw new Error("user (classic address) required");
      sendJSON(res, 200, { ok: true, ...(await loansForUser(user)) });
      return true;
    }
    if (route === "GET /loans/payments") {
      const user = url.searchParams.get("user") || "";
      if (!user.startsWith("r")) throw new Error("user (classic address) required");
      sendJSON(res, 200, { ok: true, payments: loanPaymentsForUser(user) });
      return true;
    }
    if (route === "POST /loans/pay") {
      const { txHash, loanId } = await readBody(req);
      if (!txHash || !loanId) throw new Error("txHash and loanId required");
      sendJSON(res, 200, { ok: true, ...(await payLoan({ txHash, loanId })) });
      return true;
    }
    // Devnet demo faucet (Faucets, Loans and Liquidity pages): mock RLUSD to
    // any address with a trust line to the mock issuer, up to FAUCET_MAX a call.
    if (route === "POST /rlusd/mint-user") {
      const { address, amount = "100" } = await readBody(req);
      if (!xrpl.isValidClassicAddress(address)) throw new Error("classic address required");
      if (!(Number(amount) > 0 && Number(amount) <= FAUCET_MAX)) throw new Error(`amount must be between 0 and ${FAUCET_MAX}`);
      const acct = await rlusdAccount(address);
      if (!acct.funded) throw new Error("account not found on the ledger — fund it with XRP first");
      if (!acct.trustLine) throw new Error("no RLUSD trust line — add one first");
      const txHash = await mintRlusd(address, String(amount));
      sendJSON(res, 200, { ok: true, amount: String(amount), txHash });
      return true;
    }
    // Hardware wallets (Yield page): prepare a transaction for the device, then submit its signature.
    if (route === "POST /hw/prepare") {
      sendJSON(res, 200, { ok: true, ...(await prepareForDevice(await readBody(req))) });
      return true;
    }
    if (route === "POST /hw/submit") {
      sendJSON(res, 200, { ok: true, ...(await submitFromDevice(await readBody(req))) });
      return true;
    }
    // Next round: an LP's off-chain pledge to the uncollateralized protocol.
    if (route === "GET /lp/allocation") {
      sendJSON(res, 200, { ok: true, ...allocationFor(url.searchParams.get("address") || "") });
      return true;
    }
    if (route === "POST /lp/allocation") {
      sendJSON(res, 200, { ok: true, ...saveAllocation(await readBody(req)) });
      return true;
    }
    if (route === "GET /rlusd/account") {
      const address = url.searchParams.get("address") || "";
      if (!xrpl.isValidClassicAddress(address)) throw new Error("classic address required");
      sendJSON(res, 200, { ok: true, address, ...(await rlusdAccount(address)) });
      return true;
    }
    if (route === "POST /operator/deposit") {
      const { amount = "10000" } = await readBody(req);
      if (!(Number(amount) > 0 && Number(amount) <= 1_000_000)) throw new Error("amount must be between 0 and 1000000");
      sendJSON(res, 200, { ok: true, amount: String(amount), ...(await operatorDeposit(String(amount))) });
      return true;
    }
    if (route === "GET /lp/position") {
      const address = url.searchParams.get("address") || "";
      if (!xrpl.isValidClassicAddress(address)) throw new Error("classic address required");
      sendJSON(res, 200, { ok: true, address, ...(await sharePosition(address)), jobs: jobsFor(address) });
      return true;
    }
    if (route === "GET /broker/log") {
      sendJSON(res, 200, { ok: true, log: brokerLog() });
      return true;
    }
  } catch (err) {
    sendJSON(res, 400, { ok: false, error: err.message });
    return true;
  }
  return false;
}

function startHttp() {
  const server = http.createServer(async (req, res) => {
    res.setHeader("Access-Control-Allow-Origin", "*");
    res.setHeader("Access-Control-Allow-Headers", "Content-Type");
    res.setHeader("Access-Control-Allow-Methods", "GET, POST, OPTIONS");
    if (req.method === "OPTIONS") {
      res.writeHead(204);
      res.end();
      return;
    }

    if (req.method === "POST" && req.url === "/settle") {
      try {
        const result = await settleDaily();
        res.writeHead(200, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ ok: true, ...result }));
      } catch (err) {
        res.writeHead(400, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ ok: false, error: err.message }));
      }
      return;
    }

    if (req.method === "GET" && req.url === "/health") {
      const pool = poolInfo();
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(
        JSON.stringify({
          ok: true,
          deposit: DEPOSIT,
          pool: pool?.address ?? null,
          processed: processed.size,
        }),
      );
      return;
    }

    if (req.method === "GET" && req.url === "/settlements") {
      try {
        const settlements = await backfillSettlementsFromXrpl();
        res.writeHead(200, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ ok: true, settlements }));
      } catch (err) {
        res.writeHead(500, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ ok: false, error: err.message }));
      }
      return;
    }


    if (req.method === "GET" && req.url === "/pool") {
      const pool = poolInfo();
      if (!pool) {
        res.writeHead(503, { "Content-Type": "application/json" });
        res.end(
          JSON.stringify({
            ok: false,
            error: "XRPL_POOL_SEED not set — cannot derive pool address",
          }),
        );
        return;
      }
      const balance = await poolRlusdBalance(await xrplClient(), pool.address).catch(() => null);
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ ok: true, ...pool, balanceRlusd: balance == null ? null : String(balance) }));
      return;
    }

    if (await loanRoutes(req, res)) return;

    res.writeHead(404, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ ok: false, error: "not found" }));
  });
  // Node closes idle keep-alive sockets after 5 s; clients (the web proxy, fetch)
  // reusing one at that moment get "other side closed". Keep them open longer.
  server.keepAliveTimeout = 65_000;
  server.headersTimeout = 66_000;
  server.listen(HTTP_PORT, () => {
    const pool = poolInfo();
    console.log(`watcher HTTP :${HTTP_PORT}`);
    if (pool) console.log(`RLUSD pool ${pool.address} — add liquidity with POST /operator/deposit`);
    if (lpConfigured()) {
      console.log(`LP deposit account ${LP_DEPOSIT} (share issues and sweeps retried every minute)`);
      // Retry pending share issues / payouts (e.g. waiting for an opt-in) and sweep deposits.
      runJobs().catch((e) => console.error("LP jobs:", e.message));
      setInterval(() => runJobs().catch((e) => console.error("LP jobs:", e.message)), 60_000);
    }
  });
}

await listenXrpl();
startHttp();

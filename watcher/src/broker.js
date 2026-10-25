/**
 * Mock loan broker. Stands in for an outside broker operating its own wallet:
 * it countersigns a LoanSet only when it matches a conversion the TEE issued
 * (CONVERT_REQUEST) for that user's repayment account, with the standard terms.
 * Every decision is logged to broker-log.json for the Mock Broker page.
 *
 * In a real deployment the TEE would wait for the broker's confirmation of the
 * loan before starting the conversion; here the broker approves automatically.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import xrpl from "xrpl";
import { counterpartySignature, isRepaymentAccountFor } from "./lending.js";
import { card } from "./tee.js";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const LOG_FILE = process.env.WATCHER_BROKER_LOG_FILE || path.join(__dirname, "..", "broker-log.json");
const BROKER_SEED = process.env.XRPL_BROKER_SEED || "";
const LOAN_BROKER_ID = process.env.LOAN_BROKER_ID || "";
const TERMS = process.env.LOAN_TERMS ? JSON.parse(process.env.LOAN_TERMS) : null;

export function brokerLog() {
  try {
    return JSON.parse(fs.readFileSync(LOG_FILE, "utf8"));
  } catch {
    return [];
  }
}

function record(entry) {
  const rows = [{ ...entry, at: Math.floor(Date.now() / 1000) }, ...brokerLog()].slice(0, 200);
  fs.writeFileSync(LOG_FILE, JSON.stringify(rows, null, 2));
}

async function check(client, loanSet, { tag, conversionId, xrplAddress: userAddress }) {
  if (!BROKER_SEED || !LOAN_BROKER_ID || !TERMS) return "broker not configured (run setup-lending.js)";
  if (loanSet.TransactionType !== "LoanSet") return "not a LoanSet";
  if (loanSet.LoanBrokerID !== LOAN_BROKER_ID) return "another loan broker";
  for (const [field, key] of [
    ["InterestRate", "interestRate"],
    ["PaymentTotal", "paymentTotal"],
    ["PaymentInterval", "paymentInterval"],
    ["GracePeriod", "gracePeriod"],
  ]) {
    if (loanSet[field] !== TERMS[key]) return `non-standard ${field}`;
  }
  for (const f of ["LoanOriginationFee", "LoanServiceFee", "Counterparty"]) {
    if (loanSet[f] !== undefined) return `unexpected ${f}`;
  }
  // The conversion must be one the TEE issued and still holds open, for this amount.
  const pos = await card("GET_POSITION", { tag });
  if (pos.xrplAddress !== userAddress) return "tag does not belong to this user";
  const conv = (pos.pendingConversions ?? []).find((c) => c.id === conversionId);
  if (!conv) return "no open TEE conversion with this id";
  if (Number(conv.rlusdAmount) !== Number(loanSet.PrincipalRequested)) return "principal differs from the TEE conversion";
  if (!(await isRepaymentAccountFor(client, loanSet.Account, userAddress))) {
    return "borrower is not the user's repayment account";
  }
  return null;
}

/**
 * Returns the broker's CounterpartySignature for loanSet, or throws with the
 * reason it was refused.
 */
export async function approveLoanSet(client, loanSet, conversion) {
  const reason = await check(client, loanSet, conversion);
  const base = {
    conversionId: conversion.conversionId,
    tag: conversion.tag,
    borrower: loanSet.Account,
    principal: String(loanSet.PrincipalRequested),
  };
  if (reason) {
    record({ ...base, approved: false, reason });
    throw new Error(`broker refused: ${reason}`);
  }
  record({ ...base, approved: true });
  return counterpartySignature(xrpl.Wallet.fromSeed(BROKER_SEED), loanSet);
}

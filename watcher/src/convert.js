/**
 * Loan conversion, run by the watcher as the Credit Server:
 *   1. TEE CONVERT_REQUEST reserves the debt and names the loan to take
 *   2. the user's repayment account (created once, then reused)
 *   3. mock broker countersigns the LoanSet
 *   4. LoanSet (principal from the vault to the repayment account)
 *   5. Payment of the principal to the pool with the user's tag, right after
 * The TEE never hears "converted": the deposit watcher sees step 5 arrive at the
 * pool and calls CREDIT_RLUSD_REPAYMENT. If step 5 never happens, the TEE's
 * reservation lapses and the debt is unchanged.
 *
 * Steps 4 and 5 are two transactions: LoanSet cannot be a Batch inner until
 * LendingProtocolV1_2. Between them the borrowed RLUSD sits in the repayment
 * account, which the user can also sign for; resume() retries step 5.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import xrpl from "xrpl";
import { approveLoanSet } from "./broker.js";
import {
  buildLoanSet,
  createRepaymentAccount,
  ensureSpendableXrp,
  isRepaymentAccountFor,
  loansOf,
  rlusd,
  signAsRepaymentAccount,
  submitLoanSet,
  submitOk,
} from "./lending.js";
import { EXPLORER } from "./network.js";
import { withClient } from "./xrpl-client.js";
import { card } from "./tee.js";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const CONVERSIONS_FILE =
  process.env.WATCHER_CONVERSIONS_FILE || path.join(__dirname, "..", "conversions.json");
const ACCOUNTS_FILE =
  process.env.WATCHER_REPAYMENT_ACCOUNTS_FILE || path.join(__dirname, "..", "repayment-accounts.json");
const LOAN_PAYMENTS_FILE =
  process.env.WATCHER_LOAN_PAYMENTS_FILE || path.join(__dirname, "..", "loan-payments.json");

function readJSON(file, fallback) {
  try {
    return JSON.parse(fs.readFileSync(file, "utf8"));
  } catch {
    return fallback;
  }
}

/** Conversions, newest first. */
export function loadConversions() {
  return readJSON(CONVERSIONS_FILE, []);
}

function saveConversion(conv) {
  const rows = loadConversions().filter((c) => c.id !== conv.id);
  rows.unshift({ ...conv, updatedAt: Math.floor(Date.now() / 1000) });
  rows.sort((a, b) => b.createdAt - a.createdAt);
  fs.writeFileSync(CONVERSIONS_FILE, JSON.stringify(rows, null, 2));
}

/** user XRPL address -> repayment account. */
export function repaymentAccounts() {
  return readJSON(ACCOUNTS_FILE, {});
}

function saveRepaymentAccount(user, account) {
  fs.writeFileSync(ACCOUNTS_FILE, JSON.stringify({ ...repaymentAccounts(), [user]: account }, null, 2));
}

// One conversion at a time: they share the pool's and repayment accounts' sequences.
let queue = Promise.resolve();
function serialized(fn) {
  const run = queue.then(fn, fn);
  queue = run.catch(() => {});
  return run;
}

const explorer = (hash) => `${EXPLORER}/${hash}`;

/** A loan on conv's repayment account taken for conv but not recorded: new since conv.loansBefore, same broker and principal, not claimed by another conversion. */
async function findTakenLoan(client, conv) {
  const claimed = new Set(loadConversions().map((c) => c.loanId).filter(Boolean));
  const loans = await loansOf(client, conv.repaymentAccount);
  return loans.find(
    (l) =>
      !conv.loansBefore.includes(l.loanId) &&
      !claimed.has(l.loanId) &&
      l.loanBrokerId === conv.loanBrokerId &&
      Number(l.principalOutstanding) === Number(conv.rlusdAmount),
  )?.loanId;
}

/** Runs the remaining steps of conv, saving after each, until done or failed. */
async function advance(client, conv) {
  const step = async (status, fn) => {
    conv.status = status;
    conv.error = undefined;
    saveConversion(conv);
    await fn();
  };
  try {
    if (!conv.repaymentAccount) {
      await step("creating-account", async () => {
        const known = repaymentAccounts()[conv.xrplAddress];
        if (known && (await isRepaymentAccountFor(client, known, conv.xrplAddress))) {
          conv.repaymentAccount = known;
          conv.accountReused = true;
        } else {
          conv.repaymentAccount = await createRepaymentAccount(client, conv.xrplAddress);
          saveRepaymentAccount(conv.xrplAddress, conv.repaymentAccount);
        }
      });
    }
    if (!conv.loanId && conv.loansBefore) {
      // A previous attempt may have taken the loan although its answer was lost
      // (dropped connection after LoanSet validated): adopt it, never take a second one.
      const taken = await findTakenLoan(client, conv);
      if (taken) {
        conv.loanId = taken;
        conv.loanAdopted = true;
        saveConversion(conv);
      }
    }
    if (!conv.loanId) {
      await step("broker-approval", async () => {
        const topUp = await ensureSpendableXrp(client, conv.repaymentAccount);
        if (topUp) conv.topUpTx = topUp;
        const loanSet = await buildLoanSet(client, {
          borrower: conv.repaymentAccount,
          loanBrokerId: conv.loanBrokerId,
          principal: conv.rlusdAmount,
          terms: conv.terms,
        });
        loanSet.CounterpartySignature = await approveLoanSet(client, loanSet, conv);
        conv.brokerApproved = true;
        conv.status = "taking-loan";
        conv.loansBefore = (await loansOf(client, conv.repaymentAccount)).map((l) => l.loanId);
        saveConversion(conv);
        const { hash, loanId } = await submitLoanSet(client, loanSet);
        conv.loanSetTx = hash;
        conv.loanId = loanId;
      });
    }
    if (!conv.poolPaymentTx) {
      // Signed only now: a pre-signed payment can expire while LoanSet is pending.
      await step("repaying-tee", async () => {
        const blob = await signAsRepaymentAccount(client, conv.repaymentAccount, {
          TransactionType: "Payment",
          Destination: conv.poolAccount,
          DestinationTag: conv.destinationTag,
          Amount: rlusd(conv.rlusdAmount),
        });
        const res = await submitOk(client, blob, undefined, "Payment to pool");
        conv.poolPaymentTx = res.result.hash;
      });
    }
    conv.status = "repaid-to-pool";
    saveConversion(conv);
  } catch (err) {
    conv.status = "failed";
    conv.error = err.message;
    saveConversion(conv);
  }
  return conv;
}

/**
 * Starts a conversion of rlusdAmount (all convertible debt when empty) for tag.
 * Returns once the principal is repaid to the pool, or the conversion failed.
 */
export function startConversion({ tag, rlusdAmount }) {
  return serialized(async () => {
    const tee = await card("CONVERT_REQUEST", rlusdAmount ? { tag, rlusdAmount } : { tag });
    const conv = {
      id: `${tag}-${tee.conversionId}-${Date.now()}`,
      conversionId: tee.conversionId,
      tag: tee.tag,
      xrplAddress: tee.xrplAddress,
      rlusdAmount: tee.rlusdAmount,
      poolAccount: tee.poolAccount,
      destinationTag: tee.destinationTag,
      loanBrokerId: tee.loanBrokerId,
      terms: tee.terms,
      expiresAt: Math.floor(Date.now() / 1000) + tee.ttlSeconds,
      teeActionId: tee.actionId,
      createdAt: Math.floor(Date.now() / 1000),
      status: "requested",
    };
    saveConversion(conv);
    return withClient((client) => advance(client, conv));
  });
}

/** Retries a failed conversion from the step it stopped at. */
export function resumeConversion(id) {
  return serialized(async () => {
    const conv = loadConversions().find((c) => c.id === id);
    if (!conv) throw new Error("unknown conversion");
    if (conv.status === "repaid-to-pool") return conv;
    // Before a LoanSet was ever sent, a lapsed TEE reservation means start over.
    // Once one was sent, resume anyway: the loan may exist and must be adopted
    // (a retried LoanSet is refused by the broker after the reservation lapses).
    if (!conv.loanId && !conv.loansBefore && conv.expiresAt <= Math.floor(Date.now() / 1000)) {
      throw new Error("TEE reservation expired; start a new conversion");
    }
    return withClient((client) => advance(client, conv));
  });
}

/** The user's repayment account and its loans, read from the ledger. */
export async function loansForUser(user) {
  const account = repaymentAccounts()[user];
  if (!account) return { repaymentAccount: null, loans: [] };
  return withClient(async (client) => ({
    repaymentAccount: account,
    loans: await loansOf(client, account),
  }));
}

/** Installments the Credit Server paid for user, newest first. */
export function loanPaymentsForUser(user) {
  const account = repaymentAccounts()[user];
  // Older records have no user; they are matched by repayment account below.
  return readJSON(LOAN_PAYMENTS_FILE, []).filter(
    (p) => p.user === user || (!p.user && account && p.repaymentAccount === account),
  );
}

/**
 * Pays a loan installment the user funded: txHash must be a validated RLUSD
 * Payment from the user to their repayment account. The Credit Server then
 * signs LoanPay for exactly the delivered amount on one of that account's loans.
 */
export function payLoan({ txHash, loanId }) {
  return serialized(() => withClient(async (client) => {
    const res = await client.request({ command: "tx", transaction: txHash });
    const tx = res.result.tx_json || res.result;
    const meta = res.result.meta;
    if (!res.result.validated || meta?.TransactionResult !== "tesSUCCESS") throw new Error("funding payment not validated");
    if (tx.TransactionType !== "Payment") throw new Error("funding tx is not a Payment");
    const used = readJSON(LOAN_PAYMENTS_FILE, []);
    if (used.some((p) => p.fundingTx === txHash)) throw new Error("this funding payment was already used");
    const account = repaymentAccounts()[tx.Account];
    if (!account || tx.Destination !== account) throw new Error("payment is not from a user to their repayment account");
    const delivered = meta.delivered_amount;
    if (typeof delivered !== "object" || !delivered.value) throw new Error("payment did not deliver RLUSD");
    const loans = await loansOf(client, account);
    if (!loans.some((l) => l.loanId === loanId)) throw new Error("loan does not belong to this repayment account");

    const blob = await signAsRepaymentAccount(client, account, {
      TransactionType: "LoanPay",
      LoanID: loanId,
      Amount: rlusd(delivered.value),
    });
    const paid = await submitOk(client, blob, undefined, "LoanPay");
    used.unshift({
      user: tx.Account,
      repaymentAccount: account,
      fundingTx: txHash,
      loanId,
      loanPayTx: paid.result.hash,
      amount: delivered.value,
      at: Math.floor(Date.now() / 1000),
    });
    fs.writeFileSync(LOAN_PAYMENTS_FILE, JSON.stringify(used, null, 2));
    return {
      loanPayTx: paid.result.hash,
      explorerUrl: explorer(paid.result.hash),
      amount: delivered.value,
      loans: await loansOf(client, account),
    };
  }));
}

/**
 * XRPL plumbing for loan conversion: repayment accounts, LoanSet signing and
 * loan lookups. Keys come from watcher/.env (written by sync-config from
 * wallets.json).
 *
 * TODO(TEE): the Credit Server cosigner key is held here by the watcher, which
 * is trusted to act honestly for the demo. It belongs in the TEE as an XRP
 * wallet key (pkg/node SignWallet), with the TEE checking each transaction it
 * signs as a user's repayment account.
 */
import xrpl from "xrpl";
import { sign as keypairSign } from "ripple-keypairs";
import { RLUSD_CURRENCY, RLUSD_ISSUER } from "./network.js";

const POOL_SEED = process.env.XRPL_POOL_SEED || "";
const CREDIT_SERVER_SEED = process.env.XRPL_CREDIT_SERVER_SEED || "";

export const rlusd = (value) => ({ currency: RLUSD_CURRENCY, issuer: RLUSD_ISSUER, value: String(value) });

/** XRP a fresh repayment account gets: base reserve plus trust line and signer list, with room for loans and fees. */
const REPAYMENT_ACCOUNT_XRP = "5";
/** Top the account up when its spendable XRP drops below this (each loan adds an owner reserve). */
const MIN_SPENDABLE_XRP = 1;

export function poolWallet() {
  if (!POOL_SEED) throw new Error("XRPL_POOL_SEED not set");
  return xrpl.Wallet.fromSeed(POOL_SEED);
}

export function creditServerWallet() {
  if (!CREDIT_SERVER_SEED) throw new Error("XRPL_CREDIT_SERVER_SEED not set — run setup-lending.js and sync-config");
  return xrpl.Wallet.fromSeed(CREDIT_SERVER_SEED);
}

export function txResult(res) {
  return res.result.meta?.TransactionResult;
}

/** Submits and requires tesSUCCESS; returns the validated result. */
export async function submitOk(client, txOrBlob, opts, what) {
  const res = await client.submitAndWait(txOrBlob, opts);
  const code = txResult(res);
  if (code !== "tesSUCCESS") throw new Error(`${what}: ${code}`);
  return res;
}

/** Autofills tx for repaymentAccount and signs it as that account's Credit Server signer. */
export async function signAsRepaymentAccount(client, repaymentAccount, tx) {
  const cs = creditServerWallet();
  const filled = await client.autofill({ ...tx, Account: repaymentAccount }, 1);
  return cs.sign(filled, true).tx_blob;
}

/**
 * Creates a user's repayment account: funded by the pool, RLUSD trust line, a
 * 1-of-2 signer list (user, Credit Server), master key disabled. The account's
 * own seed is discarded, so only the two signers control it afterwards.
 * Separate transactions, not a Batch: safe to retry, and Devnet's Batch did not
 * apply these inner transactions in our tests.
 */
export async function createRepaymentAccount(client, userAddress) {
  const pool = poolWallet();
  const cs = creditServerWallet();
  const acct = xrpl.Wallet.generate();
  await submitOk(client, {
    TransactionType: "Payment",
    Account: pool.address,
    Destination: acct.address,
    Amount: xrpl.xrpToDrops(REPAYMENT_ACCOUNT_XRP),
  }, { wallet: pool }, "fund repayment account");
  await submitOk(client, {
    TransactionType: "TrustSet",
    Account: acct.address,
    LimitAmount: rlusd("1000000000"),
    Flags: xrpl.TrustSetFlags.tfSetNoRipple,
  }, { wallet: acct }, "repayment account trust line");
  await submitOk(client, {
    TransactionType: "SignerListSet",
    Account: acct.address,
    SignerQuorum: 1,
    SignerEntries: [
      { SignerEntry: { Account: userAddress, SignerWeight: 1 } },
      { SignerEntry: { Account: cs.address, SignerWeight: 1 } },
    ],
  }, { wallet: acct }, "repayment account signer list");
  await submitOk(client, {
    TransactionType: "AccountSet",
    Account: acct.address,
    SetFlag: xrpl.AccountSetAsfFlags.asfDisableMaster,
  }, { wallet: acct }, "repayment account disable master key");
  return acct.address;
}

/** Checks on chain that account is a repayment account for userAddress with our Credit Server. */
export async function isRepaymentAccountFor(client, account, userAddress) {
  const cs = creditServerWallet();
  const info = await client.request({ command: "account_info", account, signer_lists: true });
  const list = info.result.signer_lists?.[0] ?? info.result.account_data?.signer_lists?.[0];
  const signers = new Set((list?.SignerEntries ?? []).map((e) => e.SignerEntry.Account));
  return info.result.account_flags?.disableMasterKey === true &&
    list?.SignerQuorum === 1 && signers.size === 2 && signers.has(userAddress) && signers.has(cs.address);
}

/** Adds XRP from the pool when the account's spendable balance runs low. */
export async function ensureSpendableXrp(client, account) {
  const info = await client.request({ command: "account_info", account });
  const server = await client.request({ command: "server_info" });
  const ledger = server.result.info.validated_ledger;
  const owned = info.result.account_data.OwnerCount;
  const reserve = Number(ledger.reserve_base_xrp) + owned * Number(ledger.reserve_inc_xrp);
  const balance = Number(xrpl.dropsToXrp(info.result.account_data.Balance));
  // Room for one more loan object's reserve plus fees.
  if (balance - reserve - Number(ledger.reserve_inc_xrp) >= MIN_SPENDABLE_XRP) return null;
  const pool = poolWallet();
  const res = await submitOk(client, {
    TransactionType: "Payment",
    Account: pool.address,
    Destination: account,
    Amount: xrpl.xrpToDrops("2"),
  }, { wallet: pool }, "top up repayment account");
  return res.result.hash;
}

/**
 * Counterparty signature for a LoanSet. rippled hashes it with the CPT prefix
 * ("CPT\0"), not the usual STX one, so xrpl.js's signLoanSetByCounterparty
 * does not produce a valid signature on this network.
 */
export function counterpartySignature(wallet, loanSet) {
  const signing = xrpl.encodeForSigning({ ...loanSet, SigningPubKey: loanSet.SigningPubKey ?? "" });
  const cpt = "43505400" + signing.slice(8);
  return { SigningPubKey: wallet.publicKey, TxnSignature: keypairSign(cpt, wallet.privateKey) };
}

/** Builds an autofilled LoanSet for a multisigned borrower. */
export async function buildLoanSet(client, { borrower, loanBrokerId, principal, terms }) {
  return client.autofill({
    TransactionType: "LoanSet",
    Account: borrower,
    LoanBrokerID: loanBrokerId,
    PrincipalRequested: String(principal),
    InterestRate: terms.interestRate,
    PaymentTotal: terms.paymentTotal,
    PaymentInterval: terms.paymentInterval,
    GracePeriod: terms.gracePeriod,
  }, 1);
}

function createdLedgerIndex(meta, type) {
  return meta?.AffectedNodes?.find((n) => n.CreatedNode?.LedgerEntryType === type)?.CreatedNode.LedgerIndex;
}

/** Signs the broker-countersigned LoanSet as the borrower and submits it; returns { hash, loanId }. */
export async function submitLoanSet(client, loanSet) {
  const blob = creditServerWallet().sign(loanSet, true).tx_blob;
  const res = await submitOk(client, blob, undefined, "LoanSet");
  const loanId = createdLedgerIndex(res.result.meta, "Loan");
  if (!loanId) throw new Error("LoanSet created no Loan");
  return { hash: res.result.hash, loanId };
}

/** Loans owned by account, with the fields the UI shows. */
export async function loansOf(client, account) {
  const res = await client.request({ command: "account_objects", account, type: "loan" });
  return (res.result.account_objects ?? []).map((l) => ({
    loanId: l.index,
    loanBrokerId: l.LoanBrokerID,
    principalOutstanding: l.PrincipalOutstanding,
    totalValueOutstanding: l.TotalValueOutstanding,
    periodicPayment: l.PeriodicPayment,
    paymentRemaining: l.PaymentRemaining,
    nextPaymentDueDate: l.NextPaymentDueDate ? xrpl.rippleTimeToUnixTime(l.NextPaymentDueDate) / 1000 : null,
    interestRate: l.InterestRate,
  }));
}

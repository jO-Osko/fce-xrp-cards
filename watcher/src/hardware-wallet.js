/**
 * Relay for hardware wallets (Ledger) on the Yield page. The browser has no
 * XRPL connection or codec, so the watcher fills in and encodes the
 * transaction, the device signs those exact bytes, and the watcher submits
 * the result. Keys never leave the device; a changed transaction would not
 * match the signature, so the ledger would reject it.
 *
 * Only TrustSet and Payment are relayed, each with the app's SourceTag.
 */
import xrpl from "xrpl";
import { SOURCE_TAG } from "./source-tag.js";
import { xrplClient } from "./xrpl-client.js";

const ALLOWED = new Set(["TrustSet", "Payment"]);
// Reviewing a transaction on the device takes a while; give it about 4 minutes
// of ledgers instead of autofill's 20.
const EXTRA_LEDGERS = 60;

/** Autofills `tx` for `account` signing with `publicKey`; returns it and the bytes to sign. */
export async function prepareForDevice({ tx, account, publicKey }) {
  if (!tx || !ALLOWED.has(tx.TransactionType)) throw new Error("only TrustSet and Payment are relayed");
  if (!xrpl.isValidClassicAddress(account)) throw new Error("classic address required");
  if (!/^[0-9A-Fa-f]{66}$/.test(publicKey || "")) throw new Error("compressed public key required");
  if (xrpl.deriveAddress(publicKey) !== account) throw new Error("public key does not match the account");
  const client = await xrplClient();
  // The user signs it, so the app's tag is set here, whatever the browser sent.
  const filled = await client.autofill({ Flags: 0, ...tx, Account: account, SigningPubKey: publicKey.toUpperCase(), SourceTag: SOURCE_TAG });
  filled.LastLedgerSequence += EXTRA_LEDGERS;
  return { tx: filled, txBlob: xrpl.encode(filled) };
}

/** Adds the device's signature to a prepared `tx`, submits it and waits for validation. */
export async function submitFromDevice({ tx, signature }) {
  if (!tx || !ALLOWED.has(tx.TransactionType)) throw new Error("only TrustSet and Payment are relayed");
  if (!/^[0-9A-Fa-f]+$/.test(signature || "")) throw new Error("signature required");
  const blob = xrpl.encode({ ...tx, TxnSignature: signature.toUpperCase() });
  const client = await xrplClient();
  const res = await client.submitAndWait(blob);
  const code = res.result.meta?.TransactionResult;
  if (code !== "tesSUCCESS") throw new Error(`${tx.TransactionType} failed: ${code}`);
  return { hash: res.result.hash };
}

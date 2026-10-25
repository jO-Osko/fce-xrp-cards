/**
 * Every XRPL transaction this app submits carries one SourceTag, so its
 * activity can be told apart on the ledger. The tag is set once, in
 * app.config.json (xrplSourceTag); sync-config copies it to watcher/.env
 * (XRPL_SOURCE_TAG) and to the web app, which puts it on every Xaman payload.
 *
 * xrpl.js autofills every transaction before signing (submitAndWait does it
 * for us, and the multisigned paths call autofill themselves), so adding the
 * tag there covers the whole watcher, the setup scripts and the hardware-wallet
 * relay. A transaction that already names a SourceTag keeps it.
 *
 * Import this module before submitting anything; network.js does.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import xrpl from "xrpl";

const CONFIG = path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "..", "app.config.json");

// The setup scripts run before watcher/.env exists, so fall back to the file itself.
function configured() {
  if (process.env.XRPL_SOURCE_TAG) return Number(process.env.XRPL_SOURCE_TAG);
  try {
    return JSON.parse(fs.readFileSync(CONFIG, "utf8")).xrplSourceTag;
  } catch {
    return undefined;
  }
}

export const SOURCE_TAG = configured();
if (!Number.isInteger(SOURCE_TAG) || SOURCE_TAG < 0 || SOURCE_TAG > 0xffffffff) {
  console.error("XRPL source tag not set: put xrplSourceTag in app.config.json, then run node ../scripts/sync-config.mjs");
  process.exit(1);
}

const autofill = xrpl.Client.prototype.autofill;
if (!autofill.sourceTagged) {
  xrpl.Client.prototype.autofill = function autofillWithSourceTag(tx, ...rest) {
    return autofill.call(this, tx.SourceTag === undefined ? { ...tx, SourceTag: SOURCE_TAG } : tx, ...rest);
  };
  xrpl.Client.prototype.autofill.sourceTagged = true;
}

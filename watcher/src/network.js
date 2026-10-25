/**
 * XRPL network and RLUSD settings. scripts/sync-config.mjs writes them to
 * watcher/.env from the `network` and `rlusd` blocks of wallets.json, so the
 * network is chosen in one place (src/fund-wallets.js).
 */
import "./source-tag.js";

function required(name) {
  const v = process.env[name];
  if (!v) {
    console.error(`${name} is not set — run: node ../scripts/sync-config.mjs`);
    process.exit(1);
  }
  return v;
}

export const NETWORK = process.env.XRPL_NETWORK || "XRPL";
export const WS = required("XRPL_WS");
export const EXPLORER = required("XRPL_EXPLORER");
export const RLUSD_CURRENCY = required("RLUSD_CURRENCY");
export const RLUSD_ISSUER = required("RLUSD_ISSUER");
/** Mock RLUSD issuer (Devnet has no real RLUSD); used to mint demo supply. */
export const ISSUER_SEED = process.env.XRPL_ISSUER_SEED || "";

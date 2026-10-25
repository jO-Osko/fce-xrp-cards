/**
 * XRPL network and RLUSD settings from deployment.gen.json, which
 * scripts/sync-config.mjs writes from watcher/wallets.json.
 */
import deployment from "./deployment.gen.json";

export const XRPL_NETWORK = deployment.xrpl.network;
export const NETWORK_NAME = XRPL_NETWORK.name;
export const RLUSD = deployment.xrpl.rlusd;

/**
 * Every XRPL transaction the app submits carries this SourceTag, so its activity
 * can be told apart on the ledger. Set in app.config.json (xrplSourceTag);
 * the watcher adds the same tag to everything it submits.
 */
export const SOURCE_TAG = deployment.xrpl.sourceTag;

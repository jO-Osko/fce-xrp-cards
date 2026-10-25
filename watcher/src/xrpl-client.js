/**
 * One XRPL connection per process. Public nodes throttle by IP, and opening a
 * connection per request (or per page refresh) gets the whole IP throttled,
 * including a phone on the same network signing with Xaman. Everything in the
 * watcher shares this client: subscriptions, lookups and submissions alike.
 */
import xrpl from "xrpl";
import { WS } from "./network.js";

const client = new xrpl.Client(WS);
let connecting = null;

/** The shared client, connected. xrpl.js reconnects it on its own after a drop. */
export async function xrplClient() {
  if (client.isConnected()) return client;
  connecting ??= client.connect().finally(() => {
    connecting = null;
  });
  await connecting;
  return client;
}

/** Runs `fn` with the shared client; the connection stays open afterwards. */
export async function withClient(fn) {
  return fn(await xrplClient());
}

/** For one-shot scripts: closes the connection so the process can exit. */
export async function closeXrplClient() {
  if (client.isConnected()) await client.disconnect();
}

/**
 * account_tx in the API v1 shape (`tx` with `hash` and `date` on each row),
 * which the callers parse; xrpl.js defaults to v2.
 */
export async function accountTx(account, limit) {
  const res = await (await xrplClient()).request({
    command: "account_tx",
    account,
    ledger_index_min: -1,
    ledger_index_max: -1,
    limit,
    api_version: 1,
  });
  return res.result.transactions || [];
}

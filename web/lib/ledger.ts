"use client";

/**
 * Ledger hardware wallet over WebHID (Chrome, Edge, Brave; not Safari or Firefox).
 * The watcher prepares each transaction (POST hw/prepare), the device signs it,
 * and the watcher submits it (POST hw/submit); the key never leaves the device.
 */
import type TransportWebHID from "@ledgerhq/hw-transport-webhid";
import type Xrp from "@ledgerhq/hw-app-xrp";

/** The first XRP account on the device (BIP44 path for XRP, account 0). */
export const LEDGER_PATH = "44'/144'/0'/0/0";

export type LedgerAccount = { address: string; publicKey: string };

let transport: Awaited<ReturnType<typeof TransportWebHID.create>> | null = null;
let app: Xrp | null = null;

export const ledgerSupported = () => typeof navigator !== "undefined" && "hid" in navigator;

let modules: Promise<[typeof import("@ledgerhq/hw-transport-webhid"), typeof import("@ledgerhq/hw-app-xrp")]> | null = null;

/**
 * Loads the Ledger code ahead of the click: the browser only shows the device
 * picker within a few seconds of the click, so it should not wait on a download.
 */
export function preloadLedger() {
  if (!ledgerSupported()) return null;
  modules ??= (async () => {
    // The Ledger libraries use Node's Buffer, which browsers do not have.
    const { Buffer } = await import("buffer");
    (globalThis as { Buffer?: unknown }).Buffer ??= Buffer;
    return Promise.all([import("@ledgerhq/hw-transport-webhid"), import("@ledgerhq/hw-app-xrp")]);
  })();
  return modules;
}

async function open() {
  if (app) return app;
  const loaded = preloadLedger();
  if (!loaded) throw new Error("This browser cannot reach a Ledger over USB. Use Chrome, Edge or Brave.");
  const [{ default: TransportWebHID }, { default: XrpApp }] = await loaded;
  const t = await TransportWebHID.create();
  t.on("disconnect", () => {
    transport = null;
    app = null;
  });
  transport = t;
  app = new XrpApp(t);
  return app;
}

/** Turns the device's status words into what the user should do. */
function explain(e: unknown) {
  const msg = e instanceof Error ? e.message : String(e);
  if (/0x6985|denied|rejected/i.test(msg)) return new Error("Rejected on the Ledger.");
  if (/0x6e0[01]|0x6511|0x6d00|CLA_NOT_SUPPORTED|INS_NOT_SUPPORTED/i.test(msg)) {
    return new Error("Open the XRP app on the Ledger, then try again.");
  }
  if (/locked|0x5515/i.test(msg)) return new Error("Unlock the Ledger with your PIN, then try again.");
  if (/No device selected|NotFoundError/i.test(msg)) return new Error("No Ledger selected.");
  if (/user gesture/i.test(msg)) return new Error("The browser blocked the device picker; click Connect Ledger again.");
  return new Error(msg);
}

/** Asks the browser for the Ledger and reads its first XRP account. Must run from a click. */
export async function connectLedger(): Promise<LedgerAccount> {
  try {
    const { address, publicKey } = await (await open()).getAddress(LEDGER_PATH);
    return { address, publicKey: publicKey.toUpperCase() };
  } catch (e) {
    await disconnectLedger();
    throw explain(e);
  }
}

export async function disconnectLedger() {
  const t = transport;
  transport = null;
  app = null;
  await t?.close().catch(() => {});
}

async function watcher<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(`/api/watcher/${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
    cache: "no-store",
  });
  const json = (await res.json()) as { ok: boolean; error?: string } & T;
  if (!json.ok) throw new Error(json.error || `${path} failed`);
  return json;
}

/**
 * Signs `tx` on the Ledger as `account` and submits it; resolves with the hash
 * once it is validated. The user confirms the details on the device.
 */
export async function signAndSubmit(account: LedgerAccount, tx: Record<string, unknown>) {
  const prepared = await watcher<{ tx: Record<string, unknown>; txBlob: string }>("hw/prepare", {
    tx,
    account: account.address,
    publicKey: account.publicKey,
  });
  let signature: string;
  try {
    signature = await (await open()).signTransaction(LEDGER_PATH, prepared.txBlob);
  } catch (e) {
    throw explain(e);
  }
  return (await watcher<{ hash: string }>("hw/submit", { tx: prepared.tx, signature })).hash;
}

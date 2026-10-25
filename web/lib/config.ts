import { XRPL_NETWORK } from "./network";

// Browser calls same-origin /api/card (Next proxies to mock POST /card or
// FCC POST /direct when TEE_MODE=fcc).
export const TEE_URL =
  typeof window === "undefined"
    ? process.env.TEE_CARD_URL ||
      process.env.NEXT_PUBLIC_TEE_CARD_URL ||
      "http://127.0.0.1:8080"
    : ""; // unused in browser — see api.ts

export const TEE_BROWSER_PATH = "/api/card";

export const WATCHER_URL =
  process.env.NEXT_PUBLIC_WATCHER_URL || "http://127.0.0.1:8090";
export const CARD_CREDENTIAL =
  process.env.NEXT_PUBLIC_CARD_CREDENTIAL || "swell-demo-card-credential";
export const EXPLORER_TX =
  process.env.NEXT_PUBLIC_XRPL_EXPLORER || XRPL_NETWORK.explorerTx;

export const EXPLORER_ACCOUNT =
  process.env.NEXT_PUBLIC_XRPL_EXPLORER_ACCOUNT || XRPL_NETWORK.explorerAccount;

/** How often pages reload their data in the background; refresh buttons reload at once. */
export const REFRESH_MS = 10_000;

export function xrplAccountUrl(address: string) {
  return `${EXPLORER_ACCOUNT}/${address}`;
}

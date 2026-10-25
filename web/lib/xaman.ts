import { RLUSD, XRPL_NETWORK, SOURCE_TAG } from "./network";

export const XAMAN_POLL_MS = 2500;
export const DROPS_PER_XRP = 1_000_000;

export type XamanCreatedPayload = {
  uuid: string;
  refs: { qr_png: string };
  next?: { always?: string };
  error?: string;
};

export type XamanPayloadStatus = {
  meta?: {
    signed?: boolean;
    resolved?: boolean;
    expired?: boolean;
    cancelled?: boolean;
  };
  response?: {
    account?: string;
    txid?: string;
  };
  error?: string;
};

export type XamanPending = {
  uuid: string;
  qrPng: string;
  deeplink: string;
};

export function xrpToDrops(xrp: string): string {
  // String arithmetic: 1.005 XRP must be 1005000 drops, not a float's 1004999.
  const m = /^(\d+)(?:\.(\d{0,6}))?$/.exec(xrp.trim());
  const drops = m ? BigInt(m[1]) * BigInt(DROPS_PER_XRP) + BigInt((m[2] ?? "").padEnd(6, "0")) : BigInt(0);
  if (drops <= BigInt(0)) {
    throw new Error("Enter a positive XRP amount (up to 6 decimals)");
  }
  return drops.toString();
}

export async function createXamanPayload(
  body: Record<string, unknown>,
): Promise<XamanCreatedPayload> {
  const res = await fetch("/api/xaman/create-payload", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const data = (await res.json()) as XamanCreatedPayload;
  if (!res.ok || !data.uuid) {
    throw new Error(data.error ?? "Failed to create Xaman payload");
  }
  return data;
}

export function pendingFromPayload(payload: XamanCreatedPayload): XamanPending {
  return {
    uuid: payload.uuid,
    qrPng: payload.refs.qr_png,
    deeplink: payload.next?.always ?? `https://xumm.app/sign/${payload.uuid}`,
  };
}

export async function fetchXamanPayload(
  uuid: string,
): Promise<XamanPayloadStatus | null> {
  const res = await fetch(`/api/xaman/payload/${uuid}`);
  if (!res.ok) return null;
  return (await res.json()) as XamanPayloadStatus;
}

/** SignIn → classic address (on the configured network). */
export function signInBody() {
  return {
    txjson: { TransactionType: "SignIn" },
    options: { submit: false, force_network: XRPL_NETWORK.xamanNetwork },
  };
}

/**
 * Collateral deposit: on the ledger an XRP Payment with the destination tag,
 * so Xaman is told what it is (custom_meta.instruction) instead of "Payment".
 */
export function depositPaymentBody(opts: {
  destination: string;
  destinationTag: number;
  amountXrp: string;
}) {
  return {
    txjson: {
      TransactionType: "Payment",
      SourceTag: SOURCE_TAG,
      Destination: opts.destination,
      Amount: xrpToDrops(opts.amountXrp),
      DestinationTag: opts.destinationTag,
    },
    options: { submit: true, force_network: XRPL_NETWORK.xamanNetwork },
    custom_meta: {
      instruction: `Deposit ${opts.amountXrp} XRP as credit card collateral (tag ${opts.destinationTag}).`,
    },
  };
}

/** RLUSD Payment (mock issuer on the configured network). */
export function rlusdPaymentBody(opts: { destination: string; value: string }) {
  return {
    txjson: {
      TransactionType: "Payment",
      SourceTag: SOURCE_TAG,
      Destination: opts.destination,
      Amount: { currency: RLUSD.currency, issuer: RLUSD.issuer, value: opts.value },
    },
    options: { submit: true, force_network: XRPL_NETWORK.xamanNetwork },
  };
}

/** Trust line to the RLUSD issuer, needed before holding or paying RLUSD. */
export function rlusdTrustSetBody() {
  return {
    txjson: {
      TransactionType: "TrustSet",
      SourceTag: SOURCE_TAG,
      LimitAmount: { currency: RLUSD.currency, issuer: RLUSD.issuer, value: "1000000000" },
    },
    options: { submit: true, force_network: XRPL_NETWORK.xamanNetwork },
  };
}

/** Trust line to the LP share token (needed before shares can be received). */
export function shareTrustSetBody(opts: { currency: string; issuer: string }) {
  return {
    txjson: {
      TransactionType: "TrustSet",
      SourceTag: SOURCE_TAG,
      LimitAmount: { currency: opts.currency, issuer: opts.issuer, value: "1000000000000" },
    },
    options: { submit: true, force_network: XRPL_NETWORK.xamanNetwork },
  };
}

/** RLUSD Payment with a destination tag (LP deposit). */
export function rlusdTaggedPaymentBody(opts: { destination: string; destinationTag: number; value: string }) {
  const body = rlusdPaymentBody(opts);
  return { ...body, txjson: { ...body.txjson, DestinationTag: opts.destinationTag } };
}

/** LP shares back to their issuer (a redemption: the tokens are burned). */
export function shareRedeemBody(opts: { currency: string; issuer: string; value: string }) {
  return {
    txjson: {
      TransactionType: "Payment",
      SourceTag: SOURCE_TAG,
      Destination: opts.issuer,
      Amount: { currency: opts.currency, issuer: opts.issuer, value: opts.value },
    },
    options: { submit: true, force_network: XRPL_NETWORK.xamanNetwork },
  };
}

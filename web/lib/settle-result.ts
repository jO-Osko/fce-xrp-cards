"use client";

import { XRPL_ACCOUNTS } from "./fcc";

const KEY = "xrpl-cc-last-settle";

export type SettleResult = {
  rlusdAmount: string;
  txHash: string;
  explorerUrl: string;
  settlementAddress: string;
  wireQueued: boolean;
  /** TEE profit (card fees, interest reserve) paid to the treasury with this settlement */
  treasuryRlusd?: string;
  treasuryTxHash?: string;
  /** Unix seconds when the settlement was recorded */
  at?: number;
  /** TEE instruction / action id for SETTLE_DAILY */
  settleDailyActionId?: string;
  /** TEE instruction / action id for SETTLE_CONFIRM */
  settleConfirmActionId?: string;
  /** Coston2 tx that sent SETTLE_DAILY (FCC) */
  settleDailyFlareTx?: string;
  /** Coston2 tx that sent SETTLE_CONFIRM (FCC) */
  settleConfirmFlareTx?: string;
};

/** The browser's copy of the last settlement, if it belongs to the current settlement address. */
export function loadSettleResult(): SettleResult | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = localStorage.getItem(KEY);
    const r = raw ? (JSON.parse(raw) as SettleResult) : null;
    // A copy from older wallets (regenerated since) is not this ledger's settlement.
    if (r && r.settlementAddress && r.settlementAddress !== XRPL_ACCOUNTS.settlementAddress) {
      clearSettleResult();
      return null;
    }
    return r;
  } catch {
    return null;
  }
}

export function saveSettleResult(r: SettleResult) {
  try {
    localStorage.setItem(KEY, JSON.stringify(r));
  } catch {
    /* storage unavailable */
  }
}

export function clearSettleResult() {
  try {
    localStorage.removeItem(KEY);
  } catch {
    /* storage unavailable */
  }
}

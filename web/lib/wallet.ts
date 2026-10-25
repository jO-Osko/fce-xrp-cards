"use client";

/**
 * Which wallet signs for an address. An address connected through a Ledger
 * signs on the Ledger on every page; any other address signs with Xaman.
 */
import { useCallback, useState } from "react";
import { signAndSubmit, type LedgerAccount } from "@/lib/ledger";
import { useXamanSign } from "@/lib/use-xaman";

const KEY = "xrpl-cc-ledger-accounts";

function load(): Record<string, string> {
  try {
    return JSON.parse(localStorage.getItem(KEY) || "{}") as Record<string, string>;
  } catch {
    return {};
  }
}

function store(map: Record<string, string>) {
  try {
    localStorage.setItem(KEY, JSON.stringify(map));
  } catch {
    /* private window: the Ledger is asked again after a reload */
  }
}

/** Remembers that `account` lives on a Ledger, so every page signs for it there. */
export function rememberLedger(account: LedgerAccount) {
  store({ ...load(), [account.address]: account.publicKey });
}

export function forgetLedger(address: string) {
  const map = load();
  delete map[address];
  store(map);
}

/** The Ledger account behind `address`, or null when it signs with Xaman. */
export function ledgerAccountFor(address: string | null | undefined): LedgerAccount | null {
  if (!address) return null;
  const publicKey = load()[address];
  return publicKey ? { address, publicKey } : null;
}

/**
 * sign(address, body) submits `body.txjson` as `address`, on the Ledger when the
 * address is one, otherwise as a Xaman payload; resolves with the transaction hash.
 * `onLedger` is true while the Ledger waits for the user to confirm.
 */
export function useSigner() {
  const xaman = useXamanSign();
  const [onLedger, setOnLedger] = useState(false);
  const { sign: xamanSign } = xaman;
  const sign = useCallback(
    async (address: string, body: { txjson: Record<string, unknown> }) => {
      const ledger = ledgerAccountFor(address);
      if (!ledger) return xamanSign(body);
      setOnLedger(true);
      try {
        return await signAndSubmit(ledger, body.txjson);
      } finally {
        setOnLedger(false);
      }
    },
    [xamanSign],
  );
  return { sign, xaman, onLedger };
}

"use client";

import { useEffect, useState } from "react";
import { connectLedger, ledgerSupported, preloadLedger, type LedgerAccount } from "@/lib/ledger";
import { rememberLedger } from "@/lib/wallet";

/**
 * "Connect Ledger": reads the device's first XRP account and remembers it, so
 * every page signs for that address on the Ledger. Errors go to `onError`.
 */
export function LedgerButton({
  onConnected,
  onError,
  disabled,
  className = "secondary",
  label = "Connect Ledger",
}: {
  onConnected: (account: LedgerAccount) => void | Promise<void>;
  onError: (message: string) => void;
  disabled?: boolean;
  className?: string;
  label?: string;
}) {
  const [busy, setBusy] = useState(false);
  const [supported, setSupported] = useState(true);

  useEffect(() => {
    setSupported(ledgerSupported());
    preloadLedger();
  }, []);

  return (
    <button
      type="button"
      className={className}
      disabled={disabled || busy || !supported}
      title={supported ? "Ledger with the XRP app open" : "Needs Chrome, Edge or Brave (WebHID)"}
      onClick={async () => {
        setBusy(true);
        try {
          const account = await connectLedger();
          rememberLedger(account);
          await onConnected(account);
        } catch (e) {
          onError(e instanceof Error ? e.message : String(e));
        } finally {
          setBusy(false);
        }
      }}
    >
      {busy ? "Confirm on your Ledger…" : label}
    </button>
  );
}

/** The note shown while a transaction waits on the Ledger. */
export function LedgerPrompt({ active }: { active: boolean }) {
  if (!active) return null;
  return <p className="okmsg">Check your Ledger: review the transaction on the device and confirm it.</p>;
}

"use client";

import { useCallback, useEffect, useState } from "react";
import { LedgerButton, LedgerPrompt } from "@/components/ledger-button";
import { XamanQr } from "@/components/xaman-qr";
import { EXPLORER_TX, xrplAccountUrl, REFRESH_MS } from "@/lib/config";
import { fetchRlusdAccount, mintUserRlusd, type RlusdAccount } from "@/lib/loans";
import { NETWORK_NAME, RLUSD } from "@/lib/network";
import { loadSession } from "@/lib/session";
import { ledgerAccountFor, useSigner } from "@/lib/wallet";
import { loadLPSession } from "@/lib/vault";
import { rlusdTrustSetBody } from "@/lib/xaman";

const FAUCET_RLUSD = "5000";
const XRP_FAUCETS = "https://xrpl.org/resources/dev-tools/xrp-faucets";

const isAddress = (s: string) => /^r[1-9A-HJ-NP-Za-km-z]{24,34}$/.test(s);
const fmt = (s: string, max = 2) => Number(s).toLocaleString(undefined, { maximumFractionDigits: max });

export default function FaucetsPage() {
  const [address, setAddress] = useState("");
  const [connected, setConnected] = useState<string | null>(null);
  const [account, setAccount] = useState<RlusdAccount | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<{ text: string; tx?: string } | null>(null);
  const { sign, xaman, onLedger } = useSigner();

  // Prefill with the card wallet, or else the liquidity wallet.
  useEffect(() => {
    const a = loadSession()?.xrplAddress || loadLPSession()?.xrplAddress || null;
    setConnected(a);
    if (a) setAddress(a);
  }, []);

  const target = address.trim();
  const valid = isAddress(target);

  const refresh = useCallback(async () => {
    if (!valid) {
      setAccount(null);
      return;
    }
    try {
      setAccount(await fetchRlusdAccount(target));
    } catch (e) {
      setAccount(null);
      const msg = e instanceof Error ? e.message : String(e);
      setError(`Balance lookup failed (${msg}). Restart the watcher if it predates this page.`);
    }
  }, [target, valid]);

  useEffect(() => {
    setError(null);
    refresh();
    const id = setInterval(refresh, REFRESH_MS);
    return () => clearInterval(id);
  }, [refresh]);

  async function run(label: string, fn: () => Promise<{ text: string; tx?: string }>) {
    setBusy(label);
    setError(null);
    setMessage(null);
    try {
      setMessage(await fn());
      await refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  return (
    <>
      <section className="panel">
        <h2>Faucets</h2>
        <p className="lead">
          Test funds for {NETWORK_NAME}. Fund the account with XRP first; it then needs a
          trust line to the demo RLUSD issuer before it can receive RLUSD.
        </p>

        <label className="field">
          Address
          <input
            className="mono"
            value={address}
            onChange={(e) => setAddress(e.target.value)}
            placeholder="r…"
            spellCheck={false}
            disabled={!!busy}
          />
        </label>
        <div className="row">
          {connected && target !== connected && (
            <button type="button" className="secondary" disabled={!!busy} onClick={() => setAddress(connected)}>
              Use connected wallet
            </button>
          )}
          <LedgerButton
            label="Use my Ledger"
            disabled={!!busy}
            onError={setError}
            onConnected={(acct) => {
              setError(null);
              setAddress(acct.address);
            }}
          />
          {ledgerAccountFor(target) ? (
            <span className="muted">Signs on your Ledger</span>
          ) : connected ? (
            <span className="muted">{target === connected ? "Your connected wallet" : "A custom address"}</span>
          ) : (
            <span className="muted">No wallet connected; paste any address.</span>
          )}
          {valid && (
            <a className="muted" href={xrplAccountUrl(target)} target="_blank" rel="noopener noreferrer">
              View on the explorer
            </a>
          )}
        </div>
        {target && !valid && <p className="err">Not a classic XRPL address.</p>}

        {valid && account && (
          <div className="grid grid-3">
            <div className="stat">
              <label>XRP</label>
              <strong>{account.funded ? fmt(account.xrp) : "—"}</strong>
              <span className="stat-sub">{account.funded ? "account exists" : "not on the ledger yet"}</span>
            </div>
            <div className="stat">
              <label>RLUSD trust line</label>
              <strong>{account.trustLine ? "Set" : "Missing"}</strong>
              <span className="stat-sub mono">issuer {RLUSD.issuer.slice(0, 10)}…</span>
            </div>
            <div className="stat">
              <label>RLUSD</label>
              <strong>{fmt(account.rlusd, 6)}</strong>
              <span className="stat-sub">demo RLUSD</span>
            </div>
          </div>
        )}
        {error && <p className="err">{error}</p>}
        {message && (
          <p className="okmsg">
            {message.text}
            {message.tx && (
              <>
                {" "}
                <a className="mono" href={`${EXPLORER_TX}/${message.tx}`} target="_blank" rel="noopener noreferrer">
                  {message.tx.slice(0, 8)}…
                </a>
              </>
            )}
          </p>
        )}
        {xaman.pending && <XamanQr pending={xaman.pending} onCancel={xaman.cancel} />}
        <LedgerPrompt active={onLedger} />
      </section>

      <section className="panel">
        <h2>1. XRP</h2>
        <p className="lead">
          XRP comes from the public XRPL faucets. Pick <strong>{NETWORK_NAME}</strong> there (this
          demo runs on it), paste the address above, and it gets new test XRP; a new address is
          created on the ledger with it.
        </p>
        <div className="row">
          <a className="btn" href={XRP_FAUCETS} target="_blank" rel="noopener noreferrer">
            Open the XRP faucets ↗
          </a>
          {valid && (
            <button type="button" className="secondary" onClick={() => navigator.clipboard?.writeText(target)}>
              Copy address
            </button>
          )}
        </div>
      </section>

      <section className="panel">
        <h2>2. RLUSD</h2>
        <p className="lead">
          {NETWORK_NAME} has no real RLUSD, so the demo mints its own from a mock issuer. Add the
          trust line once (signed by the wallet that owns the address: its Ledger, or Xaman), then take{" "}
          {Number(FAUCET_RLUSD).toLocaleString()} RLUSD as often as you need.
        </p>
        <div className="row">
          <button
            type="button"
            className="secondary"
            disabled={!!busy || !valid || account?.funded === false || account?.trustLine === true}
            onClick={() =>
              run("trust", async () => {
                const tx = await sign(target, rlusdTrustSetBody());
                return { text: "RLUSD trust line set", tx };
              })
            }
          >
            {account?.trustLine ? "RLUSD trust line ✓" : busy === "trust" ? "Signing…" : "Add RLUSD trust line"}
          </button>
          <button
            type="button"
            disabled={!!busy || !valid || account?.trustLine === false}
            onClick={() =>
              run("mint", async () => {
                const r = await mintUserRlusd(target, FAUCET_RLUSD);
                return { text: `Sent ${Number(r.amount).toLocaleString()} RLUSD to ${target.slice(0, 8)}…`, tx: r.txHash };
              })
            }
          >
            {busy === "mint" ? "Minting…" : `Get ${Number(FAUCET_RLUSD).toLocaleString()} RLUSD`}
          </button>
        </div>
        {valid && account && !account.funded && <p className="muted">Fund the address with XRP first (step 1).</p>}
        {valid && account?.funded && !account.trustLine && (
          <p className="muted">
            The trust line must be signed by the owner of {target.slice(0, 8)}…; {ledgerAccountFor(target) ? "confirm it on the Ledger." : "scan the Xaman QR with that wallet."}
          </p>
        )}
      </section>
    </>
  );
}

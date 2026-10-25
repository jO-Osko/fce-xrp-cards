"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { Meter } from "@/components/meter";
import { LedgerButton, LedgerPrompt } from "@/components/ledger-button";
import { Collapsible } from "@/components/collapsible";
import { downloadCsv, isoTime, today } from "@/lib/csv";
import { XamanQr } from "@/components/xaman-qr";
import { EXPLORER_TX, xrplAccountUrl, REFRESH_MS } from "@/lib/config";
import { NETWORK_NAME } from "@/lib/network";
import { forgetLedger, useSigner } from "@/lib/wallet";
import {
  fetchVault,
  loadLPSession,
  lpPosition,
  lpRegister,
  pct,
  saveLPSession,
  sharesFromUnits,
  type LPJob,
  type Vault,
} from "@/lib/vault";
import {
  rlusdTaggedPaymentBody,
  shareRedeemBody,
  shareTrustSetBody,
  signInBody,
} from "@/lib/xaman";
import deployment from "@/lib/deployment.gen.json";

const SHARE_CODE = deployment.vault.shareCode || "share";

// Shares move in units of 0.000001; anything finer would be burned without payout.
const validShares = (s: string) => /^\d+(\.\d{1,6})?$/.test(s.trim()) && Number(s) >= 0.000001;

const fmt = (n: number, max = 2) => n.toLocaleString(undefined, { maximumFractionDigits: max });

function when(at: number) {
  return new Date(at * 1000).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}

type Row = { key: string; at: number; label: string; detail: string; amount: string; tx?: string; status?: { text: string; tone: string } };

export default function LiquidityPage() {
  const [lp, setLP] = useState<{ xrplAddress: string } | null>(null);
  const [vault, setVault] = useState<Vault | null>(null);
  const [tag, setTag] = useState<number | null>(null);
  const [holding, setHolding] = useState<{ authorized: boolean; shares: string; jobs: LPJob[] } | null>(null);
  const [depositAmount, setDepositAmount] = useState("100");
  const [withdrawShares, setWithdrawShares] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const { sign, xaman, onLedger } = useSigner();

  const refresh = useCallback(async () => {
    const s = loadLPSession();
    setLP(s);
    try {
      setVault(await fetchVault(s?.xrplAddress));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
    if (s) {
      try {
        setHolding(await lpPosition(s.xrplAddress));
      } catch {
        /* watcher down: keep the last view */
      }
    }
  }, []);

  useEffect(() => {
    refresh();
    const id = setInterval(refresh, REFRESH_MS);
    return () => clearInterval(id);
  }, [refresh]);

  // The deposit tag is reserved once per address (LP_REGISTER is idempotent).
  useEffect(() => {
    if (!lp) return;
    lpRegister(lp.xrplAddress)
      .then((r) => setTag(r.tag))
      .catch((e) => setError(e instanceof Error ? e.message : String(e)));
  }, [lp]);

  async function run(label: string, fn: () => Promise<string>) {
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

  const connect = () =>
    run("connect", async () => {
      const account = await xaman.signIn(signInBody());
      forgetLedger(account);
      saveLPSession({ xrplAddress: account });
      setLP({ xrplAddress: account });
      return `Connected ${account}`;
    });

  const price = Number(vault?.sharePrice || 1);
  const shares = sharesFromUnits(holding?.shares || "0");
  const value = shares * price;
  const pendingIssue = (holding?.jobs ?? []).filter((j) => j.kind === "issue" && j.status !== "done");

  const history: Row[] = [
    ...(vault?.position?.deposits ?? []).map((d) => {
      const job = holding?.jobs.find((j) => j.sourceTx === d.txHash);
      return {
        key: `d-${d.txHash}`,
        at: d.at,
        label: "Deposit",
        detail: `${fmt(sharesFromUnits(d.shares), 6)} shares`,
        amount: `+${d.rlusdAmount} RLUSD`,
        tx: d.txHash,
        status: job?.status === "done" ? { text: "shares sent", tone: "ok" } : { text: "shares pending", tone: "warn" },
      };
    }),
    ...(vault?.position?.redemptions ?? []).map((r) => {
      const job = holding?.jobs.find((j) => j.sourceTx === r.txHash);
      return {
        key: `r-${r.txHash}`,
        at: r.at,
        label: "Withdrawal",
        detail: `${fmt(sharesFromUnits(r.shares), 6)} shares`,
        amount: `−${r.rlusdAmount} RLUSD`,
        tx: r.txHash,
        status: job?.status === "done" ? { text: "paid", tone: "ok" } : { text: "payout pending", tone: "warn" },
      };
    }),
    ...(holding?.jobs ?? [])
      .filter((j) => j.kind === "return")
      .map((j) => ({
        key: `x-${j.id}`,
        at: j.createdAt,
        label: "Withdrawal reverted",
        detail: j.reason || "shares returned",
        amount: `${fmt(sharesFromUnits(j.shares || "0"), 6)} shares back`,
        tx: j.sourceTx,
        status: j.status === "done" ? { text: "returned", tone: "bad" } : { text: "returning", tone: "warn" },
      })),
  ].sort((a, b) => b.at - a.at);

  return (
    <>
      <section className="panel">
        <h2>Liquidity provisioning</h2>
        <p className="lead">
          Lend RLUSD to the card&apos;s lending pool inside the TEE and earn the interest cardholders pay.
          There is one pool: the operator&apos;s own liquidity and LPs&apos; liquidity are mixed in it, and both
          hold the same shares, a trust-line token ({SHARE_CODE}) on {NETWORK_NAME}. Shares grow in value as interest accrues; send them
          back to withdraw, like a Morpho vault.
        </p>
        {vault && (
          <div className="grid grid-3">
            <div className="stat">
              <label>Pool liquidity</label>
              <strong>{fmt(Number(vault.totalAssetsRlusd), 6)} RLUSD</strong>
              <span className="stat-sub">
                {fmt(Number(vault.internalLiquidityRlusd), 6)} operator · {fmt(Number(vault.lpLiquidityRlusd), 6)} LPs
              </span>
            </div>
            <div className="stat">
              <label>Available to withdraw</label>
              <strong>{fmt(Number(vault.freeLiquidityRlusd), 6)} RLUSD</strong>
              <span className="stat-sub">free cash, after what merchants and the treasury are owed</span>
            </div>
            <div className="stat">
              <label>Utilization</label>
              <strong>{pct(vault.utilizationBps)}</strong>
              <Meter ratio={vault.utilizationBps / 10000} label="Pool utilization" />
              <span className="stat-sub">{fmt(Number(vault.cardDebtRlusd), 6)} RLUSD lent to cardholders</span>
            </div>
            <div className="stat">
              <label>Supply APR</label>
              <strong>{pct(vault.supplyAprBps)}</strong>
              <span className="stat-sub">
                borrow {pct(vault.borrowAprBps)} × utilization × (1 − {pct(vault.reserveFactorBps)} TEE cut)
              </span>
            </div>
            <div className="stat">
              <label>Share price</label>
              <strong>{vault.sharePrice} RLUSD</strong>
              <span className="stat-sub">{fmt(sharesFromUnits(vault.totalShares))} shares outstanding</span>
            </div>
            <div className="stat">
              <label>Interest earned by LPs</label>
              <strong>{vault.interestToLpsRlusd} RLUSD</strong>
              <span className="stat-sub">{vault.lpCount} liquidity providers</span>
            </div>
          </div>
        )}
      </section>

      <section className="panel">
        <div className="row" style={{ justifyContent: "space-between" }}>
          <h2>Your position</h2>
          {lp ? (
            <button type="button" className="secondary" onClick={() => { saveLPSession(null); setLP(null); setHolding(null); setTag(null); }}>
              Disconnect
            </button>
          ) : (
            <div className="row">
              <button type="button" disabled={!!busy} onClick={connect}>
                {busy === "connect" ? "Waiting for Xaman…" : "Connect Xaman"}
              </button>
              <LedgerButton
                disabled={!!busy}
                onError={setError}
                onConnected={(acct) => {
                  setError(null);
                  saveLPSession({ xrplAddress: acct.address });
                  setLP({ xrplAddress: acct.address });
                  setMessage(`Ledger connected: ${acct.address}`);
                }}
              />
            </div>
          )}
        </div>
        {error && <p className="err">{error}</p>}
        {message && <p className="okmsg">{message}</p>}
        {xaman.pending && <XamanQr pending={xaman.pending} onCancel={xaman.cancel} />}
        <LedgerPrompt active={onLedger} />

        {!lp && <p className="empty-note">Connect a wallet to deposit. This is separate from the card account.</p>}
        {lp && vault && (
          <>
            <p className="lead mono account-id">
              <a className="addr-link" href={xrplAccountUrl(lp.xrplAddress)} target="_blank" rel="noopener noreferrer">
                {lp.xrplAddress}
              </a>
            </p>
            <div className="grid grid-3">
              <div className="stat">
                <label>Your shares</label>
                <strong>{fmt(shares, 6)}</strong>
                <span className="stat-sub">{holding?.authorized ? `${SHARE_CODE} trust line set` : `no ${SHARE_CODE} trust line yet`}</span>
              </div>
              <div className="stat">
                <label>Worth</label>
                <strong>{fmt(value, 6)} RLUSD</strong>
                <span className="stat-sub">at {vault.sharePrice} RLUSD per share</span>
              </div>
              <div className="stat">
                <label>Deposit to</label>
                <a className="mono addr-link" href={xrplAccountUrl(vault.lpDepositAccount)} target="_blank" rel="noopener noreferrer">
                  {vault.lpDepositAccount.slice(0, 12)}…
                </a>
                <span className="stat-sub">destination tag {tag ?? "…"}</span>
              </div>
            </div>
            {pendingIssue.length > 0 && (
              <p className="err">
                {pendingIssue.length} share issue{pendingIssue.length > 1 ? "s" : ""} waiting
                {holding?.authorized ? " for the watcher's next run" : `: add the ${SHARE_CODE} trust line to receive them`}.
              </p>
            )}

            <h3 className="section-label">1. Get set up (once)</h3>
            <div className="row">
              <button
                type="button"
                className="secondary"
                disabled={!!busy || holding?.authorized}
                onClick={() => run("optin", async () => {
                  const tx = await sign(lp.xrplAddress, shareTrustSetBody({ currency: vault.shareCurrency, issuer: vault.lpDepositAccount }));
                  return `Trust line to ${SHARE_CODE} set (${tx.slice(0, 8)}…)`;
                })}
              >
                {holding?.authorized ? `${SHARE_CODE} trust line ✓` : busy === "optin" ? "Signing…" : `Add ${SHARE_CODE} trust line`}
              </button>
              <Link className="muted" href="/faucets">
                Need RLUSD? Faucets →
              </Link>
            </div>

            <h3 className="section-label">2. Deposit RLUSD</h3>
            <div className="row">
              <input type="number" min="0" step="0.01" value={depositAmount} onChange={(e) => setDepositAmount(e.target.value)} disabled={!!busy} />
              <button
                type="button"
                disabled={!!busy || !tag || !(Number(depositAmount) > 0)}
                onClick={() => run("deposit", async () => {
                  const tx = await sign(lp.xrplAddress, rlusdTaggedPaymentBody({ destination: vault.lpDepositAccount, destinationTag: tag!, value: depositAmount }));
                  return `Deposited ${depositAmount} RLUSD (${tx.slice(0, 8)}…); shares arrive in a few seconds`;
                })}
              >
                {busy === "deposit" ? "Signing…" : "Deposit"}
              </button>
              <span className="muted">
                ≈ {fmt(Number(depositAmount || 0) / price, 6)} shares
              </span>
            </div>

            <h3 className="section-label">3. Withdraw</h3>
            <div className="row">
              <input type="number" min="0" step="0.000001" placeholder={`max ${fmt(shares, 6)} shares`} value={withdrawShares} onChange={(e) => setWithdrawShares(e.target.value)} disabled={!!busy} />
              <button type="button" className="secondary" disabled={!!busy || shares <= 0} onClick={() => setWithdrawShares(String(shares))}>
                Max
              </button>
              <button
                type="button"
                disabled={!!busy || !validShares(withdrawShares) || Number(withdrawShares) > shares}
                onClick={() => run("withdraw", async () => {
                  const tx = await sign(lp.xrplAddress, shareRedeemBody({ currency: vault.shareCurrency, issuer: vault.lpDepositAccount, value: withdrawShares }));
                  return `Sent ${withdrawShares} shares back (${tx.slice(0, 8)}…); RLUSD arrives in a few seconds`;
                })}
              >
                {busy === "withdraw" ? "Signing…" : "Withdraw"}
              </button>
              <span className="muted">≈ {fmt(Number(withdrawShares || 0) * price, 6)} RLUSD</span>
            </div>
          </>
        )}
      </section>

      {lp && (
        <section className="panel">
          <Collapsible
            title={<h2>History</h2>}
            count={history.length}
            actions={
              <button
                type="button"
                className="secondary"
                disabled={history.length === 0}
                onClick={() =>
                  downloadCsv(
                    `xrpl-card-lp-history-${lp.xrplAddress}-${today()}.csv`,
                    ["Time (UTC)", "Type", "Detail", "Amount", "Status", "Transaction"],
                    history.map((h) => [isoTime(h.at), h.label, h.detail, h.amount.replace(/^\+/, ""), h.status?.text ?? "", h.tx ?? ""]),
                  )
                }
              >
                Export CSV
              </button>
            }
          >
          {history.length === 0 ? (
            <p className="empty-note">No deposits or withdrawals yet.</p>
          ) : (
            <ul className="tx-list">
              {history.map((h) => (
                <li key={h.key} className="tx-row activity-row">
                  <time>{when(h.at)}</time>
                  <span className="activity-main">
                    <span className="kind-label">{h.label}</span>
                    <span>{h.detail}</span>
                  </span>
                  <span>
                    {h.status && <span className={`badge ${h.status.tone}`}>{h.status.text}</span>}{" "}
                    {h.tx && (
                      <a className="mono muted" href={`${EXPLORER_TX}/${h.tx}`} target="_blank" rel="noopener noreferrer">
                        {h.tx.slice(0, 8)}…
                      </a>
                    )}
                  </span>
                  <strong>{h.amount}</strong>
                </li>
              ))}
            </ul>
          )}
          </Collapsible>
        </section>
      )}
    </>
  );
}

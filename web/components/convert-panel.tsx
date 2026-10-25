"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { dropsToXrp } from "@/lib/api";
import { amount as fmt, when } from "@/lib/format";
import { Collapsible } from "@/components/collapsible";
import { EXPLORER_TX, xrplAccountUrl, REFRESH_MS } from "@/lib/config";
import {
  listConversions,
  resumeConversion,
  startConversion,
  type Conversion,
} from "@/lib/loans";

export type Repayment = {
  txHash: string;
  rlusdAmount: string;
  rlusdCredited: string;
  rlusdExcess?: string;
  xrpUnlockedDrops: string;
  at: number;
};

type Props = {
  tag: number;
  rlusdDebt: string;
  convertibleRlusd: string;
  repayments: Repayment[];
  onChanged: () => void;
};

const STEPS: Array<{ status: Conversion["status"]; label: string }> = [
  { status: "requested", label: "TEE reserved the debt" },
  { status: "creating-account", label: "Repayment account (1-of-2: you + Credit Server)" },
  { status: "broker-approval", label: "Loan broker approved" },
  { status: "taking-loan", label: "XLS-66 loan taken" },
  { status: "repaying-tee", label: "Principal repaid to the pool with your tag" },
];
const ORDER = [...STEPS.map((s) => s.status), "repaid-to-pool"];

function stepState(conv: Conversion, i: number, credited: boolean) {
  if (conv.status === "repaid-to-pool") return "ok";
  const at = ORDER.indexOf(conv.status);
  if (conv.status === "failed") {
    // Infer how far it got from what it recorded.
    const done = [true, !!conv.repaymentAccount, !!conv.brokerApproved, !!conv.loanId, !!conv.poolPaymentTx];
    if (done[i]) return "ok";
    return done[i - 1] ? "bad" : "todo";
  }
  if (i < at) return "ok";
  if (i === at) return "warn";
  return credited ? "ok" : "todo";
}

function TxLink({ hash }: { hash?: string }) {
  if (!hash) return null;
  return (
    <a className="mono muted" href={`${EXPLORER_TX}/${hash}`} target="_blank" rel="noopener noreferrer">
      {hash.slice(0, 8)}…
    </a>
  );
}

export function ConvertPanel({ tag, rlusdDebt, convertibleRlusd, repayments, onChanged }: Props) {
  const [amount, setAmount] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [latest, setLatest] = useState<Conversion | null>(null);

  const refresh = useCallback(async () => {
    try {
      setLatest((await listConversions(tag))[0] ?? null);
    } catch {
      /* watcher down: keep the last view */
    }
  }, [tag]);

  useEffect(() => {
    refresh();
    const id = setInterval(refresh, busy ? 1500 : REFRESH_MS);
    return () => clearInterval(id);
  }, [refresh, busy]);

  async function run(fn: () => Promise<Conversion>) {
    setBusy(true);
    setError(null);
    try {
      const conv = await fn();
      setLatest(conv);
      if (conv.status === "failed") setError(conv.error || "conversion failed");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
      onChanged();
    }
  }

  const credited = !!latest?.poolPaymentTx && repayments.some((r) => r.txHash === latest.poolPaymentTx);
  const canConvert = Number(convertibleRlusd) > 0;
  // A conversion that finished a while ago is one line, not the full step list.
  const settled = !!latest && latest.status === "repaid-to-pool" && Date.now() / 1000 - latest.createdAt > 600;

  return (
    <div className="tx-block">
      <h3>Convert to an XLS-66 loan</h3>
      <p className="lead">
        Turn card debt into a regular XRPL loan with monthly payments. The loan pays your
        debt back to the pool under your tag; the TEE sees that repayment and unlocks
        the matching XRP collateral. If the repayment never arrives, nothing changes.
      </p>
      <div className="row">
        <input
          type="number"
          min="0"
          step="0.01"
          placeholder={`all ${fmt(convertibleRlusd)} RLUSD`}
          value={amount}
          onChange={(e) => setAmount(e.target.value)}
          disabled={busy || !canConvert}
        />
        <button type="button" disabled={busy || !canConvert} onClick={() => run(() => startConversion(tag, amount))}>
          {busy ? "Converting…" : "Convert"}
        </button>
        <span className="muted">
          Debt {fmt(rlusdDebt)} RLUSD · convertible {fmt(convertibleRlusd)} RLUSD
        </span>
      </div>
      {error && <p className="err">{error}</p>}

      {latest && settled && (
        <p className="lead">
          Last conversion: {fmt(latest.rlusdAmount)} RLUSD on {when(latest.createdAt)}, loan <TxLink hash={latest.loanSetTx} />
        </p>
      )}
      {latest && !settled && (
        <ul className="tx-list" style={{ marginTop: "1rem" }}>
          {STEPS.map((s, i) => {
            const state = stepState(latest, i, credited);
            return (
              <li key={s.status} className="tx-row">
                <span className={`badge ${state === "todo" ? "" : state}`}>
                  {state === "ok" ? "done" : state === "warn" ? "running" : state === "bad" ? "failed" : "—"}
                </span>
                <span>{s.label}</span>
                {i === 1 && latest.repaymentAccount && (
                  <a className="mono muted" href={xrplAccountUrl(latest.repaymentAccount)} target="_blank" rel="noopener noreferrer">
                    {latest.repaymentAccount.slice(0, 8)}…{latest.accountReused ? " (reused)" : ""}
                  </a>
                )}
                {i === 3 && <TxLink hash={latest.loanSetTx} />}
                {i === 4 && <TxLink hash={latest.poolPaymentTx} />}
                {i === 0 && <strong>{fmt(latest.rlusdAmount)} RLUSD</strong>}
              </li>
            );
          })}
          <li className="tx-row">
            <span className={`badge ${credited ? "ok" : ""}`}>{credited ? "done" : "—"}</span>
            <span>TEE credited the repayment and unlocked collateral</span>
          </li>
        </ul>
      )}
      {latest?.status === "failed" && (
        <div className="row" style={{ marginTop: "0.5rem" }}>
          <span className="err">{latest.error}</span>
          <button type="button" className="secondary" disabled={busy} onClick={() => run(() => resumeConversion(latest.id))}>
            Resume
          </button>
        </div>
      )}
      {latest?.loanId && (
        <p className="lead">
          Repay it on the <Link href="/loans">Loans</Link> page.
        </p>
      )}

      <div style={{ marginTop: "1.5rem" }}>
      <Collapsible title="Debt repayments" count={repayments.length}>
      {repayments.length === 0 ? (
        <p className="empty-note">No repayments yet.</p>
      ) : (
        <ul className="tx-list">
          {repayments.map((r) => (
            <li key={r.txHash} className="tx-row">
              <time>{when(r.at)}</time>
              <TxLink hash={r.txHash} />
              <span className="muted">unlocked {dropsToXrp(r.xrpUnlockedDrops)} XRP</span>
              <strong>
                {fmt(r.rlusdCredited)} RLUSD{r.rlusdExcess ? ` (+${fmt(r.rlusdExcess)} excess)` : ""}
              </strong>
            </li>
          ))}
        </ul>
      )}
      </Collapsible>
      </div>
    </div>
  );
}

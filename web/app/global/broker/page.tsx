"use client";

import { useEffect, useState } from "react";
import { xrplAccountUrl, REFRESH_MS } from "@/lib/config";
import { amount, when } from "@/lib/format";
import { Collapsible } from "@/components/collapsible";
import deployment from "@/lib/deployment.gen.json";
import { fetchBrokerLog, type BrokerLogEntry } from "@/lib/loans";
import { NETWORK_NAME } from "@/lib/network";

const L = deployment.lending;

function days(seconds: number) {
  return `${Math.round(seconds / 86400)} days`;
}

export default function BrokerPage() {
  const [log, setLog] = useState<BrokerLogEntry[]>([]);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const load = () =>
      fetchBrokerLog()
        .then((rows) => {
          setLog(rows);
          setError(null);
        })
        .catch((e) => setError(e instanceof Error ? e.message : String(e)));
    load();
    const id = setInterval(load, REFRESH_MS);
    return () => clearInterval(id);
  }, []);

  return (
    <>
      <section className="panel">
        <h2>Mock loan broker</h2>
        <p className="lead">
          Stands in for an outside XLS-66 loan broker on {NETWORK_NAME}. It countersigns
          a loan only when it matches a conversion the TEE issued, for that user&apos;s
          repayment account and the standard terms below, and logs every decision. A
          real broker would review each loan; this one approves automatically.
        </p>
        <div className="grid">
          <div className="stat">
            <label>Broker</label>
            {L.broker ? (
              <a className="mono addr-link" href={xrplAccountUrl(L.broker)} target="_blank" rel="noopener noreferrer">
                {L.broker}
              </a>
            ) : (
              <strong>not set up</strong>
            )}
          </div>
          <div className="stat">
            <label>Interest</label>
            <strong>{(L.terms.interestRate / 1000).toFixed(1)}% a year</strong>
          </div>
          <div className="stat">
            <label>Schedule</label>
            <strong>
              {L.terms.paymentTotal} × {days(L.terms.paymentInterval)}
            </strong>
          </div>
          <div className="stat">
            <label>Grace period</label>
            <strong>{days(L.terms.gracePeriod)}</strong>
          </div>
          <div className="stat">
            <label>Loan broker id</label>
            <span className="mono" title={L.loanBrokerId}>
              {L.loanBrokerId ? `${L.loanBrokerId.slice(0, 12)}…` : "—"}
            </span>
          </div>
          <div className="stat">
            <label>Vault id</label>
            <span className="mono" title={L.vaultId}>
              {L.vaultId ? `${L.vaultId.slice(0, 12)}…` : "—"}
            </span>
          </div>
        </div>
      </section>

      <section className="panel">
        {error && <p className="err">{error}</p>}
        <Collapsible title={<h2>Decisions</h2>} count={log.length}>
        {log.length === 0 ? (
          <p className="empty-note">No loan requests yet.</p>
        ) : (
          <ul className="tx-list">
            {log.map((e, i) => (
              <li key={`${e.conversionId}-${e.at}-${i}`} className="tx-row">
                <time>{when(e.at)}</time>
                <span className={`badge ${e.approved ? "ok" : "bad"}`}>{e.approved ? "approved" : "refused"}</span>
                <span className="muted">
                  tag {e.tag} · {e.reason || `borrower ${e.borrower.slice(0, 8)}…`}
                </span>
                <strong>{amount(e.principal)} RLUSD</strong>
              </li>
            ))}
          </ul>
        )}
        </Collapsible>
      </section>
    </>
  );
}

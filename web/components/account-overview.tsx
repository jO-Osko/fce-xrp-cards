"use client";

import Link from "next/link";
import { useCallback, useEffect, useMemo, useState } from "react";
import type { CardPayment } from "@/components/payments";
import type { Repayment } from "@/components/convert-panel";
import { dropsToXrp, teeCard } from "@/lib/api";
import { EXPLORER_TX, xrplAccountUrl, REFRESH_MS } from "@/lib/config";
import { usd } from "@/lib/format";
import { Collapsible } from "@/components/collapsible";
import { downloadCsv, isoTime, today } from "@/lib/csv";
import deployment from "@/lib/deployment.gen.json";
import { shortHex, teeDirectActionUrl } from "@/lib/fcc";
import { attachFlareActions } from "@/lib/flare-actions";
import {
  fetchLoanPayments,
  fetchLoans,
  installmentAmount,
  listConversions,
  type Conversion,
  type Loan,
  type LoanPayment,
} from "@/lib/loans";
import { loadSession, type Session } from "@/lib/session";
import { Meter } from "@/components/meter";

type Position = {
  tag: number;
  xrplAddress: string;
  xrpAvailableDrops: string;
  xrpLockedDrops: string;
  rlusdDebt: string;
  convertibleRlusd?: string;
  borrowAprBps?: number;
  xrpUsdPrice: string;
  ltvCapBps: number;
  depositAccount: string;
  deposits: Array<{ txHash: string; drops: string; at: number }>;
  payments: CardPayment[];
  pendingConversions?: Array<{ id: string; rlusdAmount: string; expires: number }>;
  repayments?: Repayment[];
};

type Kind = "deposit" | "card" | "conversion" | "repayment" | "installment";

const KIND_LABEL: Record<Kind, string> = {
  deposit: "Collateral",
  card: "Card",
  conversion: "Conversion",
  repayment: "Repayment",
  installment: "Loan payment",
};

type Activity = {
  key: string;
  at: number;
  kind: Kind;
  title: string;
  link?: { href: string; text: string; title?: string };
  status?: { text: string; tone: "ok" | "warn" | "bad" };
  amount: string;
};

const num = (s: string | undefined) => {
  const n = Number(s);
  return Number.isFinite(n) ? n : 0;
};

function fmt(n: number, max = 2) {
  return n.toLocaleString(undefined, { maximumFractionDigits: max });
}

function when(at: number) {
  if (!at) return "—";
  return new Date(at * 1000).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function txLink(hash?: string) {
  return hash ? { href: `${EXPLORER_TX}/${hash}`, text: `${hash.slice(0, 8)}…` } : undefined;
}

const CONVERSION_STATUS: Record<Conversion["status"], { text: string; tone: "ok" | "warn" | "bad" }> = {
  requested: { text: "reserved", tone: "warn" },
  "creating-account": { text: "setting up account", tone: "warn" },
  "broker-approval": { text: "awaiting broker", tone: "warn" },
  "taking-loan": { text: "taking loan", tone: "warn" },
  "repaying-tee": { text: "repaying TEE", tone: "warn" },
  "repaid-to-pool": { text: "converted", tone: "ok" },
  failed: { text: "failed", tone: "bad" },
};

function buildActivity(
  pos: Position,
  conversions: Conversion[],
  installments: LoanPayment[],
): Activity[] {
  const rows: Activity[] = [];
  for (const d of pos.deposits ?? []) {
    rows.push({
      key: `d-${d.txHash}`,
      at: d.at,
      kind: "deposit",
      title: "XRP deposited as collateral",
      link: txLink(d.txHash),
      amount: `+${dropsToXrp(d.drops)} XRP`,
    });
  }
  pos.payments.forEach((p, i) => {
    rows.push({
      key: `c-${p.at}-${i}`,
      at: p.at,
      kind: "card",
      title: p.label || "Card payment",
      link: p.flareActionId
        ? { href: teeDirectActionUrl(p.flareActionId), text: `TEE ${shortHex(p.flareActionId)}`, title: "TEE direct action" }
        : undefined,
      amount: `${fmt(num(p.rlusdAmount), 6)} RLUSD`,
    });
  });
  for (const c of conversions) {
    rows.push({
      key: `v-${c.id}`,
      at: c.createdAt,
      kind: "conversion",
      title: c.error ? `Convert to XLS-66 loan: ${c.error}` : "Card debt converted to an XLS-66 loan",
      link: txLink(c.loanSetTx),
      status: CONVERSION_STATUS[c.status],
      amount: `${fmt(num(c.rlusdAmount), 6)} RLUSD`,
    });
  }
  for (const r of pos.repayments ?? []) {
    rows.push({
      key: `r-${r.txHash}`,
      at: r.at,
      kind: "repayment",
      title: `Debt repaid to the pool · unlocked ${fmt(num(r.xrpUnlockedDrops) / 1e6)} XRP` +
        (r.rlusdExcess ? ` · ${fmt(num(r.rlusdExcess), 6)} RLUSD excess` : ""),
      link: txLink(r.txHash),
      amount: `−${fmt(num(r.rlusdCredited), 6)} RLUSD debt`,
    });
  }
  for (const p of installments) {
    rows.push({
      key: `i-${p.loanPayTx}`,
      at: p.at,
      kind: "installment",
      title: `Installment on loan ${p.loanId.slice(0, 8)}…`,
      link: txLink(p.loanPayTx),
      amount: `${fmt(num(p.amount), 6)} RLUSD`,
    });
  }
  return rows.sort((a, b) => b.at - a.at);
}

export function AccountOverview() {
  const [session, setSession] = useState<Session | null>(null);
  const [pos, setPos] = useState<Position | null>(null);
  const [conversions, setConversions] = useState<Conversion[]>([]);
  const [loans, setLoans] = useState<Loan[]>([]);
  const [repaymentAccount, setRepaymentAccount] = useState<string | null>(null);
  const [installments, setInstallments] = useState<LoanPayment[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [watcherDown, setWatcherDown] = useState(false);
  const [filter, setFilter] = useState<Kind | "all">("all");

  const load = useCallback(async () => {
    const s = loadSession();
    setSession(s);
    if (!s) return;
    try {
      const p = await teeCard<Position>("GET_POSITION", { tag: s.tag });
      setPos({ ...p, payments: attachFlareActions(s.tag, p.payments ?? []) });
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
    // Loans and conversions come from the watcher and XRPL; show the TEE view even if they fail.
    const [conv, ln, inst] = await Promise.allSettled([
      listConversions(s.tag),
      fetchLoans(s.xrplAddress),
      fetchLoanPayments(s.xrplAddress),
    ]);
    if (conv.status === "fulfilled") setConversions(conv.value);
    if (ln.status === "fulfilled") {
      setLoans(ln.value.loans);
      setRepaymentAccount(ln.value.repaymentAccount);
    }
    if (inst.status === "fulfilled") setInstallments(inst.value);
    setWatcherDown([conv, ln, inst].some((r) => r.status === "rejected"));
  }, []);

  useEffect(() => {
    load();
    const id = setInterval(load, REFRESH_MS);
    return () => clearInterval(id);
  }, [load]);

  const activity = useMemo(
    () => (pos ? buildActivity(pos, conversions, installments) : []),
    [pos, conversions, installments],
  );

  if (!session) {
    return (
      <section className="panel">
        <h2>My account</h2>
        <p className="empty-note">
          Connect a wallet on <Link href="/">Onboard</Link> to see your account.
        </p>
      </section>
    );
  }

  const price = num(pos?.xrpUsdPrice);
  const available = num(pos?.xrpAvailableDrops) / 1e6;
  const locked = num(pos?.xrpLockedDrops) / 1e6;
  const collateral = available + locked;
  const debt = num(pos?.rlusdDebt);
  const ltvCap = (pos?.ltvCapBps ?? 0) / 10000;
  const creditLimit = collateral * price * ltvCap;
  const utilization = creditLimit > 0 ? debt / creditLimit : 0;
  const cardSpend = (pos?.payments ?? []).reduce((s, p) => s + num(p.rlusdAmount), 0);
  const repaid = (pos?.repayments ?? []).reduce((s, r) => s + num(r.rlusdCredited), 0);
  const loanOutstanding = loans.reduce((s, l) => s + num(l.totalValueOutstanding), 0);
  const installmentsPaid = installments.reduce((s, p) => s + num(p.amount), 0);
  const nextDue = loans
    .filter((l) => l.nextPaymentDueDate)
    .sort((a, b) => (a.nextPaymentDueDate ?? 0) - (b.nextPaymentDueDate ?? 0))[0];
  const shown = filter === "all" ? activity : activity.filter((a) => a.kind === filter);
  const counts = activity.reduce<Record<string, number>>((m, a) => ({ ...m, [a.kind]: (m[a.kind] ?? 0) + 1 }), {});

  /** Downloads the activity as shown (respecting the filter) as CSV. */
  function exportActivity() {
    const rows = shown.map((a) => {
      // Explorer links end in the full transaction hash; TEE action links don't.
      const hash = a.link?.href.startsWith(`${EXPLORER_TX}/`) ? a.link.href.slice(EXPLORER_TX.length + 1) : "";
      // A leading + is only display; dropped so spreadsheets read the amount as text, not a formula.
      return [isoTime(a.at), KIND_LABEL[a.kind], a.title, a.amount.replace(/^\+/, ""), a.status?.text ?? "", hash, a.link?.href ?? ""];
    });
    const scope = filter === "all" ? "" : `-${filter}`;
    downloadCsv(
      `xrpl-card-activity-tag${session?.tag ?? ""}${scope}-${today()}.csv`,
      ["Time (UTC)", "Type", "Description", "Amount", "Status", "Transaction", "Link"],
      rows,
    );
  }

  return (
    <>
      <section className="panel">
        <div className="row" style={{ justifyContent: "space-between" }}>
          <div>
            <h2>My account</h2>
            <p className="lead mono account-id">
              Tag {session.tag} ·{" "}
              <a className="addr-link" href={xrplAccountUrl(session.xrplAddress)} target="_blank" rel="noopener noreferrer">
                {session.xrplAddress}
              </a>
            </p>
          </div>
          <button type="button" className="secondary" onClick={load}>
            Refresh
          </button>
        </div>
        {error && <p className="err">{error}</p>}
        {watcherDown && <p className="err">Watcher unreachable: loans and conversions may be out of date.</p>}

        <h3 className="section-label">Card account (inside the TEE)</h3>
        <div className="grid grid-3">
          <div className="stat">
            <label>Collateral</label>
            <strong>{fmt(collateral)} XRP</strong>
            <span className="stat-sub">≈ ${fmt(collateral * price)} at {pos ? usd(pos.xrpUsdPrice) : "—"}</span>
          </div>
          <div className="stat">
            <label>Locked for card debt</label>
            <strong>{fmt(locked)} XRP</strong>
            <span className="stat-sub">{fmt(available)} XRP free</span>
          </div>
          <div className="stat">
            <label>Card debt</label>
            <strong>{fmt(debt, 6)} RLUSD</strong>
            <span className="stat-sub">
              accruing {((pos?.borrowAprBps ?? 0) / 100).toFixed(0)}% APR · {fmt(num(pos?.convertibleRlusd ?? "0"), 6)} RLUSD convertible
            </span>
          </div>
          <div className="stat">
            <label>Credit used</label>
            <strong>{fmt(utilization * 100, 1)}%</strong>
            <Meter ratio={utilization} label="Credit used" />
            <span className="stat-sub">
              of ${fmt(creditLimit)} limit ({(ltvCap * 100).toFixed(0)}% LTV)
            </span>
          </div>
          <div className="stat">
            <label>Card spend, all time</label>
            <strong>{fmt(cardSpend, 6)} RLUSD</strong>
            <span className="stat-sub">{pos?.payments.length ?? 0} payments</span>
          </div>
          <div className="stat">
            <label>Debt repaid</label>
            <strong>{fmt(repaid, 6)} RLUSD</strong>
            <span className="stat-sub">{pos?.repayments?.length ?? 0} repayments to the pool</span>
          </div>
        </div>

        <h3 className="section-label">XLS-66 loans (on XRPL)</h3>
        <div className="grid">
          <div className="stat">
            <label>Owed on loans</label>
            <strong>{fmt(loanOutstanding, 6)} RLUSD</strong>
            <span className="stat-sub">{loans.length} open {loans.length === 1 ? "loan" : "loans"}</span>
          </div>
          <div className="stat">
            <label>Next installment</label>
            <strong>{nextDue ? `${installmentAmount(nextDue.periodicPayment)} RLUSD` : "—"}</strong>
            <span className="stat-sub">
              {nextDue?.nextPaymentDueDate ? `due ${new Date(nextDue.nextPaymentDueDate * 1000).toLocaleDateString()}` : "nothing due"}
            </span>
          </div>
          <div className="stat">
            <label>Installments paid</label>
            <strong>{fmt(installmentsPaid, 6)} RLUSD</strong>
            <span className="stat-sub">{installments.length} payments</span>
          </div>
          <div className="stat">
            <label>Repayment account</label>
            {repaymentAccount ? (
              <a className="mono addr-link" href={xrplAccountUrl(repaymentAccount)} target="_blank" rel="noopener noreferrer">
                {repaymentAccount.slice(0, 10)}…
              </a>
            ) : (
              <strong>—</strong>
            )}
            <span className="stat-sub">1-of-2: you + Credit Server</span>
          </div>
        </div>

        {loans.length > 0 && (
          <Collapsible title="Loans" count={loans.length}>
          <div className="table-scroll">
          <table className="stats-table" style={{ marginTop: "1rem" }}>
            <thead>
              <tr>
                <th>Loan</th>
                <th>Owed</th>
                <th>Principal left</th>
                <th>Progress</th>
                <th>Next due</th>
              </tr>
            </thead>
            <tbody>
              {loans.map((l) => {
                const total = loanTotalPayments(l, conversions);
                const made = total - l.paymentRemaining;
                return (
                  <tr key={l.loanId}>
                    <td className="mono">{l.loanId.slice(0, 10)}…</td>
                    <td>{fmt(num(l.totalValueOutstanding), 6)} RLUSD</td>
                    <td>{fmt(num(l.principalOutstanding), 6)}</td>
                    <td style={{ minWidth: "10rem" }}>
                      <span className="stat-sub">
                        {made} of {total} paid
                      </span>
                      <div className="meter__track" style={{ background: "color-mix(in srgb, var(--pink) 18%, transparent)" }}>
                        <div className="meter__fill" style={{ width: `${total ? (made / total) * 100 : 0}%`, background: "var(--pink)" }} />
                      </div>
                    </td>
                    <td>{l.nextPaymentDueDate ? new Date(l.nextPaymentDueDate * 1000).toLocaleDateString() : "—"}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
          </div>
          </Collapsible>
        )}
        {loans.length > 0 && (
          <p className="lead" style={{ marginTop: "0.5rem" }}>
            Pay installments on the <Link href="/loans">Loans</Link> page.
          </p>
        )}
      </section>

      <section className="panel">
        <Collapsible
          title={<h2>Activity</h2>}
          count={activity.length}
          actions={
            <button type="button" className="secondary" disabled={shown.length === 0} onClick={exportActivity}>
              Export CSV
            </button>
          }
        >
        <div className="chips" role="group" aria-label="Filter activity">
          {(["all", "deposit", "card", "conversion", "repayment", "installment"] as const).map((k) => (
            <button
              key={k}
              type="button"
              className="chip"
              data-active={filter === k ? "true" : "false"}
              onClick={() => setFilter(k)}
            >
              {k === "all" ? "All" : KIND_LABEL[k]} {k === "all" ? activity.length : counts[k] ?? 0}
            </button>
          ))}
        </div>
        {shown.length === 0 ? (
          <p className="empty-note">Nothing here yet.</p>
        ) : (
          <ul className="tx-list">
            {shown.map((a) => (
              <li key={a.key} className="tx-row activity-row">
                <time>{when(a.at)}</time>
                <span className="activity-main">
                  <span className="kind-label">{KIND_LABEL[a.kind]}</span>
                  <span>{a.title}</span>
                </span>
                <span>
                  {a.status && <span className={`badge ${a.status.tone}`}>{a.status.text}</span>}{" "}
                  {a.link && (
                    <a className="mono muted" href={a.link.href} target="_blank" rel="noopener noreferrer" title={a.link.title}>
                      {a.link.text}
                    </a>
                  )}
                </span>
                <strong>{a.amount}</strong>
              </li>
            ))}
          </ul>
        )}
        </Collapsible>
      </section>
    </>
  );
}

/** PaymentTotal is not on the Loan entry; take it from the conversion that created it, else the standard terms. */
function loanTotalPayments(loan: Loan, conversions: Conversion[]) {
  const conv = conversions.find((c) => c.loanId === loan.loanId);
  return Math.max(conv?.terms?.paymentTotal ?? deployment.lending.terms.paymentTotal, loan.paymentRemaining);
}

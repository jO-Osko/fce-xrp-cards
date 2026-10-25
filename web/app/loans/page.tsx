"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { LedgerPrompt } from "@/components/ledger-button";
import { Collapsible } from "@/components/collapsible";
import { XamanQr } from "@/components/xaman-qr";
import { xrplAccountUrl, REFRESH_MS } from "@/lib/config";
import {
  fetchLoans,
  installmentAmount,
  payLoan,
  type Loan,
} from "@/lib/loans";
import { NETWORK_NAME } from "@/lib/network";
import { loadSession, type Session } from "@/lib/session";
import { useSigner } from "@/lib/wallet";
import { rlusdPaymentBody } from "@/lib/xaman";

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** Xaman can report the txid before the ledger validates it; retry briefly. */
async function payWhenValidated(txHash: string, loanId: string) {
  for (let i = 0; ; i++) {
    try {
      return await payLoan(txHash, loanId);
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      // xrpl.js reports a not-yet-known tx as "Transaction not found."
      if (i >= 15 || !/not validated|txnNotFound|transaction not found/i.test(msg)) throw e;
      await sleep(2000);
    }
  }
}

function due(loan: Loan) {
  if (!loan.nextPaymentDueDate) return "—";
  return new Date(loan.nextPaymentDueDate * 1000).toLocaleDateString();
}

export default function LoansPage() {
  const [session, setSession] = useState<Session | null>(null);
  const [account, setAccount] = useState<string | null>(null);
  const [loans, setLoans] = useState<Loan[]>([]);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const { sign, xaman, onLedger } = useSigner();

  const refresh = useCallback(async () => {
    const s = loadSession();
    setSession(s);
    if (!s) return;
    try {
      const res = await fetchLoans(s.xrplAddress);
      setAccount(res.repaymentAccount);
      setLoans(res.loans);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    refresh();
    const id = setInterval(refresh, REFRESH_MS);
    return () => clearInterval(id);
  }, [refresh]);

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

  const payInstallment = (loan: Loan) =>
    run(loan.loanId, async () => {
      if (!account) throw new Error("no repayment account");
      if (!session) throw new Error("register on Onboard first");
      const value = installmentAmount(loan.periodicPayment);
      const funding = await sign(session.xrplAddress, rlusdPaymentBody({ destination: account, value }));
      const paid = await payWhenValidated(funding, loan.loanId);
      return `Paid ${paid.amount} RLUSD (LoanPay ${paid.loanPayTx.slice(0, 8)}…)`;
    });

  if (!session) {
    return (
      <section className="panel">
        <h2>Loans</h2>
        <p className="err">Register on Onboard first.</p>
      </section>
    );
  }

  return (
    <>
      <section className="panel">
        <h2>Loans</h2>
        <p className="lead">
          XLS-66 loans from converted card debt. They live on {NETWORK_NAME} in your
          repayment account, a 1-of-2 multisig of your key and the Credit Server.
          Each installment is two steps: you send RLUSD to the repayment account from
          your wallet (Xaman or Ledger), then the Credit Server signs the LoanPay for exactly that amount.
        </p>
        {account ? (
          <p className="lead">
            Repayment account{" "}
            <a className="mono addr-link" href={xrplAccountUrl(account)} target="_blank" rel="noopener noreferrer">
              {account}
            </a>
          </p>
        ) : (
          <p className="empty-note">No repayment account yet: convert card debt on the Position page.</p>
        )}
        {error && <p className="err">{error}</p>}
        {message && <p className="okmsg">{message}</p>}
        {xaman.pending && <XamanQr pending={xaman.pending} onCancel={xaman.cancel} />}
        <LedgerPrompt active={onLedger} />

        {loans.length > 0 && (
          <Collapsible title="Your loans" count={loans.length}>
            <table className="stats-table">
              <thead>
                <tr>
                  <th>Loan</th>
                  <th>Outstanding</th>
                  <th>Principal</th>
                  <th>Installment</th>
                  <th>Payments left</th>
                  <th>Next due</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {loans.map((l) => (
                  <tr key={l.loanId}>
                    <td className="mono">{l.loanId.slice(0, 10)}…</td>
                    <td>{Number(l.totalValueOutstanding).toFixed(2)} RLUSD</td>
                    <td>{Number(l.principalOutstanding).toFixed(2)}</td>
                    <td>{installmentAmount(l.periodicPayment)}</td>
                    <td>{l.paymentRemaining}</td>
                    <td>{due(l)}</td>
                    <td>
                      <button type="button" disabled={!!busy} onClick={() => payInstallment(l)}>
                        {busy === l.loanId ? "Paying…" : "Pay installment"}
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Collapsible>
        )}
      </section>

      <section className="panel">
        <h2>RLUSD for repayments</h2>
        <p className="lead">
          Installments are paid in RLUSD. {NETWORK_NAME} has no real RLUSD: add the trust line
          and get demo RLUSD on the <Link href="/faucets">Faucets</Link> page.
        </p>
      </section>
    </>
  );
}

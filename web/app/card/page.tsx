"use client";

import { FccPanel } from "@/components/fcc-panel";
import { PaymentCard } from "@/components/payment-card";
import { PaymentsList, type CardPayment } from "@/components/payments";
import { Collapsible } from "@/components/collapsible";
import { dropsToXrp, teeCard } from "@/lib/api";
import { REFRESH_MS } from "@/lib/config";
import { shortHex, teeDirectActionUrl } from "@/lib/fcc";
import {
  attachFlareActions,
  rememberFlareAction,
} from "@/lib/flare-actions";
import { loadSession, type Session } from "@/lib/session";
import { useCallback, useEffect, useMemo, useState } from "react";

type Reserve = {
  approved: boolean;
  rlusdAmount: string;
  xrpLockedDrops?: string;
  xrpAvailableLeft?: string;
  rlusdDebt?: string;
  virtualSettlementRlusd?: string;
  reason?: string;
  actionId?: string;
};

type CardStatus = "idle" | "authorizing" | "approved" | "declined";

const GOOD_TO_PAY = [
  { label: "Coffee at Swell Café", amount: "4.50" },
  { label: "Uber to the venue", amount: "12.00" },
  { label: "Chick-Fil-A", amount: "18.75" },
  { label: "Conference merch", amount: "35.00" },
  { label: "Hotel deposit hold", amount: "50.00" },
] as const;

export default function MockCardPage() {
  const [session, setSession] = useState<Session | null>(null);
  const [amount, setAmount] = useState<string>(GOOD_TO_PAY[0].amount);
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<Reserve | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [payments, setPayments] = useState<CardPayment[]>([]);

  const refreshPayments = useCallback(async (tag: number) => {
    try {
      const data = await teeCard<{ payments?: CardPayment[] }>("GET_POSITION", {
        tag,
      });
      setPayments(attachFlareActions(tag, data.payments ?? []));
    } catch {
      // position may be unavailable while the TEE is down
    }
  }, []);

  useEffect(() => {
    const s = loadSession();
    setSession(s);
    if (!s) return;
    refreshPayments(s.tag);
    const id = setInterval(() => refreshPayments(s.tag), REFRESH_MS);
    return () => clearInterval(id);
  }, [refreshPayments]);

  const status: CardStatus = useMemo(() => {
    if (busy) return "authorizing";
    if (!result) return "idle";
    return result.approved ? "approved" : "declined";
  }, [busy, result]);

  async function pay() {
    const s = loadSession();
    setSession(s);
    if (!s) {
      setError("Register on Onboard first");
      return;
    }
    setBusy(true);
    setError(null);
    setResult(null);
    try {
      const match = GOOD_TO_PAY.find((item) => item.amount === amount);
      const data = await teeCard<Reserve>("RESERVE", {
        tag: s.tag,
        rlusdAmount: amount,
        label: match?.label ?? "Card payment",
      });
      setResult(data);
      if (data.approved) {
        const pos = await teeCard<{ payments?: CardPayment[] }>("GET_POSITION", {
          tag: s.tag,
        });
        const latest = (pos.payments ?? [])[0];
        if (data.actionId && latest && latest.rlusdAmount === data.rlusdAmount) {
          rememberFlareAction({
            tag: s.tag,
            at: latest.at,
            rlusdAmount: latest.rlusdAmount,
            actionId: data.actionId,
          });
        }
        setPayments(attachFlareActions(s.tag, pos.payments ?? []));
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
    <section className="panel">
      <h2>Mock Card</h2>
      <p className="lead">
        Pay like at a card terminal. The TEE approves it in under a second: it locks
        enough of your XRP collateral, books the RLUSD as card debt, and adds the
        payment to the merchants&apos; daily settlement.
      </p>

      <div className="pay-layout">
        <PaymentCard
          tag={session?.tag}
          xrplAddress={session?.xrplAddress}
          amount={amount}
          status={status}
        />

        <div className="pay-form">
          <h3>Terminal</h3>
          <div className="pay-suggestions">
            <span className="pay-suggestions__label">Good to pay for</span>
            <ul>
              {GOOD_TO_PAY.map((item) => (
                <li key={item.label}>
                  <button
                    type="button"
                    className={amount === item.amount ? "active" : undefined}
                    onClick={() => {
                      setAmount(item.amount);
                      setResult(null);
                    }}
                  >
                    <span>{item.label}</span>
                    <span className="pay-suggestions__amt">${item.amount}</span>
                  </button>
                </li>
              ))}
            </ul>
          </div>

          <label className="field">
            Payment amount (RLUSD)
            <input
              inputMode="decimal"
              value={amount}
              onChange={(e) => {
                setAmount(e.target.value);
                setResult(null);
              }}
              placeholder="10.00"
            />
          </label>

          <button type="button" disabled={busy || !session} onClick={pay}>
            {busy ? "Authorizing…" : "Pay with card"}
          </button>

          {!session && (
            <p className="err">Register on Onboard first to bind this card to a tag.</p>
          )}
          {error && <p className="err">{error}</p>}

          {result && (
            <div className="pay-result">
              {result.approved ? (
                <span className="badge ok">Approved</span>
              ) : (
                <span className="badge bad">Declined</span>
              )}
              {result.reason && <p className="err">{result.reason}</p>}
              {result.approved && (
                <p className="okmsg">
                  Borrowed {result.rlusdAmount} virtual RLUSD
                  {result.xrpLockedDrops
                    ? ` · locked ${dropsToXrp(result.xrpLockedDrops)} XRP`
                    : ""}
                  . Settlement pile: {result.virtualSettlementRlusd}
                  {result.actionId ? (
                    <>
                      {" "}
                      · TEE{" "}
                      <a
                        className="mono"
                        href={teeDirectActionUrl(result.actionId)}
                        target="_blank"
                        rel="noreferrer"
                        title={result.actionId}
                      >
                        {shortHex(result.actionId)}
                      </a>
                      {" "}
                      (direct action)
                    </>
                  ) : null}
                </p>
              )}
            </div>
          )}
        </div>
      </div>

      {session && (
        <div className="tx-block">
          <Collapsible title="Card payments" count={payments.length}>
            <PaymentsList payments={payments} />
          </Collapsible>
        </div>
      )}
    </section>
    <FccPanel />
    </>
  );
}

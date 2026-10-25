"use client";

import { useCallback, useEffect, useState } from "react";
import { DepositsList, type Deposit } from "@/components/deposits";
import { Collapsible } from "@/components/collapsible";
import { PaymentsList, type CardPayment } from "@/components/payments";
import { ConvertPanel, type Repayment } from "@/components/convert-panel";
import { dropsToXrp, teeCard } from "@/lib/api";
import { REFRESH_MS, xrplAccountUrl } from "@/lib/config";
import { amount, shortAddress, usd } from "@/lib/format";
import { attachFlareActions } from "@/lib/flare-actions";
import { loadSession, saveSession, type Session } from "@/lib/session";

type Position = {
  tag: number;
  xrplAddress: string;
  xrpAvailableDrops: string;
  xrpLockedDrops: string;
  rlusdDebt: string;
  virtualSettlementRlusd: string;
  xrpUsdPrice: string;
  ltvCapBps: number;
  depositAccount: string;
  settlementAddress: string;
  deposits?: Deposit[];
  payments?: CardPayment[];
  convertibleRlusd?: string;
  repayments?: Repayment[];
};

export default function PositionPage() {
  const [session, setSession] = useState<Session | null>(null);
  const [pos, setPos] = useState<Position | null>(null);
  const [deposits, setDeposits] = useState<Deposit[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const refresh = useCallback(async () => {
    const s = loadSession();
    setSession(s);
    if (!s) {
      setError("Register on Onboard first");
      setPos(null);
      return;
    }
    setBusy(true);
    setError(null);
    try {
      let session = s;
      let data: Position;
      try {
        data = await teeCard<Position>("GET_POSITION", { tag: session.tag });
      } catch (e) {
        const msg = e instanceof Error ? e.message : String(e);
        // TEE restarted / snapshot missing — re-bind the Xaman address.
        if (!/account not found/i.test(msg)) throw e;
        const reg = await teeCard<{
          tag: number;
          xrplAddress: string;
          depositAccount: string;
        }>("REGISTER", { xrplAddress: session.xrplAddress });
        session = {
          xrplAddress: reg.xrplAddress,
          tag: reg.tag,
          depositAccount: reg.depositAccount || session.depositAccount,
        };
        saveSession(session);
        setSession(session);
        data = await teeCard<Position>("GET_POSITION", { tag: session.tag });
      }
      setPos({
        ...data,
        payments: attachFlareActions(session.tag, data.payments ?? []),
      });
      setDeposits(data.deposits ?? []);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setPos(null);
    } finally {
      setBusy(false);
    }
  }, []);

  useEffect(() => {
    refresh();
    const id = setInterval(refresh, REFRESH_MS);
    return () => clearInterval(id);
  }, [refresh]);

  return (
    <section className="panel">
      <div className="row" style={{ justifyContent: "space-between" }}>
        <div>
          <h2>Position</h2>
          <p className="lead">Your card account inside the TEE: collateral, debt and card payments.</p>
          {session && (
            <p className="lead">
              Card tag {session.tag} ·{" "}
              <a className="mono" href={xrplAccountUrl(session.xrplAddress)} target="_blank" rel="noopener noreferrer" title={session.xrplAddress}>
                {shortAddress(session.xrplAddress)}
              </a>
            </p>
          )}
        </div>
        <button type="button" className="secondary" disabled={busy} onClick={refresh}>
          {busy ? "Refreshing…" : "Refresh"}
        </button>
      </div>
      {error && <p className="err">{error}</p>}
      {pos && (
        <div className="grid">
          <div className="stat">
            <label>Free collateral</label>
            <strong>{dropsToXrp(pos.xrpAvailableDrops)} XRP</strong>
          </div>
          <div className="stat">
            <label>Locked collateral</label>
            <strong>{dropsToXrp(pos.xrpLockedDrops)} XRP</strong>
          </div>
          <div className="stat">
            <label>Card debt</label>
            <strong>{amount(pos.rlusdDebt)} RLUSD</strong>
          </div>
          <div className="stat">
            <label>XRP price</label>
            <strong>{usd(pos.xrpUsdPrice)}</strong>
          </div>
          <div className="stat">
            <label>LTV cap</label>
            <strong>{(pos.ltvCapBps / 100).toFixed(0)}%</strong>
          </div>
        </div>
      )}
      {pos && (
        <div className="tx-block">
          <Collapsible title="Deposits" count={deposits.length}>
            <DepositsList deposits={deposits} />
          </Collapsible>
        </div>
      )}
      {pos && (
        <div className="tx-block">
          <Collapsible title="Card payments" count={pos.payments?.length ?? 0}>
            <PaymentsList payments={pos.payments ?? []} />
          </Collapsible>
        </div>
      )}
      {pos && session && (
        <ConvertPanel
          tag={session.tag}
          rlusdDebt={pos.rlusdDebt}
          convertibleRlusd={pos.convertibleRlusd ?? "0"}
          repayments={pos.repayments ?? []}
          onChanged={refresh}
        />
      )}
    </section>
  );
}

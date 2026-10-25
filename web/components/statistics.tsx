"use client";

import { useCallback, useEffect, useState } from "react";
import { dropsToXrp, teeCard } from "@/lib/api";
import { REFRESH_MS, xrplAccountUrl } from "@/lib/config";
import { Collapsible } from "@/components/collapsible";
import { usd } from "@/lib/format";
import { Meter } from "@/components/meter";
import { pct, type Vault } from "@/lib/vault";

type UserStat = {
  tag: number;
  xrplAddress: string;
  xrpAvailableDrops: string;
  xrpLockedDrops: string;
  rlusdDebt: string;
  depositsCount: number;
  paymentsCount: number;
  utilizationBps: number;
  lastActivityAt: number;
};

type Stats = {
  userCount: number;
  totalXrpAvailableDrops: string;
  totalXrpLockedDrops: string;
  totalCollateralDrops: string;
  totalRlusdDebt: string;
  totalCardVolumeRlusd: string;
  virtualSettlementRlusd: string;
  processedTxCount: number;
  totalCardPayments: number;
  totalDeposits: number;
  xrpUsdPrice: string;
  ltvCapBps: number;
  depositAccount: string;
  settlementAddress: string;
  users: UserStat[];
  vault?: Vault;
};

// RLUSD with all 6 decimals the TEE keeps, so accrued interest shows.
const num = (s: string) => Number(s).toLocaleString(undefined, { maximumFractionDigits: 6 });

/** Where the pool's liquidity comes from, how it is used, and what the TEE earned. */
function PoolOverview({ vault: v }: { vault: Vault }) {
  const total = Number(v.totalAssetsRlusd);
  const internalShare = total > 0 ? Number(v.internalLiquidityRlusd) / total : 0;
  return (
    <>
      <h3 style={{ margin: "1.5rem 0 0.5rem" }}>Lending pool</h3>
      <div className="grid grid-3">
        <div className="stat">
          <label>Internal liquidity (operator)</label>
          <strong>{num(v.internalLiquidityRlusd)} RLUSD</strong>
          <span className="stat-sub">{(internalShare * 100).toFixed(1)}% of the pool</span>
        </div>
        <div className="stat">
          <label>LP liquidity</label>
          <strong>{num(v.lpLiquidityRlusd)} RLUSD</strong>
          <span className="stat-sub">{(100 - internalShare * 100).toFixed(1)}% · {v.lpCount} provider{v.lpCount === 1 ? "" : "s"}</span>
        </div>
        <div className="stat">
          <label>Utilization</label>
          <strong>{pct(v.utilizationBps)}</strong>
          <Meter ratio={v.utilizationBps / 10000} label="Pool utilization" />
          <span className="stat-sub">{num(v.cardDebtRlusd)} lent · {num(v.freeLiquidityRlusd)} free</span>
        </div>
        <div className="stat">
          <label>Rates</label>
          <strong>{pct(v.borrowAprBps)} borrow</strong>
          <span className="stat-sub">{pct(v.supplyAprBps)} to LPs · share price {v.sharePrice}</span>
        </div>
        <div className="stat">
          <label>TEE profit</label>
          <strong>{num(String(Number(v.feesEarnedRlusd) + Number(v.interestReserveRlusd)))} RLUSD</strong>
          <span className="stat-sub">
            {num(v.feesEarnedRlusd)} card fees ({pct(v.cardFeeBps)}) + {num(v.interestReserveRlusd)} interest ({pct(v.reserveFactorBps)})
          </span>
        </div>
        <div className="stat">
          <label>Paid to treasury</label>
          <strong>{num(v.treasuryPaidRlusd)} RLUSD</strong>
          <span className="stat-sub">{num(v.treasuryOwedRlusd)} more at the next settlement</span>
        </div>
      </div>
    </>
  );
}

function shortAddr(a: string) {
  if (!a || a.length <= 12) return a;
  return `${a.slice(0, 6)}…${a.slice(-4)}`;
}

function ago(at: number) {
  if (!at) return "-";
  const s = Math.max(0, Math.floor(Date.now() / 1000 - at));
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

export function Statistics() {
  const [stats, setStats] = useState<Stats | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await teeCard<Stats>("GET_STATS");
      setStats(data);
      setErr(null);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
    const t = setInterval(load, REFRESH_MS);
    return () => clearInterval(t);
  }, [load]);

  return (
    <section className="panel">
      <div className="row" style={{ justifyContent: "space-between", alignItems: "center" }}>
        <div>
          <h2 style={{ margin: 0 }}>Global statistics</h2>
          <p className="empty-note" style={{ margin: "0.25rem 0 0" }}>
            Live totals from the TEE ledger, refreshed every 10 seconds.
          </p>
        </div>
        <button className="btn" onClick={load} disabled={loading}>
          {loading ? "Refreshing…" : "Refresh"}
        </button>
      </div>

      {err ? <p className="err">{err}</p> : null}

      {stats ? (
        <>
          <div className="grid" style={{ marginTop: "1rem" }}>
            <div className="stat">
              <label>Cardholders</label>
              <strong>{stats.userCount}</strong>
            </div>
            <div className="stat">
              <label>Total collateral</label>
              <strong>{dropsToXrp(stats.totalCollateralDrops)} XRP</strong>
            </div>
            <div className="stat">
              <label>RLUSD outstanding</label>
              <strong>{num(stats.totalRlusdDebt)} RLUSD</strong>
            </div>
            <div className="stat">
              <label>Pending settlement</label>
              <strong>{num(stats.virtualSettlementRlusd)} RLUSD</strong>
            </div>
            <div className="stat">
              <label>Card volume (lifetime)</label>
              <strong>{num(stats.totalCardVolumeRlusd)} RLUSD</strong>
            </div>
            <div className="stat">
              <label>Card payments</label>
              <strong>{stats.totalCardPayments}</strong>
            </div>
            <div className="stat">
              <label>Deposits credited</label>
              <strong>{stats.totalDeposits}</strong>
            </div>
            <div className="stat">
              <label>XRP price</label>
              <strong>{usd(stats.xrpUsdPrice)}</strong>
            </div>
            <div className="stat">
              <label>LTV cap</label>
              <strong>{(stats.ltvCapBps / 100).toFixed(0)}%</strong>
            </div>
          </div>

          {stats.vault && <PoolOverview vault={stats.vault} />}

          <div style={{ marginTop: "1.5rem" }}>
          <Collapsible title="Cardholders" count={stats.users.length}>
          {stats.users.length === 0 ? (
            <p className="empty-note">
              No cardholders yet. Run <span className="mono">scripts/seed-cardholders.mjs</span> to add some.
            </p>
          ) : (
            <div className="stat" style={{ padding: 0, overflowX: "auto" }}>
              <table className="stats-table">
                <thead>
                  <tr>
                    <th>Tag</th>
                    <th>XRPL account</th>
                    <th>Available</th>
                    <th>Locked</th>
                    <th>Debt</th>
                    <th>Utilization</th>
                    <th>Swipes</th>
                    <th>Last activity</th>
                  </tr>
                </thead>
                <tbody>
                  {stats.users.map((u) => {
                    const util = u.utilizationBps / 100;
                    const hot = util >= 90;
                    return (
                      <tr key={u.tag}>
                        <td className="mono">{u.tag}</td>
                        <td className="mono" title={u.xrplAddress}>{shortAddr(u.xrplAddress)}</td>
                        <td>{dropsToXrp(u.xrpAvailableDrops)} XRP</td>
                        <td>{dropsToXrp(u.xrpLockedDrops)} XRP</td>
                        <td>{num(u.rlusdDebt)} RLUSD</td>
                        <td>
                          <span className={`badge ${hot ? "" : "ok"}`}>{util.toFixed(0)}%</span>
                        </td>
                        <td>{u.paymentsCount}</td>
                        <td className="empty-note">{ago(u.lastActivityAt)}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
          </Collapsible>
          </div>

          <p className="empty-note" style={{ marginTop: "1rem" }}>
            Deposit account{" "}
            <a className="mono" href={xrplAccountUrl(stats.depositAccount)} target="_blank" rel="noopener noreferrer">{shortAddr(stats.depositAccount)}</a>
            {" · "}settlement address{" "}
            <a className="mono" href={xrplAccountUrl(stats.settlementAddress)} target="_blank" rel="noopener noreferrer">{shortAddr(stats.settlementAddress)}</a>
            {" · "}{stats.processedTxCount} XRPL transactions credited
          </p>
        </>
      ) : (
        !err && <p className="empty-note" style={{ marginTop: "1rem" }}>Loading operator stats…</p>
      )}
    </section>
  );
}

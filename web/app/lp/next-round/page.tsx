"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { REFRESH_MS, xrplAccountUrl } from "@/lib/config";
import { amount, shortAddress, when } from "@/lib/format";
import {
  apyFromApr,
  fetchAllocation,
  fetchVault,
  loadLPSession,
  saveAllocation,
  sharesFromUnits,
  type Allocation,
  type Vault,
} from "@/lib/vault";
import deployment from "@/lib/deployment.gen.json";

const SHARE_CODE = deployment.vault.shareCode || "share";
const percent = (x: number) => `${(x * 100).toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}%`;

export default function NextRoundPage() {
  const [address, setAddress] = useState<string | null>(null);
  const [vault, setVault] = useState<Vault | null>(null);
  const [saved, setSaved] = useState<Allocation | null>(null);
  // The slider, in whole percent; null until the saved choice has loaded.
  const [share, setShare] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    const a = loadLPSession()?.xrplAddress ?? null;
    setAddress(a);
    try {
      setVault(await fetchVault(a ?? undefined));
      if (a) {
        const alloc = await fetchAllocation(a);
        setSaved(alloc);
        setShare((s) => (s == null ? alloc.unsecuredBps / 100 : s));
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    refresh();
    const id = setInterval(refresh, REFRESH_MS);
    return () => clearInterval(id);
  }, [refresh]);

  const pos = vault?.position;
  const shares = sharesFromUnits(Number(pos?.sharesIssued ?? 0) - Number(pos?.sharesRedeemed ?? 0));
  const worth = shares * Number(vault?.sharePrice || 1);
  const securedApy = apyFromApr((vault?.supplyAprBps ?? 0) / 10_000);
  const unsecuredApy = (saved?.round.projectedApyBps ?? 0) / 10_000;
  const p = (share ?? 0) / 100;
  const blendedApy = (1 - p) * securedApy + p * unsecuredApy;
  const changed = saved != null && share != null && Math.round(share * 100) !== saved.unsecuredBps;

  async function save() {
    if (!address || share == null) return;
    setBusy(true);
    setError(null);
    setMessage(null);
    try {
      const alloc = await saveAllocation(address, Math.round(share * 100), worth);
      setSaved(alloc);
      setMessage(`Saved: ${share}% of your deposit is marked for the next round. Nothing was signed or sent on chain.`);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <section className="panel yield-hero">
        <p className="yield-kicker">Next round · optional</p>
        <h2>Uncollateralized lending</h2>
        <p className="lead">
          The next round adds an uncollateralized lending protocol: loans without collateral behind them, so a
          borrower who does not repay is a loss for its lenders. In return it pays more. You can mark part of your
          existing deposit to move there when the round opens; the rest stays in today&apos;s pool, where every
          loan is backed by XRP collateral.
        </p>
        <p className="lead">
          This is only a choice saved off chain: nothing is signed, nothing moves, and you can change it until the
          round opens.
        </p>
        {vault && saved && (
          <div className="grid grid-3">
            <div className="stat">
              <label>Collateralized pool</label>
              <strong>{percent(securedApy)} APY</strong>
              <span className="stat-sub">today&apos;s pool, variable</span>
            </div>
            <div className="stat">
              <label>Uncollateralized</label>
              <strong className="apy-risky">{percent(unsecuredApy)} APY</strong>
              <span className="stat-sub">projected for the next round; can lose principal</span>
            </div>
            <div className="stat">
              <label>Pledged so far</label>
              <strong>{amount(saved.round.pledgedRlusd)} RLUSD</strong>
              <span className="stat-sub">
                from {saved.round.lpCount} liquidity provider{saved.round.lpCount === 1 ? "" : "s"}
              </span>
            </div>
          </div>
        )}
      </section>

      <section className="panel">
        <h2>Your split</h2>
        {!address ? (
          <p className="empty-note">
            Connect your wallet on <Link href="/lp">Yield</Link> first.
          </p>
        ) : !vault || share == null ? (
          <p className="empty-note">Loading…</p>
        ) : (
          <>
            <p className="lead">
              <a className="mono" href={xrplAccountUrl(address)} target="_blank" rel="noopener noreferrer" title={address}>
                {shortAddress(address)}
              </a>{" "}
              · deposit worth {amount(worth)} RLUSD ({amount(shares)} {SHARE_CODE})
            </p>
            {worth <= 0 && (
              <p className="muted">
                You have no deposit yet: deposit on <Link href="/lp">Yield</Link>, then come back to split it.
              </p>
            )}

            <div className="split">
              <div className="split-ends split-ends--risky">
                <span>Uncollateralized</span>
                <strong>{share}%</strong>
              </div>
              <input
                type="range"
                min={0}
                max={100}
                step={5}
                value={share}
                onChange={(e) => setShare(Number(e.target.value))}
                disabled={busy || worth <= 0}
                aria-label="Share of your deposit for uncollateralized lending"
                style={{ ["--fill" as string]: `${share}%` }}
              />
              <div className="split-ends split-ends--safe">
                <strong>{100 - share}%</strong>
                <span>Collateralized</span>
              </div>
            </div>

            <div className="grid grid-3">
              <div className="stat">
                <label>Stays collateralized</label>
                <strong>{amount(worth * (1 - p))} RLUSD</strong>
                <span className="stat-sub">at {percent(securedApy)} APY</span>
              </div>
              <div className="stat">
                <label>Moves to uncollateralized</label>
                <strong>{amount(worth * p)} RLUSD</strong>
                <span className="stat-sub">at {percent(unsecuredApy)} APY, projected</span>
              </div>
              <div className="stat">
                <label>Your blended APY</label>
                <strong className="apy-blended">{percent(blendedApy)}</strong>
                <span className="stat-sub">≈ {amount(worth * blendedApy)} RLUSD a year</span>
              </div>
            </div>

            <div className="row" style={{ marginTop: "1rem" }}>
              <button type="button" disabled={busy || !changed || worth <= 0} onClick={save}>
                {busy ? "Saving…" : "Save my choice"}
              </button>
              <span className="muted">
                {changed
                  ? "Not saved yet"
                  : saved?.updatedAt
                    ? `Saved ${when(saved.updatedAt)} · off chain`
                    : "Nothing saved: all of it stays collateralized"}
              </span>
            </div>
            {error && <p className="err">{error}</p>}
            {message && <p className="okmsg">{message}</p>}
          </>
        )}
      </section>
    </>
  );
}

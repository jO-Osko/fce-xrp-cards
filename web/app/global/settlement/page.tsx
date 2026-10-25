"use client";

import { FccPanel } from "@/components/fcc-panel";
import { TeeStatus } from "@/components/tee-status";
import { Collapsible } from "@/components/collapsible";
import {
  SettlementsList,
  type SettlementRow,
} from "@/components/settlements";
import {
  fetchSettlementStatus,
  mintPoolRlusd,
  rlusdPositive,
  watcherPool,
  watcherSettle,
  type PoolInfo,
  type SettlementStatus,
} from "@/lib/api";
import { REFRESH_MS, xrplAccountUrl } from "@/lib/config";
import { amount, shortAddress, shortHash } from "@/lib/format";
import { FCC, FCC_DEPLOYED, coston2AddressUrl, coston2TxUrl, shortHex } from "@/lib/fcc";
import { NETWORK_NAME } from "@/lib/network";
import { loadSession } from "@/lib/session";
import {
  clearSettleResult,
  loadSettleResult,
  saveSettleResult,
  type SettleResult,
} from "@/lib/settle-result";
import { useCallback, useEffect, useState } from "react";

export default function SettlePage() {
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<SettleResult | null>(null);
  const [settlements, setSettlements] = useState<SettlementRow[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [pool, setPool] = useState<PoolInfo | null>(null);
  const [poolError, setPoolError] = useState<string | null>(null);
  const [status, setStatus] = useState<SettlementStatus | null>(null);
  const [statusError, setStatusError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [minting, setMinting] = useState(false);

  async function mintPool() {
    setMinting(true);
    setPoolError(null);
    try {
      await mintPoolRlusd("10000");
      refreshPool();
    } catch (e) {
      setPoolError(e instanceof Error ? e.message : String(e));
    } finally {
      setMinting(false);
    }
  }

  async function copyPoolAddress() {
    if (!pool?.address) return;
    try {
      await navigator.clipboard.writeText(pool.address);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard unavailable */
    }
  }

  const refreshPool = useCallback(() => {
    watcherPool()
      .then((p) => {
        setPool(p);
        setPoolError(null);
      })
      .catch((e) =>
        setPoolError(e instanceof Error ? e.message : String(e)),
      );
  }, []);

  const refreshStatus = useCallback(() => {
    const tag = loadSession()?.tag;
    fetchSettlementStatus(tag)
      .then((s) => {
        setStatus(s);
        setStatusError(null);
      })
      .catch((e) =>
        setStatusError(e instanceof Error ? e.message : String(e)),
      );
  }, []);

  const refreshSettlements = useCallback(async () => {
    const local = loadSettleResult();
    try {
      const res = await fetch("/api/settle/history", { cache: "no-store" });
      if (!res.ok) {
        if (local?.txHash) {
          setResult(local);
          setSettlements([local]);
        }
        return;
      }
      const json = (await res.json()) as {
        ok?: boolean;
        settlements?: SettlementRow[];
      };
      const rows = Array.isArray(json.settlements) ? json.settlements : [];
      setSettlements(rows);
      // The watcher (backed by XRPL) is the source of truth once it answers: no
      // settlement there means none, whatever the browser remembers.
      const latest = rows[0];
      if (latest?.txHash) {
        saveSettleResult(latest);
        setResult(latest);
      } else {
        clearSettleResult();
        setResult(null);
      }
    } catch {
      if (local?.txHash) {
        setResult(local);
        setSettlements([local]);
      }
    }
  }, []);

  useEffect(() => {
    refreshSettlements();
    refreshPool();
    refreshStatus();
    const id = setInterval(() => {
      refreshPool();
      refreshStatus();
      refreshSettlements();
    }, REFRESH_MS);
    return () => clearInterval(id);
  }, [refreshSettlements, refreshPool, refreshStatus]);

  async function settle() {
    setBusy(true);
    setError(null);
    try {
      const data = await watcherSettle();
      saveSettleResult(data);
      setResult(data);
      setSettlements((prev) => {
        const rest = prev.filter((s) => s.txHash !== data.txHash);
        return [data, ...rest];
      });
      refreshPool();
      refreshStatus();
      refreshSettlements();
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      // TEE already paid the pile (or a prior click finished after the UI timed out).
      if (/nothing to settle/i.test(msg)) {
        setError(null);
        await refreshSettlements();
        refreshPool();
        refreshStatus();
      } else {
        setError(msg);
        refreshStatus();
      }
    } finally {
      setBusy(false);
    }
  }


  const pending = rlusdPositive(status?.pendingSettleRlusd);
  const ready = rlusdPositive(status?.virtualSettlementRlusd);
  // Interest and fees alone also settle (to the treasury), with no card spend.
  const treasuryOnly = !pending && !ready && rlusdPositive(status?.treasuryOwedRlusd);
  const needSettle = pending || ready || treasuryOnly;
  const settleAmount = pending
    ? status!.pendingSettleRlusd
    : treasuryOnly
      ? `${status!.treasuryOwedRlusd} (treasury)`
      : status?.virtualSettlementRlusd ?? null;

  return (
    <>
      <TeeStatus />
      <section className="panel">
        <h2>Fund RLUSD pool</h2>
        <p className="lead">
          Settlement pays out of the pool wallet on {NETWORK_NAME}. The pool&apos;s RLUSD comes from LP
          deposits, and the operator is an LP like everyone else. {NETWORK_NAME}{" "}has no real RLUSD, so the
          operator deposit mints demo RLUSD and deposits it as the operator&apos;s liquidity: the TEE credits
          it, issues the operator&apos;s shares, and the watcher moves the RLUSD into the pool.
        </p>
        {poolError && <p className="err">{poolError}</p>}
        {pool && (
          <div className="grid">
            <div className="stat">
              <label>Pool RLUSD balance</label>
              <strong>
                {pool.balanceRlusd == null
                  ? "—"
                  : `${amount(pool.balanceRlusd)} RLUSD`}
              </strong>
            </div>
            <div className="stat">
              <label>Pool address</label>
              <div className="addr-line">
                <a
                  className="mono addr-link"
                  href={xrplAccountUrl(pool.address)}
                  target="_blank"
                  rel="noreferrer"
                  title={pool.address}
                >
                  {shortAddress(pool.address)}
                </a>
                <button
                  type="button"
                  className="secondary copy-btn"
                  onClick={copyPoolAddress}
                >
                  {copied ? "Copied" : "Copy"}
                </button>
              </div>
            </div>
            <div className="stat">
              <label>RLUSD issuer</label>
              <a
                className="mono addr-link"
                href={xrplAccountUrl(pool.rlusd.issuer)}
                target="_blank"
                rel="noreferrer"
                title={pool.rlusd.issuer}
              >
                {shortAddress(pool.rlusd.issuer)}
              </a>
            </div>
            <div className="stat">
              <label>Operator deposit</label>
              <button
                type="button"
                className="secondary"
                disabled={minting}
                onClick={mintPool}
              >
                {minting ? "Depositing…" : "Deposit 10,000 RLUSD"}
              </button>
            </div>
          </div>
        )}
      </section>

      <section className="panel">
        <h2>Daily settlement</h2>
        <p className="lead">
          Once a day the TEE closes the day&apos;s card spend. The pool then pays, in RLUSD on{" "}
          {NETWORK_NAME}, the merchants&apos; total to the settlement address and the
          TEE&apos;s profit (card fees and its interest cut) to the treasury.
        </p>

        <div className="grid" style={{ marginBottom: "1rem" }}>
          <div className="stat">
            <label>Awaiting settlement</label>
            <strong>
              {statusError
                ? "—"
                : status == null
                  ? "…"
                  : needSettle
                    ? `${amount(settleAmount)} RLUSD`
                    : "0 RLUSD"}
            </strong>
          </div>
          <div className="stat">
            <label>Status</label>
            <strong>
              {statusError ? (
                <span className="badge warn">Status unavailable</span>
              ) : status == null ? (
                <span className="badge warn">Checking…</span>
              ) : pending ? (
                <span className="badge warn">Retry XRPL payment</span>
              ) : ready ? (
                <span className="badge warn">Ready to settle</span>
              ) : (
                <span className="badge ok">Nothing to settle</span>
              )}
            </strong>
          </div>
        </div>

        {statusError && (
          <p className="err">
            {statusError}
            {!loadSession() &&
              " — is the TEE running?"}
          </p>
        )}
        {!statusError && status && !needSettle && (
          <p className="okmsg">
            Caught up. Make a Mock Card payment first — that credits the daily
            settlement pile.
          </p>
        )}
        {!statusError && status && ready && (
          <p className="lead" style={{ marginTop: 0 }}>
            {amount(status.virtualSettlementRlusd)} RLUSD of card spend is waiting. Settle to pay it
            out on {NETWORK_NAME}.
          </p>
        )}
        {!statusError && status && pending && (
          <p className="lead" style={{ marginTop: 0 }}>
            The TEE already closed {amount(status.pendingSettleRlusd)} RLUSD. Settle again to resend
            the XRPL payment and confirm it.
          </p>
        )}

        <div className="row">
          <button
            type="button"
            disabled={busy || (!needSettle && status != null && !statusError)}
            onClick={settle}
          >
            {busy
              ? "Settling…"
              : pending
                ? `Complete settlement (${amount(settleAmount)} RLUSD)`
                : ready
                  ? `Settle ${amount(settleAmount)} RLUSD`
                  : "Settle"}
          </button>
        </div>
        {error && <p className="err">{error}</p>}

        <div className="tx-block">
          <Collapsible title="Settlements" count={settlements.length}>
            <SettlementsList settlements={settlements} />
          </Collapsible>
        </div>

        {result && FCC_DEPLOYED && (
          <div style={{ marginTop: "1.25rem" }}>
            <h3 style={{ margin: "0 0 0.65rem", fontSize: "0.95rem" }}>
              Latest settlement detail
            </h3>
            <div className="grid">
              <div className="stat">
                <label>RLUSD settled</label>
                <strong>{amount(result.rlusdAmount)} RLUSD</strong>
              </div>
              <div className="stat">
                <label>Settlement address</label>
                <strong className="mono" title={result.settlementAddress}>{shortAddress(result.settlementAddress)}</strong>
              </div>
              <div className="stat">
                <label>XRPL tx</label>
                <strong className="mono">
                  <a href={result.explorerUrl} target="_blank" rel="noreferrer">
                    {shortHash(result.txHash)}
                  </a>
                </strong>
              </div>
              <>
              <div className="stat">
                <label>TEE instruction sender</label>
                <strong className="mono">
                  <a
                    href={coston2AddressUrl(FCC.instructionSender)}
                    target="_blank"
                    rel="noreferrer"
                    title={FCC.instructionSender}
                  >
                    {shortHex(FCC.instructionSender, 8, 6)}
                  </a>
                </strong>
              </div>
              <div className="stat">
                <label>Coston2 · TEE production</label>
                <strong className="mono">
                  <a
                    href={coston2TxUrl(FCC.productionTx)}
                    target="_blank"
                    rel="noreferrer"
                    title="Machine toProduction on Coston2"
                  >
                    {shortHex(FCC.productionTx)}
                  </a>
                </strong>
              </div>
              </>
              <div className="stat">
                <label>Coston2 · SETTLE_DAILY</label>
                <strong className="mono">
                  {result.settleDailyFlareTx ? (
                    <a
                      href={coston2TxUrl(result.settleDailyFlareTx)}
                      target="_blank"
                      rel="noreferrer"
                      title={
                        result.settleDailyActionId
                          ? `Instruction ${result.settleDailyActionId}`
                          : result.settleDailyFlareTx
                      }
                    >
                      {shortHex(result.settleDailyFlareTx)}
                    </a>
                  ) : result.settleDailyActionId ? (
                    <span title={result.settleDailyActionId}>
                      {shortHex(result.settleDailyActionId)}
                    </span>
                  ) : (
                    "—"
                  )}
                </strong>
              </div>
              <div className="stat">
                <label>Coston2 · SETTLE_CONFIRM</label>
                <strong className="mono">
                  {result.settleConfirmFlareTx ? (
                    <a
                      href={coston2TxUrl(result.settleConfirmFlareTx)}
                      target="_blank"
                      rel="noreferrer"
                      title={
                        result.settleConfirmActionId
                          ? `Instruction ${result.settleConfirmActionId}`
                          : result.settleConfirmFlareTx
                      }
                    >
                      {shortHex(result.settleConfirmFlareTx)}
                    </a>
                  ) : result.settleConfirmActionId ? (
                    <span title={result.settleConfirmActionId}>
                      {shortHex(result.settleConfirmActionId)}
                    </span>
                  ) : (
                    "—"
                  )}
                </strong>
              </div>
              <div className="stat">
                <label>Status</label>
                <strong>
                  {result.wireQueued ? (
                    <span className="badge ok">Settled</span>
                  ) : (
                    "—"
                  )}
                </strong>
              </div>
            </div>
          </div>
        )}
      </section>
      <FccPanel />
    </>
  );
}

"use client";

import { DepositsList, type Deposit } from "@/components/deposits";
import { Collapsible } from "@/components/collapsible";
import { LedgerButton, LedgerPrompt } from "@/components/ledger-button";
import { XamanQr } from "@/components/xaman-qr";
import { dropsToXrp, teeCard } from "@/lib/api";
import { amount } from "@/lib/format";
import { fetchRlusdAccount } from "@/lib/loans";
import { EXPLORER_TX, REFRESH_MS } from "@/lib/config";
import { signAndSubmit } from "@/lib/ledger";
import { NETWORK_NAME } from "@/lib/network";
import { forgetLedger, ledgerAccountFor } from "@/lib/wallet";
import { loadSession, saveSession, type Session } from "@/lib/session";
import {
  createXamanPayload,
  depositPaymentBody,
  fetchXamanPayload,
  pendingFromPayload,
  signInBody,
  XAMAN_POLL_MS,
  type XamanPending,
} from "@/lib/xaman";
import { useCallback, useEffect, useRef, useState } from "react";

type RegisterData = {
  tag: number;
  xrplAddress: string;
  depositAccount: string;
};

type Flow =
  | { kind: "idle" }
  | { kind: "signin"; pending: XamanPending }
  | { kind: "deposit"; pending: XamanPending }
  | { kind: "deposit-done"; txid: string };

export default function OnboardPage() {
  const [session, setSession] = useState<Session | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [flow, setFlow] = useState<Flow>({ kind: "idle" });
  const [depositXrp, setDepositXrp] = useState("50");
  const [deposits, setDeposits] = useState<Deposit[]>([]);
  const [refreshing, setRefreshing] = useState(false);
  const [walletXrp, setWalletXrp] = useState<string | null>(null);
  const [collateral, setCollateral] = useState<{ free: string; locked: string } | null>(null);
  const [onLedger, setOnLedger] = useState(false);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const refreshDeposits = useCallback(async (tag: number) => {
    try {
      const pos = await teeCard<{ deposits?: Deposit[]; xrpAvailableDrops: string; xrpLockedDrops: string }>("GET_POSITION", { tag });
      setDeposits(pos.deposits ?? []);
      setCollateral({ free: pos.xrpAvailableDrops, locked: pos.xrpLockedDrops });
    } catch {
      // not registered yet, or the TEE is restarting
    }
    // What the wallet itself holds, read from the ledger by the watcher.
    const address = loadSession()?.xrplAddress;
    if (address) {
      fetchRlusdAccount(address)
        .then((a) => setWalletXrp(a.funded ? a.xrp : "0"))
        .catch(() => setWalletXrp(null));
    }
  }, []);

  useEffect(() => {
    const s = loadSession();
    setSession(s);
    if (s) refreshDeposits(s.tag);
    return () => stopPolling();
  }, [refreshDeposits]);

  useEffect(() => {
    if (!session) return;
    const id = setInterval(
      () => refreshDeposits(session.tag),
      REFRESH_MS,
    );
    return () => clearInterval(id);
  }, [session, refreshDeposits]);

  function stopPolling() {
    if (pollRef.current) {
      clearInterval(pollRef.current);
      pollRef.current = null;
    }
  }

  function cancelFlow() {
    stopPolling();
    setBusy(false);
    setFlow({ kind: "idle" });
  }

  async function registerAddress(xrplAddress: string) {
    const data = await teeCard<RegisterData>("REGISTER", { xrplAddress });
    const s: Session = {
      xrplAddress: data.xrplAddress,
      tag: data.tag,
      depositAccount: data.depositAccount || "",
    };
    saveSession(s);
    setSession(s);
    void refreshDeposits(s.tag);
    return s;
  }

  function startPolling(
    uuid: string,
    onSigned: (status: NonNullable<Awaited<ReturnType<typeof fetchXamanPayload>>>) => void | Promise<void>,
  ) {
    stopPolling();
    pollRef.current = setInterval(async () => {
      try {
        const data = await fetchXamanPayload(uuid);
        if (!data) return;
        if (data.meta?.signed) {
          stopPolling();
          await onSigned(data);
        } else if (data.meta?.expired || data.meta?.cancelled || data.meta?.resolved) {
          stopPolling();
          setBusy(false);
          setFlow({ kind: "idle" });
          setError("Xaman request expired, was cancelled or was rejected");
        }
      } catch {
        // keep polling on transient errors
      }
    }, XAMAN_POLL_MS);
  }

  async function connectXaman() {
    setBusy(true);
    setError(null);
    try {
      const payload = await createXamanPayload(signInBody());
      const pending = pendingFromPayload(payload);
      setFlow({ kind: "signin", pending });
      startPolling(payload.uuid, async (status) => {
        const account = status.response?.account;
        if (!account) {
          setBusy(false);
          setFlow({ kind: "idle" });
          setError("Xaman signed but no account returned");
          return;
        }
        try {
          forgetLedger(account);
          await registerAddress(account);
          setFlow({ kind: "idle" });
        } catch (e) {
          setError(e instanceof Error ? e.message : String(e));
          setFlow({ kind: "idle" });
        } finally {
          setBusy(false);
        }
      });
    } catch (e) {
      setBusy(false);
      setFlow({ kind: "idle" });
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  async function depositWithXaman() {
    const s = loadSession();
    setSession(s);
    if (!s?.depositAccount) {
      setError("Missing deposit account — set XRPL_DEPOSIT_ACCOUNT on the TEE");
      return;
    }
    setBusy(true);
    setError(null);
    const body = depositPaymentBody({
      destination: s.depositAccount,
      destinationTag: s.tag,
      amountXrp: depositXrp,
    });
    const ledger = ledgerAccountFor(s.xrplAddress);
    if (ledger) {
      setOnLedger(true);
      try {
        setFlow({ kind: "deposit-done", txid: await signAndSubmit(ledger, body.txjson) });
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      } finally {
        setOnLedger(false);
        setBusy(false);
      }
      return;
    }
    try {
      const payload = await createXamanPayload(body);
      const pending = pendingFromPayload(payload);
      setFlow({ kind: "deposit", pending });
      startPolling(payload.uuid, (status) => {
        const txid = status.response?.txid;
        setBusy(false);
        if (!txid) {
          setFlow({ kind: "idle" });
          setError("Deposit signed but no transaction id returned");
          return;
        }
        setFlow({ kind: "deposit-done", txid });
      });
    } catch (e) {
      setBusy(false);
      setFlow({ kind: "idle" });
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  const showSignInQr = flow.kind === "signin";
  const showDepositQr = flow.kind === "deposit";

  return (
    <>
      <section className="panel">
        <h2>Onboard</h2>
        <p className="lead">
          Connect{" "}
          <a href="https://xaman.app" target="_blank" rel="noopener noreferrer">
            Xaman
          </a>{" "}
          or a Ledger on {NETWORK_NAME}. The TEE assigns a destination tag — deposit XRP into the
          TEE deposit account with that tag and it becomes your card collateral.
        </p>

        {showSignInQr ? (
          <XamanQr
            pending={flow.pending}
            title="Scan with Xaman to connect"
            onCancel={cancelFlow}
          />
        ) : session ? (
          <div className="row">
            <span className="badge ok">
              Connected{ledgerAccountFor(session.xrplAddress) ? " · Ledger" : " · Xaman"}
            </span>
            <button
              type="button"
              className="secondary"
              disabled={busy}
              onClick={connectXaman}
            >
              {busy ? "Opening Xaman…" : "Switch to Xaman"}
            </button>
            <LedgerButton
              label="Switch to Ledger"
              disabled={busy}
              onError={setError}
              onConnected={async (acct) => {
                setError(null);
                await registerAddress(acct.address);
              }}
            />
          </div>
        ) : (
          <div className="row">
            <button type="button" disabled={busy} onClick={connectXaman}>
              {busy ? "Opening Xaman…" : "Connect with Xaman"}
            </button>
            <LedgerButton
              label="Connect Ledger"
              disabled={busy}
              onError={setError}
              onConnected={async (acct) => {
                setError(null);
                await registerAddress(acct.address);
              }}
            />
          </div>
        )}

        {error && <p className="err">{error}</p>}
      </section>

      {session && (
        <section className="panel">
          <h2>Deposit instructions</h2>
          <p className="lead">
            Deposit XRP as collateral from your wallet with your destination tag. The
            watcher credits it to your collateral once the deposit validates.
          </p>
          <div className="grid holdings">
            <div className="stat">
              <label>XRP in your wallet</label>
              <strong>{walletXrp == null ? "…" : `${amount(walletXrp, 6)} XRP`}</strong>
              <span className="stat-sub">on {NETWORK_NAME}; part of it is held as the account reserve</span>
            </div>
            <div className="stat">
              <label>Deposited as collateral</label>
              <strong>
                {collateral == null
                  ? "…"
                  : `${dropsToXrp(String(Number(collateral.free) + Number(collateral.locked)))} XRP`}
              </strong>
              <span className="stat-sub">
                {collateral == null
                  ? "in the TEE"
                  : `${dropsToXrp(collateral.free)} free · ${dropsToXrp(collateral.locked)} locked for card debt`}
              </span>
            </div>
          </div>

          <div className="deposit-grid">
            <div className="stat">
              <label>Your address</label>
              <strong className="mono" title={session.xrplAddress}>
                {session.xrplAddress}
              </strong>
            </div>
            <div className="stat">
              <label>Destination tag</label>
              <strong>{session.tag}</strong>
            </div>
            <div className="stat">
              <label>TEE deposit account</label>
              <strong
                className="mono"
                title={session.depositAccount || undefined}
              >
                {session.depositAccount || "(set XRPL_DEPOSIT_ACCOUNT)"}
              </strong>
            </div>
          </div>

          {showDepositQr ? (
            <XamanQr
              pending={flow.pending}
              title="Scan with Xaman to deposit XRP"
              onCancel={cancelFlow}
            />
          ) : flow.kind === "deposit-done" ? (
            <div className="xaman-done">
              <span className="badge ok">Deposit submitted</span>
              <p className="okmsg">
                <a
                  href={`${EXPLORER_TX}/${flow.txid}`}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  View on {NETWORK_NAME} explorer
                </a>
              </p>
              <button type="button" className="secondary" onClick={cancelFlow}>
                Deposit again
              </button>
            </div>
          ) : (
            <div className="deposit-xaman">
              <label className="field">
                Amount (XRP)
                <input
                  inputMode="decimal"
                  value={depositXrp}
                  onChange={(e) => setDepositXrp(e.target.value)}
                  placeholder="50"
                />
              </label>
              <button
                type="button"
                disabled={busy || !session.depositAccount}
                onClick={depositWithXaman}
              >
                {onLedger
                  ? "Confirm on your Ledger…"
                  : busy && flow.kind === "idle"
                    ? "Opening Xaman…"
                    : "Deposit XRP as collateral"}
              </button>
            </div>
          )}

          <LedgerPrompt active={onLedger} />

          <div className="tx-block">
            <Collapsible
              title="Credited deposits"
              count={deposits.length}
              actions={
                <button
                  type="button"
                  className="secondary"
                  disabled={refreshing}
                  onClick={async () => {
                    setRefreshing(true);
                    await refreshDeposits(session.tag);
                    setRefreshing(false);
                  }}
                >
                  {refreshing ? "Refreshing…" : "Refresh"}
                </button>
              }
            >
              <DepositsList deposits={deposits} />
            </Collapsible>
          </div>
        </section>
      )}
    </>
  );
}

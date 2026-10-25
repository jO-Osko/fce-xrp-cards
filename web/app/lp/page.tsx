"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { LedgerButton, LedgerPrompt } from "@/components/ledger-button";
import { XamanQr } from "@/components/xaman-qr";
import { EXPLORER_TX, REFRESH_MS, xrplAccountUrl } from "@/lib/config";
import { amount, shortAddress, shortHash } from "@/lib/format";
import { disconnectLedger, ledgerSupported, type LedgerAccount } from "@/lib/ledger";
import { fetchRlusdAccount, type RlusdAccount } from "@/lib/loans";
import { NETWORK_NAME, RLUSD } from "@/lib/network";
import { forgetLedger, rememberLedger, useSigner } from "@/lib/wallet";
import { apyFromApr, fetchVault, lpPosition, lpRegister, pct, saveLPSession, sharesFromUnits, type Vault } from "@/lib/vault";
import { rlusdTaggedPaymentBody, rlusdTrustSetBody, shareTrustSetBody, signInBody } from "@/lib/xaman";
import deployment from "@/lib/deployment.gen.json";

const SHARE_CODE = deployment.vault.shareCode || "share";
const WALLET_KEY = "xrpl-cc-yield-wallet";

type Wallet = { kind: "ledger"; account: LedgerAccount } | { kind: "xaman"; address: string };

const percent = (x: number) => `${(x * 100).toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}%`;

function loadWallet(): Wallet | null {
  try {
    const raw = localStorage.getItem(WALLET_KEY);
    return raw ? (JSON.parse(raw) as Wallet) : null;
  } catch {
    return null;
  }
}

function saveWallet(w: Wallet | null) {
  try {
    if (w) localStorage.setItem(WALLET_KEY, JSON.stringify(w));
    else localStorage.removeItem(WALLET_KEY);
  } catch {
    /* private window: the wallet is forgotten on reload */
  }
}

const walletAddress = (w: Wallet) => (w.kind === "ledger" ? w.account.address : w.address);

export default function YieldPage() {
  const [vault, setVault] = useState<Vault | null>(null);
  const [wallet, setWallet] = useState<Wallet | null>(null);
  const [account, setAccount] = useState<RlusdAccount | null>(null);
  const [shareLine, setShareLine] = useState<boolean | null>(null);
  const [tag, setTag] = useState<number | null>(null);
  const [depositAmount, setDepositAmount] = useState("1000");
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<{ text: string; tx?: string } | null>(null);
  const [hid, setHid] = useState(true);
  const { sign: signAs, xaman, onLedger } = useSigner();

  const address = wallet ? walletAddress(wallet) : null;

  useEffect(() => {
    const w = loadWallet();
    if (w?.kind === "ledger") rememberLedger(w.account);
    setWallet(w);
    setHid(ledgerSupported());
  }, []);

  const refresh = useCallback(async () => {
    try {
      setVault(await fetchVault(address ?? undefined));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
    if (!address) return;
    try {
      const [acct, pos] = await Promise.all([fetchRlusdAccount(address), lpPosition(address)]);
      setAccount(acct);
      setShareLine(pos.authorized);
    } catch {
      /* watcher down: keep the last view */
    }
  }, [address]);

  useEffect(() => {
    refresh();
    const id = setInterval(refresh, REFRESH_MS);
    return () => clearInterval(id);
  }, [refresh]);

  // The deposit tag is reserved once per address (LP_REGISTER is idempotent).
  useEffect(() => {
    setTag(null);
    if (!address) return;
    lpRegister(address)
      .then((r) => setTag(r.tag))
      .catch((e) => setError(e instanceof Error ? e.message : String(e)));
  }, [address]);

  async function run(label: string, fn: () => Promise<{ text: string; tx?: string }>) {
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

  function use(w: Wallet | null) {
    saveWallet(w);
    // The Liquidity page shows the same wallet's position and withdrawals.
    saveLPSession(w ? { xrplAddress: walletAddress(w) } : null);
    setWallet(w);
    setAccount(null);
    setShareLine(null);
  }

  /** Signs and submits one transaction with the connected wallet; returns its hash. */
  async function sign(body: { txjson: Record<string, unknown> }) {
    if (!wallet) throw new Error("connect a wallet first");
    return signAs(walletAddress(wallet), body);
  }

  const apr = (vault?.supplyAprBps ?? 0) / 10_000;
  const apy = apyFromApr(apr);
  const price = Number(vault?.sharePrice || 1);
  const pos = vault?.position;
  const shares = sharesFromUnits(Number(pos?.sharesIssued ?? 0) - Number(pos?.sharesRedeemed ?? 0));
  const deposited = (pos?.deposits ?? []).reduce((s, d) => s + Number(d.rlusdAmount), 0);
  const withdrawn = (pos?.redemptions ?? []).reduce((s, r) => s + Number(r.rlusdAmount), 0);
  const worth = shares * price;
  const earned = worth + withdrawn - deposited;
  const toDeposit = Number(depositAmount);
  const funded = account?.funded !== false;
  const rlusdLine = account?.trustLine === true;
  const balance = Number(account?.rlusd ?? 0);

  return (
    <>
      <section className="panel yield-hero">
        <p className="yield-kicker">Earn on RLUSD</p>
        <div className="yield-apy">
          <strong>{vault ? percent(apy) : "…"}</strong>
          <span>APY</span>
        </div>
        <p className="lead">
          Deposit RLUSD into the card&apos;s lending pool and earn the interest cardholders pay on their card debt.
          Interest accrues every minute and compounds into the value of your {SHARE_CODE} pool tokens.
        </p>
        {vault && (
          <div className="grid grid-3">
            <div className="stat">
              <label>On 1,000 RLUSD</label>
              <strong>≈ {amount(1000 * apy)} RLUSD / year</strong>
              <span className="stat-sub">at today&apos;s rate</span>
            </div>
            <div className="stat">
              <label>Rate</label>
              <strong>{percent(apr)} APR</strong>
              <span className="stat-sub">
                {"Variable: cardholders pay "}
                {pct(vault.borrowAprBps)}
                {", "}
                {pct(vault.utilizationBps)}
                {" of the pool is lent out, and the TEE keeps "}
                {pct(vault.reserveFactorBps)}
                {" of the interest."}
              </span>
            </div>
            <div className="stat">
              <label>Pool</label>
              <strong>{amount(vault.totalAssetsRlusd)} RLUSD</strong>
              <span className="stat-sub">
                1 {SHARE_CODE} = {amount(vault.sharePrice)} RLUSD
              </span>
            </div>
          </div>
        )}
      </section>

      <section className="panel wallet-panel" data-connected={wallet ? "true" : "false"}>
        <div className="row" style={{ justifyContent: "space-between" }}>
          <div>
            <h2>{wallet ? "Wallet connected" : "Connect your hardware wallet"}</h2>
            {wallet ? (
              <p className="lead">
                <span className="wallet-dot" aria-hidden /> {wallet.kind === "ledger" ? "Ledger" : "Xaman"} ·{" "}
                <a className="mono" href={xrplAccountUrl(walletAddress(wallet))} target="_blank" rel="noopener noreferrer" title={walletAddress(wallet)}>
                  {shortAddress(walletAddress(wallet))}
                </a>{" "}
                on {NETWORK_NAME}
              </p>
            ) : (
              <p className="lead">
                Plug in your Ledger, unlock it and open the XRP app. Every transaction is shown on the device for you to
                confirm; the key never leaves it.
              </p>
            )}
          </div>
          {wallet && (
            <button
              type="button"
              className="secondary"
              disabled={!!busy}
              onClick={async () => {
                if (wallet.kind === "ledger") await disconnectLedger();
                use(null);
              }}
            >
              Disconnect
            </button>
          )}
        </div>
        {!wallet && (
          <div className="row">
            <LedgerButton
              className="wallet-connect"
              disabled={!!busy}
              onError={setError}
              onConnected={(acct) => {
                setError(null);
                use({ kind: "ledger", account: acct });
                setMessage({ text: `Ledger connected: ${shortAddress(acct.address)}` });
              }}
            />
            <button
              type="button"
              className="secondary"
              disabled={!!busy}
              onClick={() =>
                run("xaman", async () => {
                  const addr = await xaman.signIn(signInBody());
                  forgetLedger(addr);
                  use({ kind: "xaman", address: addr });
                  return { text: `Xaman connected: ${shortAddress(addr)}` };
                })
              }
            >
              {busy === "xaman" ? "Waiting for Xaman…" : "Use Xaman instead"}
            </button>
          </div>
        )}
        {!wallet && !hid && (
          <p className="err">This browser cannot reach a Ledger over USB. Use Chrome, Edge or Brave, or Xaman.</p>
        )}
        {xaman.pending && <XamanQr pending={xaman.pending} onCancel={xaman.cancel} />}
        <LedgerPrompt active={onLedger} />
        {error && <p className="err">{error}</p>}
        {message && (
          <p className="okmsg">
            {message.text}
            {message.tx && (
              <>
                {" "}
                <a className="mono" href={`${EXPLORER_TX}/${message.tx}`} target="_blank" rel="noopener noreferrer">
                  {shortHash(message.tx)}
                </a>
              </>
            )}
          </p>
        )}
      </section>

      {wallet && vault && (
        <section className="panel">
          <h2>Deposit</h2>
          {!funded && (
            <p className="err">
              {shortAddress(walletAddress(wallet))} is not on {NETWORK_NAME} yet: it needs XRP first (
              <Link href="/faucets">Faucets</Link>).
            </p>
          )}
          <ol className="yield-steps">
            <li data-done={rlusdLine ? "true" : "false"}>
              <div>
                <strong>1. Trust RLUSD</strong>
                <span className="muted">Lets your account hold RLUSD from issuer {shortAddress(RLUSD.issuer)}.</span>
              </div>
              {rlusdLine ? (
                <span className="badge ok">Done</span>
              ) : (
                <button
                  type="button"
                  className="secondary"
                  disabled={!!busy || !funded}
                  onClick={() => run("rlusd-line", async () => ({ text: "RLUSD trust line added", tx: await sign(rlusdTrustSetBody()) }))}
                >
                  {busy === "rlusd-line" ? "Signing…" : "Add RLUSD trust line"}
                </button>
              )}
            </li>
            <li data-done={shareLine ? "true" : "false"}>
              <div>
                <strong>2. Trust the {SHARE_CODE} pool token</strong>
                <span className="muted">Lets your account receive the token that represents your share of the pool.</span>
              </div>
              {shareLine ? (
                <span className="badge ok">Done</span>
              ) : (
                <button
                  type="button"
                  className="secondary"
                  disabled={!!busy || !funded}
                  onClick={() =>
                    run("share-line", async () => ({
                      text: `${SHARE_CODE} trust line added`,
                      tx: await sign(shareTrustSetBody({ currency: vault.shareCurrency, issuer: vault.lpDepositAccount })),
                    }))
                  }
                >
                  {busy === "share-line" ? "Signing…" : `Add ${SHARE_CODE} trust line`}
                </button>
              )}
            </li>
            <li data-done="false">
              <div>
                <strong>3. Deposit RLUSD</strong>
                <span className="muted">
                  Balance {amount(balance)} RLUSD
                  {rlusdLine && balance === 0 && (
                    <>
                      {" "}
                      · <Link href="/faucets">get demo RLUSD</Link>
                    </>
                  )}
                  {toDeposit > 0 && ` · you receive ≈ ${amount(toDeposit / price)} ${SHARE_CODE}`}
                </span>
              </div>
              <div className="row yield-deposit">
                <input
                  type="number"
                  min="0"
                  step="0.000001"
                  value={depositAmount}
                  onChange={(e) => setDepositAmount(e.target.value)}
                  disabled={!!busy}
                  aria-label="RLUSD to deposit"
                />
                <button
                  type="button"
                  disabled={!!busy || !rlusdLine || !shareLine || !tag || !(toDeposit > 0) || toDeposit > balance}
                  onClick={() =>
                    run("deposit", async () => ({
                      text: `Deposited ${amount(depositAmount)} RLUSD; your ${SHARE_CODE} arrive in a few seconds`,
                      tx: await sign(rlusdTaggedPaymentBody({ destination: vault.lpDepositAccount, destinationTag: tag!, value: depositAmount })),
                    }))
                  }
                >
                  {busy === "deposit" ? "Signing…" : "Deposit"}
                </button>
              </div>
            </li>
          </ol>
          {toDeposit > balance && rlusdLine && <p className="muted">That is more than your RLUSD balance.</p>}
        </section>
      )}

      {wallet && vault && shares > 0 && (
        <section className="panel">
          <h2>Your earnings</h2>
          <div className="grid grid-3">
            <div className="stat">
              <label>Worth now</label>
              <strong>{amount(worth)} RLUSD</strong>
              <span className="stat-sub">
                {amount(shares)} {SHARE_CODE}
              </span>
            </div>
            <div className="stat">
              <label>Earned so far</label>
              <strong>{amount(earned)} RLUSD</strong>
              <span className="stat-sub">grows every minute</span>
            </div>
            <div className="stat">
              <label>At today&apos;s APY</label>
              <strong>≈ {amount(worth * apy)} RLUSD / year</strong>
              <span className="stat-sub">
                withdraw on <Link href="/lp/liquidity">Liquidity</Link>
              </span>
            </div>
          </div>
        </section>
      )}
    </>
  );
}

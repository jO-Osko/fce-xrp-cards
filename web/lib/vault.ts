/** The TEE lending pool (vault) and LP positions. */
import { teeCard } from "./api";

export type LPDeposit = { txHash: string; rlusdAmount: string; shares: string; at: number };
export type LPRedemption = { txHash: string; shares: string; rlusdAmount: string; at: number };

export type Vault = {
  totalAssetsRlusd: string;
  cashRlusd: string;
  freeLiquidityRlusd: string;
  cardDebtRlusd: string;
  owedToMerchantsRlusd: string;
  utilizationBps: number;
  borrowAprBps: number;
  supplyAprBps: number;
  reserveFactorBps: number;
  cardFeeBps: number;
  totalShares: string;
  sharePrice: string;
  internalShares: string;
  internalLiquidityRlusd: string;
  lpShares: string;
  lpLiquidityRlusd: string;
  lpCount: number;
  interestEarnedRlusd: string;
  interestToLpsRlusd: string;
  treasuryOwedRlusd: string;
  treasuryPaidRlusd: string;
  feesEarnedRlusd: string;
  interestReserveRlusd: string;
  lpDepositAccount: string;
  poolAccount: string;
  treasuryAccount: string;
  operatorAccount: string;
  shareCurrency: string;
  position?: {
    tag: number;
    xrplAddress: string;
    sharesIssued: string;
    sharesRedeemed: string;
    deposits: LPDeposit[];
    redemptions: LPRedemption[];
  };
};

export type LPJob = {
  id: string;
  kind: "issue" | "payout" | "return";
  to: string;
  shares?: string;
  amount?: string;
  sourceTx: string;
  status: "pending" | "done";
  txHash?: string;
  error?: string;
  reason?: string;
  createdAt: number;
};

/** TEE share units → displayed shares (1e6 units per share). */
export const sharesFromUnits = (units: string | number) => Number(units) / 1e6;

/** The pool accrues interest every minute, so its APR compounds per minute. */
export const apyFromApr = (apr: number) => Math.pow(1 + apr / 525_600, 525_600) - 1;

export const pct = (bps: number) => `${(bps / 100).toFixed(2).replace(/\.?0+$/, "")}%`;

export async function fetchVault(xrplAddress?: string) {
  return teeCard<Vault>("GET_VAULT", xrplAddress ? { xrplAddress } : {});
}

export async function lpRegister(xrplAddress: string) {
  return teeCard<{ tag: number; xrplAddress: string; depositAccount: string; shareCurrency: string }>(
    "LP_REGISTER",
    { xrplAddress },
  );
}

async function watcher<T>(method: "GET" | "POST", path: string, body?: unknown): Promise<T> {
  const res = await fetch(`/api/watcher/${path}`, {
    method,
    headers: body ? { "Content-Type": "application/json" } : undefined,
    body: body ? JSON.stringify(body) : undefined,
    cache: "no-store",
  });
  const json = (await res.json()) as { ok: boolean; error?: string } & T;
  if (!json.ok) throw new Error(json.error || `${path} failed`);
  return json;
}

/** Share balance and opt-in state from XRPL, plus the watcher's jobs for the address. */
export async function lpPosition(address: string) {
  return watcher<{ authorized: boolean; shares: string; jobs: LPJob[] }>(
    "GET",
    `lp/position?address=${encodeURIComponent(address)}`,
  );
}

const LP_SESSION = "xrpl-cc-lp-session";

/** The LP session is separate from the card session. */
export function loadLPSession(): { xrplAddress: string } | null {
  try {
    const raw = localStorage.getItem(LP_SESSION);
    return raw ? (JSON.parse(raw) as { xrplAddress: string }) : null;
  } catch {
    return null;
  }
}

export function saveLPSession(s: { xrplAddress: string } | null) {
  try {
    if (s) localStorage.setItem(LP_SESSION, JSON.stringify(s));
    else localStorage.removeItem(LP_SESSION);
  } catch {
    /* storage unavailable */
  }
}

export type Allocation = {
  address: string;
  unsecuredBps: number;
  updatedAt: number | null;
  round: { projectedApyBps: number; lpCount: number; pledgedRlusd: string };
};

/** An LP's off-chain pledge to the next round's uncollateralized protocol, with the round's totals. */
export async function fetchAllocation(address: string) {
  return watcher<Allocation>("GET", `lp/allocation?address=${encodeURIComponent(address)}`);
}

/** Saves the share (basis points) of the position marked for the uncollateralized protocol. Nothing is signed. */
export async function saveAllocation(address: string, unsecuredBps: number, positionRlusd: number) {
  return watcher<Allocation>("POST", "lp/allocation", { address, unsecuredBps, positionRlusd: positionRlusd.toFixed(6) });
}

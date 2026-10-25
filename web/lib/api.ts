import { CARD_CREDENTIAL, TEE_BROWSER_PATH } from "./config";

export type CardResponse<T> = {
  ok: boolean;
  command?: string;
  error?: string;
  data?: T;
  /** FCC direct-action id when TEE_MODE=fcc */
  actionId?: string;
};

export async function teeCard<T>(
  command: string,
  body: Record<string, unknown> = {},
): Promise<T & { actionId?: string }> {
  const res = await fetch(TEE_BROWSER_PATH, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Card-Credential": CARD_CREDENTIAL,
    },
    body: JSON.stringify({
      command,
      credential: CARD_CREDENTIAL,
      ...body,
    }),
    cache: "no-store",
  });
  const json = (await res.json()) as CardResponse<T>;
  if (!json.ok || !json.data) {
    throw new Error(json.error || `TEE ${command} failed`);
  }
  if (json.actionId && typeof json.data === "object" && json.data !== null) {
    return { ...(json.data as T), actionId: json.actionId };
  }
  return json.data as T & { actionId?: string };
}

export async function watcherSettle() {
  // Same-origin proxy — the browser used to call WATCHER_URL/settle directly and
  // often timed out while XRPL + SETTLE_CONFIRM were still finishing.
  const res = await fetch("/api/settle", { method: "POST", cache: "no-store" });
  const json = await res.json();
  if (!json.ok) {
    throw new Error(json.error || "settle failed");
  }
  return json as {
    ok: true;
    rlusdAmount: string;
    txHash: string;
    explorerUrl: string;
    settlementAddress: string;
    wireQueued: boolean;
    settleDailyActionId?: string;
    settleConfirmActionId?: string;
    settleDailyFlareTx?: string;
    settleConfirmFlareTx?: string;
  };
}

export type PoolInfo = {
  address: string;
  balanceRlusd: string | null;
  rlusd: { currency: string; issuer: string };
  network: string;
  note: string;
};

export type SettlementStatus = {
  virtualSettlementRlusd: string;
  pendingSettleRlusd: string;
  pendingSettleNonce: string;
  lastSettleTx?: string;
  settlementAddress?: string;
  /** TEE profit (fees, interest cut) a settlement would pay the treasury, even with no card spend. */
  treasuryOwedRlusd?: string;
};

/** Demo: the operator adds liquidity as an LP deposit (mock issuer mints it under the operator's tag). */
export async function mintPoolRlusd(amount: string) {
  const res = await fetch("/api/pool/mint", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ amount }),
    cache: "no-store",
  });
  const json = (await res.json()) as { ok: boolean; error?: string; txHash?: string };
  if (!json.ok) throw new Error(json.error || "mint failed");
  return json.txHash!;
}

export async function watcherPool(): Promise<PoolInfo> {
  const res = await fetch("/api/pool", { cache: "no-store" });
  const text = await res.text();
  let json: { ok: boolean; error?: string } & Partial<PoolInfo>;
  try {
    json = JSON.parse(text) as { ok: boolean; error?: string } & Partial<PoolInfo>;
  } catch {
    throw new Error(
      res.ok
        ? "pool info returned invalid JSON"
        : `pool info unavailable (${res.status})`,
    );
  }
  if (!json.ok || !json.address) {
    throw new Error(json.error || "pool info unavailable");
  }
  return {
    address: json.address,
    balanceRlusd:
      typeof json.balanceRlusd === "string" ? json.balanceRlusd : null,
    rlusd: json.rlusd!,
    network: json.network!,
    note: json.note!,
  };
}

/** Pending daily settlement pile from TEE GET /state (mock) or GET_POSITION (FCC). */
export async function fetchSettlementStatus(tag?: number): Promise<SettlementStatus> {
  const status = await cardSettlementStatus(tag);
  try {
    const vault = await teeCard<{ treasuryOwedRlusd: string }>("GET_VAULT", {});
    return { ...status, treasuryOwedRlusd: vault.treasuryOwedRlusd };
  } catch {
    return status;
  }
}

async function cardSettlementStatus(
  tag?: number,
): Promise<SettlementStatus> {
  const res = await fetch("/api/tee-state", { cache: "no-store" });
  if (res.ok) {
    const json = (await res.json()) as {
      ok: boolean;
      virtualSettlementRlusd?: string;
      pendingSettleRlusd?: string;
      pendingSettleNonce?: string;
      error?: string;
    };
    if (json.ok && typeof json.virtualSettlementRlusd === "string") {
      return {
        virtualSettlementRlusd: json.virtualSettlementRlusd,
        pendingSettleRlusd: json.pendingSettleRlusd || "",
        pendingSettleNonce: json.pendingSettleNonce || "",
      };
    }
  }

  if (tag == null) {
    throw new Error("settlement status unavailable (no TEE state or session)");
  }
  const pos = await teeCard<{
    virtualSettlementRlusd: string;
    pendingSettleRlusd?: string;
    pendingSettleNonce?: string;
    lastSettleTx?: string;
    settlementAddress?: string;
  }>("GET_POSITION", { tag });
  return {
    virtualSettlementRlusd: pos.virtualSettlementRlusd,
    pendingSettleRlusd: pos.pendingSettleRlusd || "",
    pendingSettleNonce: pos.pendingSettleNonce || "",
    lastSettleTx: pos.lastSettleTx || "",
    settlementAddress: pos.settlementAddress || "",
  };
}

export function rlusdPositive(amount: string | null | undefined): boolean {
  if (!amount) return false;
  const n = Number(amount);
  return Number.isFinite(n) && n > 0;
}

export function dropsToXrp(drops: string): string {
  const n = Number(drops) / 1_000_000;
  if (!Number.isFinite(n)) return drops;
  return n.toLocaleString(undefined, { maximumFractionDigits: 2 });
}

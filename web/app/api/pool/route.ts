import fs from "node:fs";
import path from "node:path";
import { NextResponse } from "next/server";

import { NETWORK_NAME, RLUSD } from "@/lib/network";

const RLUSD_CURRENCY = RLUSD.currency;
const RLUSD_ISSUER = RLUSD.issuer;
const NOTE = `${NETWORK_NAME} demo RLUSD comes from a mock issuer; add liquidity with an operator deposit.`;

function fromWalletsFile(): string | null {
  try {
    const file = path.join(process.cwd(), "..", "watcher", "wallets.json");
    const w = JSON.parse(fs.readFileSync(file, "utf8")) as {
      pool?: { address?: string };
    };
    return w.pool?.address || null;
  } catch {
    return null;
  }
}

/** The pool's address and RLUSD balance, read by the watcher over its one XRPL connection. */
async function fromWatcher(): Promise<{ address: string; balanceRlusd: string | null } | null> {
  const base =
    process.env.WATCHER_URL ||
    process.env.NEXT_PUBLIC_WATCHER_URL ||
    "http://127.0.0.1:8090";
  try {
    const res = await fetch(`${base}/pool`, { cache: "no-store" });
    const json = JSON.parse(await res.text()) as {
      ok?: boolean;
      address?: string;
      balanceRlusd?: string | null;
    };
    if (json?.ok && json.address) {
      return { address: json.address, balanceRlusd: json.balanceRlusd ?? null };
    }
  } catch {
    // watcher down, or not JSON
  }
  return null;
}

export async function GET() {
  const watcher = await fromWatcher();
  const address =
    process.env.XRPL_POOL_ADDRESS ||
    process.env.NEXT_PUBLIC_XRPL_POOL_ADDRESS ||
    watcher?.address ||
    fromWalletsFile();

  if (!address) {
    return NextResponse.json(
      {
        ok: false,
        error:
          "Pool address unknown — set XRPL_POOL_ADDRESS or restart watcher with XRPL_POOL_SEED",
      },
      { status: 503 },
    );
  }

  return NextResponse.json({
    ok: true,
    address,
    // Only the watcher reads the ledger; without it the balance is unknown.
    balanceRlusd: watcher && watcher.address === address ? watcher.balanceRlusd : null,
    rlusd: { currency: RLUSD_CURRENCY, issuer: RLUSD_ISSUER },
    network: NETWORK_NAME,
    note: NOTE,
  });
}

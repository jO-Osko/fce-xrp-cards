import { NextResponse } from "next/server";

const TEE_URL =
  process.env.TEE_CARD_URL ||
  process.env.NEXT_PUBLIC_TEE_CARD_URL ||
  "http://127.0.0.1:8080";

const TEE_MODE = (process.env.TEE_MODE || "mock").toLowerCase();

type TeeState = {
  virtualSettlementRlusd: string;
  pendingSettleRlusd?: string;
  pendingSettleNonce?: string;
  userCount?: number;
};

/**
 * Proxies TEE GET /state (mock extension). FCC tee-proxy does not expose /state;
 * the settle page falls back to GET_POSITION in that case.
 */
export async function GET() {
  if (TEE_MODE === "fcc") {
    return NextResponse.json(
      {
        ok: false,
        error: "tee-state unavailable in FCC mode — use GET_POSITION",
        mode: "fcc",
      },
      { status: 501 },
    );
  }

  try {
    const upstream = await fetch(`${TEE_URL}/state`, { cache: "no-store" });
    const text = await upstream.text();
    let json: {
      state?: TeeState;
      State?: TeeState;
    };
    try {
      json = JSON.parse(text) as typeof json;
    } catch {
      return NextResponse.json(
        { ok: false, error: "tee /state returned invalid JSON" },
        { status: 502 },
      );
    }
    const state = json.state ?? json.State;
    if (!state || typeof state.virtualSettlementRlusd !== "string") {
      return NextResponse.json(
        { ok: false, error: "tee /state missing virtualSettlementRlusd" },
        { status: 502 },
      );
    }
    return NextResponse.json({
      ok: true,
      mode: "mock",
      virtualSettlementRlusd: state.virtualSettlementRlusd,
      pendingSettleRlusd: state.pendingSettleRlusd || "",
      pendingSettleNonce: state.pendingSettleNonce || "",
      userCount: state.userCount ?? 0,
    });
  } catch (e) {
    return NextResponse.json(
      {
        ok: false,
        error: e instanceof Error ? e.message : String(e),
      },
      { status: 502 },
    );
  }
}

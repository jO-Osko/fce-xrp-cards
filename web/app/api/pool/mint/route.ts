import { NextResponse } from "next/server";

const WATCHER =
  process.env.WATCHER_URL ||
  process.env.NEXT_PUBLIC_WATCHER_URL ||
  "http://127.0.0.1:8090";

/** Proxies the demo operator deposit: mint RLUSD into the pool as the operator's LP deposit. */
export async function POST(req: Request) {
  try {
    const upstream = await fetch(`${WATCHER}/operator/deposit`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: await req.text(),
      cache: "no-store",
    });
    return new NextResponse(await upstream.text(), {
      status: upstream.status,
      headers: { "Content-Type": "application/json" },
    });
  } catch (e) {
    return NextResponse.json(
      { ok: false, error: e instanceof Error ? e.message : String(e) },
      { status: 502 },
    );
  }
}

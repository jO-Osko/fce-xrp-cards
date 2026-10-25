import { NextResponse } from "next/server";

const WATCHER =
  process.env.WATCHER_URL ||
  process.env.NEXT_PUBLIC_WATCHER_URL ||
  "http://127.0.0.1:8090";

/** All daily settlements recorded by the watcher (plus XRPL backfill). */
export async function GET() {
  try {
    const upstream = await fetch(
      `${WATCHER.replace(/\/$/, "")}/settlements`,
      { cache: "no-store", signal: AbortSignal.timeout(30_000) },
    );
    const text = await upstream.text();
    return new NextResponse(text, {
      status: upstream.status,
      headers: { "Content-Type": "application/json" },
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

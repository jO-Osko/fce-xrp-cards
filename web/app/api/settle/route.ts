import { NextResponse } from "next/server";

const WATCHER =
  process.env.WATCHER_URL ||
  process.env.NEXT_PUBLIC_WATCHER_URL ||
  "http://127.0.0.1:8090";

/** Same-origin proxy so the browser does not wait on a cross-origin /settle. */
export async function POST() {
  try {
    const upstream = await fetch(`${WATCHER.replace(/\/$/, "")}/settle`, {
      method: "POST",
      cache: "no-store",
      signal: AbortSignal.timeout(120_000),
    });
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

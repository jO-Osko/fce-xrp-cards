import { NextRequest, NextResponse } from "next/server";

const WATCHER = (
  process.env.WATCHER_URL ||
  process.env.NEXT_PUBLIC_WATCHER_URL ||
  "http://127.0.0.1:8090"
).replace(/\/$/, "");

// Watcher routes the browser may reach (loan conversion, loans, faucet, mock broker).
const ALLOWED: Array<[string, RegExp]> = [
  ["POST", /^convert$/],
  ["POST", /^conversions\/[^/]+\/resume$/],
  ["GET", /^conversions$/],
  ["GET", /^loans$/],
  ["GET", /^loans\/payments$/],
  ["POST", /^loans\/pay$/],
  ["POST", /^rlusd\/mint-user$/],
  ["GET", /^rlusd\/account$/],
  ["POST", /^hw\/prepare$/],
  ["POST", /^hw\/submit$/],
  ["GET", /^lp\/position$/],
  ["GET", /^lp\/allocation$/],
  ["POST", /^lp\/allocation$/],
  ["POST", /^operator\/deposit$/],
  ["GET", /^broker\/log$/],
];

async function proxy(req: NextRequest, path: string[]) {
  const sub = path.map(encodeURIComponent).join("/");
  if (!ALLOWED.some(([m, re]) => m === req.method && re.test(sub))) {
    return NextResponse.json({ ok: false, error: "not found" }, { status: 404 });
  }
  try {
    const upstream = await fetch(`${WATCHER}/${sub}${req.nextUrl.search}`, {
      method: req.method,
      headers: { "Content-Type": "application/json" },
      body: req.method === "POST" ? await req.text() : undefined,
      cache: "no-store",
      // A conversion runs several XRPL transactions in a row.
      signal: AbortSignal.timeout(180_000),
    });
    return new NextResponse(await upstream.text(), {
      status: upstream.status,
      headers: { "Content-Type": "application/json" },
    });
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e);
    // fetch reports a watcher that is down (or still starting up) as "fetch failed".
    return NextResponse.json(
      { ok: false, error: /fetch failed|ECONNREFUSED/.test(msg) ? "Watcher unreachable: is it running?" : msg },
      { status: 502 },
    );
  }
}

type Ctx = { params: Promise<{ path: string[] }> };

export async function GET(req: NextRequest, ctx: Ctx) {
  return proxy(req, (await ctx.params).path);
}

export async function POST(req: NextRequest, ctx: Ctx) {
  return proxy(req, (await ctx.params).path);
}

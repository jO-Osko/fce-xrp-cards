import { NextRequest, NextResponse } from "next/server";
import { SOURCE_TAG } from "@/lib/network";

const XAMAN_API_BASE = "https://xumm.app/api/v1/platform";

export async function POST(req: NextRequest) {
  const apiKey = process.env.XAMAN_API_KEY;
  const apiSecret = process.env.XAMAN_API_SECRET;

  if (!apiKey || !apiSecret) {
    return NextResponse.json(
      { error: "Xaman API credentials not configured" },
      { status: 500 },
    );
  }

  let body: unknown;
  try {
    body = await req.json();
  } catch {
    return NextResponse.json({ error: "Invalid request body" }, { status: 400 });
  }

  // Every transaction a user signs through the app carries the app's SourceTag
  // (app.config.json); a SignIn is not a transaction.
  const txjson = (body as { txjson?: Record<string, unknown> } | null)?.txjson;
  if (txjson && txjson.TransactionType !== "SignIn") txjson.SourceTag = SOURCE_TAG;

  const response = await fetch(`${XAMAN_API_BASE}/payload`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "x-api-key": apiKey,
      "x-api-secret": apiSecret,
    },
    body: JSON.stringify(body),
  });

  const data = await response.json();

  if (!response.ok) {
    return NextResponse.json(
      { error: data?.error?.reference ?? "Failed to create Xaman payload" },
      { status: response.status },
    );
  }

  return NextResponse.json(data);
}

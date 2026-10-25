import { NextRequest, NextResponse } from "next/server";
import { fccCard } from "@/lib/fcc-direct";

const TEE_URL =
  process.env.TEE_CARD_URL ||
  process.env.NEXT_PUBLIC_TEE_CARD_URL ||
  "http://127.0.0.1:8080";

/** fcc = live tee-proxy POST /direct; mock = local go/ POST /card */
const TEE_MODE = (process.env.TEE_MODE || "mock").toLowerCase();

export async function POST(req: NextRequest) {
  const bodyText = await req.text();
  const credential =
    req.headers.get("x-card-credential") ||
    process.env.NEXT_PUBLIC_CARD_CREDENTIAL ||
    "swell-demo-card-credential";

  let body: Record<string, unknown>;
  try {
    body = JSON.parse(bodyText) as Record<string, unknown>;
  } catch {
    return NextResponse.json({ ok: false, error: "invalid JSON" }, { status: 400 });
  }
  if (!body.credential) body.credential = credential;

  if (TEE_MODE === "fcc") {
    const apiKey = process.env.TEE_DIRECT_API_KEY || "12345";
    const { status, json } = await fccCard(
      { proxyUrl: TEE_URL, apiKey },
      body,
    );
    return NextResponse.json(json, { status });
  }

  try {
    const upstream = await fetch(`${TEE_URL}/card`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Card-Credential": credential,
      },
      body: JSON.stringify(body),
      cache: "no-store",
    });
    const text = await upstream.text();
    return new NextResponse(text, {
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

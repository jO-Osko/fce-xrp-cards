import { NextRequest, NextResponse } from "next/server";

const XAMAN_API_BASE = "https://xumm.app/api/v1/platform";

export async function GET(
  _req: NextRequest,
  { params }: { params: Promise<{ uuid: string }> },
) {
  const apiKey = process.env.XAMAN_API_KEY;
  const apiSecret = process.env.XAMAN_API_SECRET;

  if (!apiKey || !apiSecret) {
    return NextResponse.json(
      { error: "Xaman API credentials not configured" },
      { status: 500 },
    );
  }

  const { uuid } = await params;

  const response = await fetch(`${XAMAN_API_BASE}/payload/${uuid}`, {
    headers: {
      "x-api-key": apiKey,
      "x-api-secret": apiSecret,
    },
  });

  const data = await response.json();

  if (!response.ok) {
    return NextResponse.json(
      { error: data?.error?.reference ?? "Failed to fetch payload status" },
      { status: response.status },
    );
  }

  return NextResponse.json(data);
}

/**
 * Talk to a live tee-proxy CARD extension via POST /direct + poll result.
 * Maps the mock TEE's {ok,command,data} envelope so /api/card stays stable.
 */

function bytes32(s: string): string {
  const buf = Buffer.from(s, "utf8");
  if (buf.length > 32) throw new Error(`op name too long: ${s}`);
  return "0x" + Buffer.concat([buf, Buffer.alloc(32 - buf.length)]).toString("hex");
}

function sleep(ms: number) {
  return new Promise((r) => setTimeout(r, ms));
}

export type FccCardEnv = {
  proxyUrl: string;
  apiKey: string;
  timeoutMs?: number;
};

export async function fccCard(
  env: FccCardEnv,
  body: Record<string, unknown>,
): Promise<{ status: number; json: unknown }> {
  const command = String(body.command || "");
  if (!command) {
    return { status: 400, json: { ok: false, error: "command required" } };
  }

  const messageObj = { ...body };
  // Handlers set Command from opCommand; keep payload fields only.
  delete messageObj.command;
  const messageHex =
    "0x" + Buffer.from(JSON.stringify(messageObj), "utf8").toString("hex");

  const base = env.proxyUrl.replace(/\/$/, "");
  const directBody = {
    opType: bytes32("CARD"),
    opCommand: bytes32(command),
    message: messageHex,
  };

  const queued = await fetch(`${base}/direct`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Api-Key": env.apiKey,
    },
    body: JSON.stringify(directBody),
    cache: "no-store",
  });
  if (!queued.ok) {
    const text = await queued.text();
    return {
      status: 502,
      json: { ok: false, command, error: `direct ${queued.status}: ${text.slice(0, 200)}` },
    };
  }
  const q = (await queued.json()) as { data?: { id?: string } };
  const id = q.data?.id;
  if (!id) {
    return { status: 502, json: { ok: false, command, error: "direct returned no id" } };
  }

  const deadline = Date.now() + (env.timeoutMs ?? 60_000);
  while (Date.now() < deadline) {
    const res = await fetch(
      `${base}/action/result/${id}?submissionTag=submit`,
      { cache: "no-store" },
    );
    if (res.status === 404) {
      await sleep(500);
      continue;
    }
    if (!res.ok) {
      const text = await res.text();
      return {
        status: 502,
        json: { ok: false, command, error: `result ${res.status}: ${text.slice(0, 200)}` },
      };
    }
    const payload = (await res.json()) as {
      result?: { status?: number; log?: string; data?: string };
    };
    const r = payload.result;
    if (!r || r.status === undefined || r.status >= 2) {
      await sleep(500);
      continue;
    }
    if (r.status !== 1) {
      return {
        status: 400,
        json: { ok: false, command, error: r.log || `status ${r.status}` },
      };
    }
    let data: unknown = null;
    if (typeof r.data === "string" && r.data.startsWith("0x")) {
      data = JSON.parse(Buffer.from(r.data.slice(2), "hex").toString("utf8"));
    }
    // Direct-action id (bytes32). Shown in the UI as a Coston2 TEE proof link.
    return { status: 200, json: { ok: true, command, data, actionId: id } };
  }
  return { status: 504, json: { ok: false, command, error: "timed out waiting for TEE result" } };
}

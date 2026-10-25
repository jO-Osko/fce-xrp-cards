const TEE_URL = process.env.TEE_CARD_URL || "http://127.0.0.1:8080";
const CREDENTIAL =
  process.env.CARD_PROCESSOR_CREDENTIAL || "swell-demo-card-credential";
const TEE_MODE = (process.env.TEE_MODE || "mock").toLowerCase();
const DIRECT_API_KEY = process.env.TEE_DIRECT_API_KEY || "12345";

function bytes32(s) {
  const buf = Buffer.from(s, "utf8");
  if (buf.length > 32) throw new Error(`op name too long: ${s}`);
  return "0x" + Buffer.concat([buf, Buffer.alloc(32 - buf.length)]).toString("hex");
}

function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms));
}

async function cardFcc(command, body = {}) {
  const messageObj = { credential: CREDENTIAL, ...body };
  const messageHex =
    "0x" + Buffer.from(JSON.stringify(messageObj), "utf8").toString("hex");
  const base = TEE_URL.replace(/\/$/, "");

  const queued = await fetch(`${base}/direct`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Api-Key": DIRECT_API_KEY,
    },
    body: JSON.stringify({
      opType: bytes32("CARD"),
      opCommand: bytes32(command),
      message: messageHex,
    }),
  });
  if (!queued.ok) {
    throw new Error(`direct ${queued.status}: ${(await queued.text()).slice(0, 200)}`);
  }
  const { data } = await queued.json();
  const id = data?.id;
  if (!id) throw new Error("direct returned no id");

  const deadline = Date.now() + 60_000;
  while (Date.now() < deadline) {
    const res = await fetch(`${base}/action/result/${id}?submissionTag=submit`);
    if (res.status === 404) {
      await sleep(500);
      continue;
    }
    if (!res.ok) {
      throw new Error(`result ${res.status}: ${(await res.text()).slice(0, 200)}`);
    }
    const payload = await res.json();
    const r = payload.result;
    if (!r || r.status === undefined || r.status >= 2) {
      await sleep(500);
      continue;
    }
    if (r.status !== 1) {
      throw new Error(r.log || `TEE ${command} status ${r.status}`);
    }
    let data = r.data;
    if (typeof data === "string" && data.startsWith("0x")) {
      data = JSON.parse(Buffer.from(data.slice(2), "hex").toString("utf8"));
    }
    if (data && typeof data === "object") {
      return { ...data, actionId: id };
    }
    return { data, actionId: id };
  }
  throw new Error(`TEE ${command} timed out`);
}

async function cardMock(command, body = {}) {
  const res = await fetch(`${TEE_URL}/card`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Card-Credential": CREDENTIAL,
    },
    body: JSON.stringify({ command, credential: CREDENTIAL, ...body }),
  });
  const json = await res.json();
  if (!json.ok) {
    throw new Error(json.error || `TEE ${command} failed`);
  }
  return json.data;
}

export async function card(command, body = {}) {
  if (TEE_MODE === "fcc") return cardFcc(command, body);
  return cardMock(command, body);
}

/**
 * On-chain CARD settle instructions via InstructionSender + tee-proxy result poll.
 * Used when TEE_MODE=fcc. Mock mode keeps POST /card.
 */
import { Contract, Interface, JsonRpcProvider, Wallet, getBytes, hexlify } from "ethers";

const TEE_URL = process.env.TEE_CARD_URL || "http://127.0.0.1:8080";
const RPC_URL =
  process.env.FLARE_RPC_URL || "https://coston2-api.flare.network/ext/C/rpc";
// Written to watcher/.env by scripts/sync-config.mjs from the fce deploy record.
const INSTRUCTION_SENDER = process.env.INSTRUCTION_SENDER || "";
const PRIVATE_KEY = process.env.FLARE_SETTLER_PRIVATE_KEY || "";
const POLL_TIMEOUT_MS = Number(process.env.FLARE_INSTRUCTION_TIMEOUT_MS || 180_000);

const OP_TYPE_CARD = hexlify(padRight(Buffer.from("CARD", "utf8"), 32));
const OP_SETTLE_DAILY = hexlify(padRight(Buffer.from("SETTLE_DAILY", "utf8"), 32));
const OP_SETTLE_CONFIRM = hexlify(
  padRight(Buffer.from("SETTLE_CONFIRM", "utf8"), 32),
);

const SENDER_ABI = [
  "function quoteFee(bytes32 opType, bytes32 opCommand) view returns (uint256)",
  "function settleDaily() payable returns (bytes32)",
  "function settleConfirm(string settleNonce, string txHash) payable returns (bytes32)",
  "event SettleDailySent(bytes32 indexed instructionId)",
  "event SettleConfirmSent(bytes32 indexed instructionId, string settleNonce, string txHash)",
];

function padRight(buf, len) {
  if (buf.length > len) throw new Error("bytes32 overflow");
  return Buffer.concat([buf, Buffer.alloc(len - buf.length)]);
}

function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms));
}

function senderContract() {
  if (!PRIVATE_KEY) {
    throw new Error(
      "FLARE_SETTLER_PRIVATE_KEY not set — required for FCC on-chain settle",
    );
  }
  if (!INSTRUCTION_SENDER) {
    throw new Error(
      "INSTRUCTION_SENDER not set — run scripts/sync-config.mjs after fce deploy",
    );
  }
  const provider = new JsonRpcProvider(RPC_URL);
  const wallet = new Wallet(PRIVATE_KEY, provider);
  return {
    contract: new Contract(INSTRUCTION_SENDER, SENDER_ABI, wallet),
    iface: new Interface(SENDER_ABI),
  };
}

/**
 * @param {"SETTLE_DAILY"|"SETTLE_CONFIRM"} command
 * @param {{ settleNonce?: string, txHash?: string }} [args]
 * @returns {Promise<{ data: object, actionId: string, flareTxHash: string }>}
 */
export async function settleInstruction(command, args = {}) {
  const { contract, iface } = senderContract();

  let tx;
  if (command === "SETTLE_DAILY") {
    const fee = await contract.quoteFee(OP_TYPE_CARD, OP_SETTLE_DAILY);
    tx = await contract.settleDaily({ value: fee });
  } else if (command === "SETTLE_CONFIRM") {
    if (!args.settleNonce || !args.txHash) {
      throw new Error("settleConfirm requires settleNonce and txHash");
    }
    const fee = await contract.quoteFee(OP_TYPE_CARD, OP_SETTLE_CONFIRM);
    tx = await contract.settleConfirm(args.settleNonce, args.txHash, {
      value: fee,
    });
  } else {
    throw new Error(`unsupported settle instruction: ${command}`);
  }

  const receipt = await tx.wait();
  const flareTxHash = receipt.hash;
  const instructionId = parseInstructionId(receipt, iface, command);
  if (!instructionId) {
    throw new Error(`${command}: no instruction id in receipt ${flareTxHash}`);
  }

  const data = await pollThresholdResult(instructionId, command);
  // Normalize ethers BytesLike / Result values to hex strings for JSON persistence.
  const actionId =
    typeof instructionId === "string"
      ? instructionId
      : hexlify(getBytes(instructionId));
  return {
    data,
    actionId,
    flareTxHash:
      typeof flareTxHash === "string" ? flareTxHash : hexlify(flareTxHash),
  };
}

function parseInstructionId(receipt, iface, command) {
  const eventName =
    command === "SETTLE_DAILY" ? "SettleDailySent" : "SettleConfirmSent";
  for (const log of receipt.logs || []) {
    if (log.address.toLowerCase() !== INSTRUCTION_SENDER.toLowerCase()) continue;
    try {
      const parsed = iface.parseLog({ topics: log.topics, data: log.data });
      if (parsed?.name === eventName) {
        return parsed.args.instructionId;
      }
    } catch {
      /* not our event */
    }
  }
  return null;
}

async function pollThresholdResult(instructionId, command) {
  const base = TEE_URL.replace(/\/$/, "");
  const id =
    typeof instructionId === "string"
      ? instructionId
      : hexlify(getBytes(instructionId));
  const deadline = Date.now() + POLL_TIMEOUT_MS;

  while (Date.now() < deadline) {
    const res = await fetch(
      `${base}/action/result/${id}?submissionTag=threshold`,
    );
    if (res.status === 404) {
      await sleep(1000);
      continue;
    }
    if (!res.ok) {
      throw new Error(
        `result ${res.status}: ${(await res.text()).slice(0, 200)}`,
      );
    }
    const payload = await res.json();
    const r = payload.result;
    if (!r || r.status === undefined || r.status >= 2) {
      await sleep(1000);
      continue;
    }
    if (r.status !== 1) {
      throw new Error(r.log || `TEE ${command} status ${r.status}`);
    }
    let data = r.data;
    if (typeof data === "string" && data.startsWith("0x")) {
      data = JSON.parse(Buffer.from(data.slice(2), "hex").toString("utf8"));
    }
    return data;
  }
  throw new Error(`TEE ${command} instruction timed out (${id})`);
}

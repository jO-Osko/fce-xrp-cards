/**
 * Live FCC (Coston2). Values come from deployment.gen.json, which
 * scripts/sync-config.mjs writes from the fce deploy record and fce run stack
 * (it runs before `npm run dev` / `npm run build`).
 */
import deployment from "./deployment.gen.json";

export const COSTON2_EXPLORER = "https://coston2-explorer.flare.network";

const d = deployment.fcc;

export const FCC = {
  chainId: d.chainId,
  extensionId: d.extensionId,
  extensionIdHex: "0x" + BigInt(d.extensionId).toString(16).padStart(64, "0"),
  /** InstructionSender — settleDaily / settleConfirm (redeploy when ABI changes). */
  instructionSender: d.instructionSender,
  flareTeeManager: d.flareTeeManager,
  /** Owner of InstructionSender; FLARE_SETTLER_PRIVATE_KEY must be this key. */
  deployer: d.deployer,
  /** Extension register on Coston2 (deployments/coston2.json) */
  registerTx: d.registerTx,
  /** Local fce-run machine → PRODUCTION */
  machineRegisterTx: d.machineRegisterTx,
  productionTx: d.productionTx,
  teeId: d.teeId,
  proxyId: d.proxyId,
  /** 0 NONE, 1 INITIALIZED, 2 PRODUCTION — instructions need 2 */
  machineStatus: d.productionTx ? "PRODUCTION" : d.teeId ? "INITIALIZED" : "NONE",
  proxyUrl: d.proxyUrl,
  tunnelUrl: d.tunnelUrl,
  version: d.version,
} as const;

/**
 * Whether a real TEE (FCC) is deployed on Coston2. The demo runs the mock TEE
 * and needs none of it; see the playbook's Part D to deploy one.
 */
export const FCC_DEPLOYED = !!d.instructionSender;

/** XRPL demo accounts from watcher/wallets.json. */
export const XRPL_ACCOUNTS = deployment.xrpl;

export function coston2TxUrl(txHash: string) {
  return `${COSTON2_EXPLORER}/tx/${txHash}`;
}

export function coston2AddressUrl(address: string) {
  return `${COSTON2_EXPLORER}/address/${address}`;
}

/** Tee-proxy result for a direct action (RESERVE, REGISTER, …). Not a Coston2 tx. */
export function teeDirectActionUrl(actionId: string) {
  const base = (FCC.tunnelUrl || FCC.proxyUrl).replace(/\/$/, "");
  const id = actionId.startsWith("0x") ? actionId : `0x${actionId}`;
  return `${base}/action/result/${id}?submissionTag=submit`;
}

export function shortHex(h: string, left = 6, right = 4) {
  const s = h.startsWith("0x") ? h.slice(2) : h;
  if (s.length <= left + right) return h.startsWith("0x") ? h : `0x${h}`;
  return `0x${s.slice(0, left)}…${s.slice(-right)}`;
}

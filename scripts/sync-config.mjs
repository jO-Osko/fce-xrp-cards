#!/usr/bin/env node
/**
 * Single sync step for demo configuration. Reads the three sources of truth
 * and writes every derived copy, so addresses never drift between components.
 *
 * Sources (never edited here):
 *   app.config.json                              app-wide settings: the XRPL SourceTag on every transaction
 *   watcher/wallets.json                         XRPL network, RLUSD issuer, deposit / settlement / pool wallets
 *   extension/template/deployments/<chain>.json  fce deploy record (InstructionSender, extension id, …)
 *   <user cache>/fce/run/<chain>-template-*      fce run stack (tunnel URL, machine ids, direct API key, proxy port)
 *
 * Outputs:
 *   extension/pkg/card/defaults_gen.go  TEE default deposit / settlement accounts and RLUSD issuer (part of the image!)
 *   web/lib/deployment.gen.json         addresses and FCC deployment shown by the web app
 *   watcher/.env, web/.env.local        only the keys listed below; other lines are kept
 *
 * Usage:
 *   node scripts/sync-config.mjs           write outputs
 *   node scripts/sync-config.mjs --check   exit 1 if any output is out of date (writes nothing)
 *
 * Env: FCE_CHAIN (default coston2), WALLETS_FILE, FCE_RUN_DIR (override the stack directory).
 */
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const CHAIN = process.env.FCE_CHAIN || "coston2";
const CHECK = process.argv.includes("--check");

const APP_CONFIG = path.join(ROOT, "app.config.json");
const WALLETS = process.env.WALLETS_FILE || path.join(ROOT, "watcher/wallets.json");
const DEPLOY_RECORD = path.join(ROOT, "extension/template/deployments", `${CHAIN}.json`);
const GO_GEN = path.join(ROOT, "extension/pkg/card/defaults_gen.go");
const WEB_GEN = path.join(ROOT, "web/lib/deployment.gen.json");
const WATCHER_ENV = path.join(ROOT, "watcher/.env");
const WATCHER_ENV_EXAMPLE = path.join(ROOT, "watcher/.env.example");
const WEB_ENV = path.join(ROOT, "web/.env.local");
const WEB_ENV_EXAMPLE = path.join(ROOT, "web/.env.example");

// tee-proxy's external API port inside the fce run stack (tooling/internal/stack).
const PROXY_EXTERNAL_PORT = 6662;

const warnings = [];
const stale = [];

function readJSON(file) {
  try {
    return JSON.parse(fs.readFileSync(file, "utf8"));
  } catch (err) {
    if (err.code === "ENOENT") return null;
    throw new Error(`reading ${file}: ${err.message}`);
  }
}

function rel(file) {
  return path.relative(ROOT, file);
}

function userCacheDir() {
  if (process.platform === "darwin") return path.join(os.homedir(), "Library/Caches");
  if (process.platform === "win32") return process.env.LocalAppData || os.homedir();
  return process.env.XDG_CACHE_HOME || path.join(os.homedir(), ".cache");
}

/** Newest fce run stack for the template on CHAIN, or null. */
function findStack() {
  if (process.env.FCE_RUN_DIR) return process.env.FCE_RUN_DIR;
  const base = path.join(userCacheDir(), "fce/run");
  let names;
  try {
    names = fs.readdirSync(base);
  } catch {
    return null;
  }
  const re = new RegExp(`^${CHAIN}-template-[0-9a-f]{8}$`);
  const dirs = names
    .filter((n) => re.test(n))
    .map((n) => path.join(base, n))
    .filter((d) => fs.existsSync(path.join(d, "state.json")))
    .sort((a, b) => fs.statSync(path.join(b, "state.json")).mtimeMs - fs.statSync(path.join(a, "state.json")).mtimeMs);
  return dirs[0] || null;
}

function readStack(dir) {
  const state = readJSON(path.join(dir, "state.json")) || {};
  let directKey = "";
  try {
    for (const line of fs.readFileSync(path.join(dir, "proxy.env"), "utf8").split("\n")) {
      if (line.startsWith("DIRECT_API_KEY=")) directKey = line.slice("DIRECT_API_KEY=".length).trim();
    }
  } catch {
    /* no proxy.env */
  }
  // The proxy's host port is picked by Docker, so ask Compose while the stack runs.
  let proxyUrl = "";
  try {
    const project = `fce-${path.basename(dir)}`;
    const out = execFileSync(
      "docker",
      ["compose", "-p", project, "-f", path.join(dir, "compose.json"), "port", "proxy", String(PROXY_EXTERNAL_PORT)],
      { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"], timeout: 10_000 },
    ).trim();
    if (out) proxyUrl = `http://${out.replace(/^0\.0\.0\.0:/, "127.0.0.1:")}`;
  } catch {
    warnings.push(`fce run stack ${path.basename(dir)} is not running; proxy URL not updated`);
  }
  return { state, directKey, proxyUrl };
}

// ---- gather -------------------------------------------------------------

const wallets = readJSON(WALLETS);
if (
  !wallets?.deposit?.address ||
  !wallets?.settlement?.address ||
  typeof wallets.network !== "object" ||
  !wallets.network.ws ||
  !wallets.rlusd?.issuer
) {
  console.error(
    `${rel(WALLETS)} missing or in the old format (no network block); run: cd watcher && npm run fund-wallets`,
  );
  process.exit(1);
}

const record = readJSON(DEPLOY_RECORD);
const stackDir = findStack();
const stack = stackDir ? readStack(stackDir) : null;
const prev = readJSON(WEB_GEN) || {};
const prevFcc = prev.fcc || {};

// The real TEE (FCC) is optional: the demo runs the mock TEE, which needs none of this.
if (!record) warnings.push(`no ${rel(DEPLOY_RECORD)}: real TEE not deployed (fine for the mock TEE; playbook Part D)`);
if (!stack) warnings.push(`no fce run stack for ${CHAIN}: no real TEE machine (fine for the mock TEE)`);

const lastVersion = record?.versions?.length ? record.versions[record.versions.length - 1].version : undefined;

/** A value from its source, or empty: nothing is carried over from an older deployment. */
const pick = (fromSource) => fromSource ?? "";

const fcc = {
  chain: CHAIN,
  chainId: record?.chainId ?? stack?.state.chainId ?? 0,
  extensionId: record?.extensionId ?? stack?.state.extensionId ?? 0,
  instructionSender: pick(record?.instructionSender),
  flareTeeManager: pick(record?.flareTeeManager),
  deployer: pick(record?.deployer),
  registerTx: pick(record?.registerTx),
  version: pick(lastVersion),
  teeId: pick(stack?.state.teeId),
  proxyId: pick(stack?.state.proxyId),
  machineRegisterTx: pick(stack?.state.registerTx),
  productionTx: pick(stack?.state.productionTx),
  tunnelUrl: pick(stack?.state.url),
  // The proxy port is only known while the stack runs; keep the last one for a stopped stack.
  proxyUrl: pick(stack?.proxyUrl || (stack ? prevFcc.proxyUrl : undefined)),
};

const appConfig = readJSON(APP_CONFIG);
const sourceTag = appConfig?.xrplSourceTag;
if (!Number.isInteger(sourceTag) || sourceTag < 0 || sourceTag > 0xffffffff) {
  console.error(`sync-config: ${rel(APP_CONFIG)} needs xrplSourceTag, a whole number from 0 to 4294967295`);
  process.exit(1);
}

const net = wallets.network;
const noTerms = { interestRate: 0, paymentTotal: 0, paymentInterval: 0, gracePeriod: 0 };
const lending = {
  creditServer: wallets.creditServer?.address || "",
  broker: wallets.broker?.address || "",
  vaultId: wallets.lending?.vaultId || "",
  loanBrokerId: wallets.lending?.loanBrokerId || "",
  terms: wallets.lending?.terms || noTerms,
  subscriptionDate: wallets.lending?.subscriptionDate || 0,
  redemptionDate: wallets.lending?.redemptionDate || 0,
};
const vault = {
  lpDepositAccount: wallets.lpDeposit?.address || "",
  treasuryAccount: wallets.treasury?.address || "",
  operatorAccount: wallets.operator?.address || "",
  shareCurrency: wallets.vault?.shareCurrency || "",
  shareCode: wallets.vault?.shareCode || "",
};
if (!wallets.vault) warnings.push("no LP vault setup in wallets.json; run: cd watcher && npm run setup-vault");
if (!wallets.lending) warnings.push("no lending setup in wallets.json; run: cd watcher && node src/setup-lending.js");

const xrpl = {
  network: {
    name: net.name,
    ws: net.ws,
    rpc: net.rpc,
    explorerTx: `${net.explorer}/transactions`,
    explorerAccount: `${net.explorer}/accounts`,
    xamanNetwork: net.xamanNetwork,
  },
  depositAccount: wallets.deposit.address,
  settlementAddress: wallets.settlement.address,
  poolAddress: wallets.pool?.address || "",
  rlusd: wallets.rlusd,
  sourceTag,
};

// ---- render -------------------------------------------------------------

const webGen = JSON.stringify({ _generated: "by scripts/sync-config.mjs; do not edit", xrpl, lending, vault, fcc }, null, 2) + "\n";

const goGen = `// Code generated by scripts/sync-config.mjs from watcher/wallets.json. DO NOT EDIT.

package card

// Defaults baked into the TEE image; XRPL_DEPOSIT_ACCOUNT / XRPL_SETTLEMENT_ADDRESS override them.
const (
	DefaultDepositAccount    = ${JSON.stringify(xrpl.depositAccount)}
	DefaultSettlementAddress = ${JSON.stringify(xrpl.settlementAddress)}
)

// RLUSDIssuer issues the RLUSD the ledger settles in (${net.name}).
const RLUSDIssuer = ${JSON.stringify(xrpl.rlusd.issuer)}

// Loan conversion (watcher/src/setup-lending.js): repayments arrive at the pool
// with the user's tag; the Credit Server cosigns each repayment account.
const (
	DefaultPoolAccount         = ${JSON.stringify(xrpl.poolAddress)}
	DefaultCreditServerAccount = ${JSON.stringify(lending.creditServer)}
	DefaultLoanBrokerID        = ${JSON.stringify(lending.loanBrokerId)}
)

// Lending pool (watcher/src/setup-vault.js): LPs deposit RLUSD at the LP
// deposit account, which issues the share token (trust line); TEE profit goes to the treasury.
const (
	DefaultLPDepositAccount = ${JSON.stringify(vault.lpDepositAccount)}
	DefaultTreasuryAccount  = ${JSON.stringify(vault.treasuryAccount)}
	DefaultOperatorAccount  = ${JSON.stringify(vault.operatorAccount)}
	DefaultShareCurrency    = ${JSON.stringify(vault.shareCurrency)}
)

// DefaultLoanTerms are the XLS-66 terms the mock broker approves.
var DefaultLoanTerms = LoanTerms{
	InterestRate:    ${lending.terms.interestRate},
	PaymentTotal:    ${lending.terms.paymentTotal},
	PaymentInterval: ${lending.terms.paymentInterval},
	GracePeriod:     ${lending.terms.gracePeriod},
}
`;

// undefined / "" leave a key as it is; null removes it (the source is gone, or nothing reads it).
function mergeEnv(file, example, updates) {
  let text = fs.existsSync(file) ? fs.readFileSync(file, "utf8") : fs.existsSync(example) ? fs.readFileSync(example, "utf8") : "";
  for (const [k, raw] of Object.entries(updates)) {
    if (raw === undefined || raw === "") continue;
    if (raw === null) {
      text = text.replace(new RegExp(`^${k}=.*\\n?`, "m"), "");
      continue;
    }
    const v = raw;
    // Quote so both `source .env` (bash) and node --env-file read the same value:
    // JSON (double quotes) in single quotes, values with spaces in double quotes.
    const str = String(v);
    const line = str.includes('"') ? `${k}='${str}'` : /[\s#]/.test(str) ? `${k}=${JSON.stringify(str)}` : `${k}=${str}`;
    const re = new RegExp(`^${k}=.*$`, "m");
    text = re.test(text) ? text.replace(re, line) : `${text.replace(/\n*$/, "\n")}${line}\n`;
  }
  return text;
}

function envValue(file, example, key) {
  const src = fs.existsSync(file) ? file : example;
  if (!fs.existsSync(src)) return "";
  const m = fs.readFileSync(src, "utf8").match(new RegExp(`^${key}=(.*)$`, "m"));
  return m ? m[1].trim() : "";
}

// Point TEE_CARD_URL at the live proxy only for components already in FCC mode.
const watcherFcc = envValue(WATCHER_ENV, WATCHER_ENV_EXAMPLE, "TEE_MODE").toLowerCase() === "fcc";
const webFcc = envValue(WEB_ENV, WEB_ENV_EXAMPLE, "TEE_MODE").toLowerCase() === "fcc";

const watcherEnv = mergeEnv(WATCHER_ENV, WATCHER_ENV_EXAMPLE, {
  XRPL_NETWORK: net.name,
  XRPL_WS: net.ws,
  // Only the watcher talks to XRPL, over its WebSocket; the RPC URL and the
  // MPT-era share id are cleared from older .env files.
  XRPL_RPC: null,
  SHARE_MPT_ID: null,
  XRPL_SOURCE_TAG: String(sourceTag),
  XRPL_EXPLORER: xrpl.network.explorerTx,
  RLUSD_CURRENCY: xrpl.rlusd.currency,
  RLUSD_ISSUER: xrpl.rlusd.issuer,
  XRPL_ISSUER_SEED: wallets.issuer?.seed,
  XRPL_POOL_ADDRESS: xrpl.poolAddress,
  XRPL_BROKER_SEED: wallets.broker?.seed,
  XRPL_CREDIT_SERVER_SEED: wallets.creditServer?.seed,
  LOAN_BROKER_ID: lending.loanBrokerId,
  XRPL_LP_DEPOSIT_ADDRESS: vault.lpDepositAccount,
  XRPL_LP_DEPOSIT_SEED: wallets.lpDeposit?.seed,
  XRPL_TREASURY_ADDRESS: vault.treasuryAccount,
  XRPL_OPERATOR_ADDRESS: vault.operatorAccount,
  SHARE_CURRENCY: vault.shareCurrency,
  LOAN_TERMS: lending.terms === noTerms ? undefined : JSON.stringify(lending.terms),
  XRPL_DEPOSIT_ACCOUNT: xrpl.depositAccount,
  XRPL_SETTLEMENT_ADDRESS: xrpl.settlementAddress,
  XRPL_POOL_SEED: wallets.pool?.seed,
  INSTRUCTION_SENDER: fcc.instructionSender || null,
  TEE_DIRECT_API_KEY: stack?.directKey,
  TEE_CARD_URL: watcherFcc ? stack?.proxyUrl : undefined,
});

const webEnv = mergeEnv(WEB_ENV, WEB_ENV_EXAMPLE, {
  XRPL_RPC: null,
  NEXT_PUBLIC_XRPL_EXPLORER: xrpl.network.explorerTx,
  NEXT_PUBLIC_XRPL_EXPLORER_ACCOUNT: xrpl.network.explorerAccount,
  XRPL_POOL_ADDRESS: xrpl.poolAddress,
  TEE_DIRECT_API_KEY: stack?.directKey,
  TEE_CARD_URL: webFcc ? stack?.proxyUrl : undefined,
});

// ---- write / check ------------------------------------------------------

function emit(file, content) {
  const current = fs.existsSync(file) ? fs.readFileSync(file, "utf8") : null;
  if (current === content) return false;
  stale.push(rel(file));
  if (!CHECK) fs.writeFileSync(file, content);
  return true;
}

const goChanged = emit(GO_GEN, goGen);
emit(WEB_GEN, webGen);
emit(WATCHER_ENV, watcherEnv);
emit(WEB_ENV, webEnv);

for (const w of warnings) console.warn(`sync-config: warning: ${w}`);

if (CHECK) {
  if (stale.length) {
    console.error(`sync-config: out of date: ${stale.join(", ")}\nrun: node scripts/sync-config.mjs`);
    process.exit(1);
  }
  console.log("sync-config: up to date");
  process.exit(0);
}

console.log(stale.length ? `sync-config: updated ${stale.join(", ")}` : "sync-config: up to date");

if (goChanged) {
  // The conformance fixtures embed these defaults, so refresh them in the same step;
  // otherwise go test and fce conformance fail after every wallet reset.
  let refreshed = false;
  try {
    execFileSync("go", ["test", "./template/internal/app", "-run", "TestFixtures", "-update"], {
      cwd: path.join(ROOT, "extension"),
      env: { ...process.env, CGO_ENABLED: "0" },
      stdio: ["ignore", "ignore", "pipe"],
    });
    refreshed = true;
  } catch (e) {
    console.warn(`sync-config: warning: could not refresh the conformance fixtures (${e.code === "ENOENT" ? "go not found" : "go test failed"})`);
  }
  console.log(
    "sync-config: TEE defaults (accounts / RLUSD issuer) changed. This changes the image's code hash:\n" +
      (refreshed
        ? "  conformance fixtures refreshed (extension/template/conformance)\n"
        : "  cd extension && go test ./template/internal/app -update   # refresh conformance fixtures\n") +
      "  rebuild the image and fce add-version before the live TEE uses them",
  );
}

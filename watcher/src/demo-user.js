/**
 * Plays the cardholder with the demo `user` wallet from ../wallets.json, for
 * walking through the demo without Xaman. Never prints the seed.
 *
 * Usage (from watcher/, services running):
 *   npm run demo-user -- session            register, print the browser session snippet
 *   npm run demo-user -- deposit <xrp>      send XRP collateral to the deposit account with the tag
 *   npm run demo-user -- pay-installment    pay the next installment of the user's first open loan
 *   npm run demo-user -- faucet             top the wallet up with Devnet XRP (after many rehearsals)
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import xrpl from "xrpl";
import { RLUSD_CURRENCY, RLUSD_ISSUER, WS } from "./network.js";
import { card } from "./tee.js";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const W = JSON.parse(fs.readFileSync(process.env.WALLETS_FILE || path.join(__dirname, "..", "wallets.json"), "utf8"));
const WATCHER = `http://127.0.0.1:${process.env.WATCHER_HTTP_PORT || 8090}`;
const user = xrpl.Wallet.fromSeed(W.user.seed);

async function register() {
  return card("REGISTER", { xrplAddress: user.address });
}

async function submit(tx) {
  const client = new xrpl.Client(WS);
  await client.connect();
  try {
    const res = await client.submitAndWait(tx, { wallet: user });
    const code = res.result.meta?.TransactionResult;
    if (code !== "tesSUCCESS") throw new Error(`${tx.TransactionType}: ${code}`);
    return res.result.hash;
  } finally {
    await client.disconnect();
  }
}

const [cmd, arg] = process.argv.slice(2);

if (cmd === "session") {
  const reg = await register();
  const session = { xrplAddress: reg.xrplAddress, tag: reg.tag, depositAccount: reg.depositAccount };
  console.log(`demo user ${reg.xrplAddress} has tag ${reg.tag}`);
  console.log("Paste into the browser console on the web app, then reload:\n");
  console.log(`localStorage.setItem("xrpl-cc-session", ${JSON.stringify(JSON.stringify(session))});`);
} else if (cmd === "deposit") {
  const xrp = arg || "50";
  const reg = await register();
  const hash = await submit({
    TransactionType: "Payment",
    Account: user.address,
    Destination: reg.depositAccount,
    DestinationTag: reg.tag,
    Amount: xrpl.xrpToDrops(xrp),
  });
  console.log(`deposited ${xrp} XRP with tag ${reg.tag}: ${hash}`);
  console.log("the watcher credits it to the TEE within a few seconds");
} else if (cmd === "pay-installment") {
  const res = await (await fetch(`${WATCHER}/loans?user=${user.address}`)).json();
  if (!res.ok) throw new Error(res.error);
  const loan = res.loans.find((l) => l.paymentRemaining > 0);
  if (!loan) throw new Error("no open loan; convert card debt first");
  const value = String(Math.ceil(Number(loan.periodicPayment) * 1e6) / 1e6);
  const funding = await submit({
    TransactionType: "Payment",
    Account: user.address,
    Destination: res.repaymentAccount,
    Amount: { currency: RLUSD_CURRENCY, issuer: RLUSD_ISSUER, value },
  });
  console.log(`funded ${value} RLUSD to repayment account ${res.repaymentAccount}: ${funding}`);
  const paid = await (await fetch(`${WATCHER}/loans/pay`, {
    method: "POST",
    body: JSON.stringify({ txHash: funding, loanId: loan.loanId }),
  })).json();
  if (!paid.ok) throw new Error(paid.error);
  console.log(`LoanPay ${paid.loanPayTx}; loan ${loan.loanId.slice(0, 8)}… now has ${paid.loans.find((l) => l.loanId === loan.loanId)?.paymentRemaining} payments left`);
} else if (cmd === "faucet") {
  const client = new xrpl.Client(WS);
  await client.connect();
  try {
    const { balance } = await client.fundWallet(user);
    console.log(`${user.address} now has ${balance} XRP`);
  } finally {
    await client.disconnect();
  }
} else {
  console.error("usage: npm run demo-user -- session | deposit <xrp> | pay-installment | faucet");
  process.exit(1);
}

#!/usr/bin/env node
// Seed the CARD ledger with demo cardholders so the operator Statistics tab looks
// populated. Registers N cardholders, credits each a deposit, and runs a few card
// swipes. Mode-agnostic: it talks to the web proxy (/api/card), which forwards to
// whichever TEE is configured (mock or fcc / live machine).
//
// Usage:
//   node scripts/seed-cardholders.mjs [count]
//   SEED_API=http://localhost:3000/api/card COUNT=5 node scripts/seed-cardholders.mjs
//   SEED_API=http://127.0.0.1:8080/card node scripts/seed-cardholders.mjs   # mock TEE direct

const API = process.env.SEED_API || "http://localhost:3000/api/card";
const CREDENTIAL = process.env.CARD_CREDENTIAL || "swell-demo-card-credential";
const COUNT = Number(process.argv[2] || process.env.COUNT || 5);

const NAMES = [
  "Ava", "Liam", "Mia", "Noah", "Zoe", "Kai", "Elena", "Jonas",
  "Sara", "Max", "Nina", "Theo", "Lea", "Finn", "Ruby", "Omar",
];
const MERCHANTS = [
  ["Coffee at Swell Cafe", 4.5],
  ["Uber to the venue", 12.0],
  ["Conference merch", 35.0],
  ["Hotel deposit hold", 50.0],
  ["Airport espresso", 6.25],
  ["Team dinner", 42.0],
];

const B58 = "rpshnaf39wBUDNEGHJKLM4PQRST7VWXYZ2bcdeCg65jkm8oFqi1tuvAxyz";
function randomXrplAddress() {
  let s = "r";
  for (let i = 0; i < 33; i++) s += B58[Math.floor(Math.random() * B58.length)];
  return s;
}
function randomTxHash() {
  const hex = "0123456789ABCDEF";
  let s = "";
  for (let i = 0; i < 64; i++) s += hex[Math.floor(Math.random() * 16)];
  return s;
}
const pick = (a) => a[Math.floor(Math.random() * a.length)];
const randint = (lo, hi) => lo + Math.floor(Math.random() * (hi - lo + 1));

async function card(command, body = {}) {
  const res = await fetch(API, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Card-Credential": CREDENTIAL },
    body: JSON.stringify({ command, credential: CREDENTIAL, ...body }),
  });
  const json = await res.json();
  if (!json.ok) throw new Error(`${command}: ${json.error || res.status}`);
  return json.data;
}

async function seedOne(i) {
  const name = NAMES[i % NAMES.length];
  const addr = randomXrplAddress();

  const reg = await card("REGISTER", { xrplAddress: addr });
  const tag = reg.tag;

  const xrp = randint(60, 300);
  await card("CREDIT_DEPOSIT", { tag, drops: String(xrp * 1_000_000), txHash: randomTxHash() });

  const swipes = randint(1, 4);
  let spent = 0;
  for (let s = 0; s < swipes; s++) {
    const [label, amount] = pick(MERCHANTS);
    try {
      const r = await card("RESERVE", { tag, rlusdAmount: String(amount), label });
      if (r.approved) spent += amount;
    } catch {
      // ignore a declined swipe (over LTV); keep seeding
    }
  }
  console.log(`  ${name.padEnd(6)} tag ${tag}  deposit ${xrp} XRP  swipes ${swipes}  spent $${spent.toFixed(2)}`);
  return tag;
}

async function main() {
  console.log(`Seeding ${COUNT} cardholders via ${API}`);
  for (let i = 0; i < COUNT; i++) {
    try {
      await seedOne(i);
    } catch (e) {
      console.error(`  cardholder ${i + 1} failed: ${e.message}`);
    }
  }
  try {
    const stats = await card("GET_STATS");
    console.log(
      `\nDone. Operator now has ${stats.userCount} cardholders, ` +
        `${stats.totalRlusdDebt} RLUSD outstanding, ${stats.totalCardPayments} card payments.`
    );
  } catch {
    console.log("\nDone.");
  }
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});

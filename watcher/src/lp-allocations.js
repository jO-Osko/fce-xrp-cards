/**
 * Next round: LPs mark part of their existing pool deposit for the
 * uncollateralized lending protocol (riskier, higher yield). This is a pledge
 * only: it is saved here, off chain, and nothing is signed or moved. When the
 * round opens, the marked share of each position would move across.
 *
 * Stored per LP address in lp-allocations.json: the share in basis points, the
 * position's RLUSD value when it was saved (for the round's totals) and when.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import xrpl from "xrpl";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const FILE = process.env.WATCHER_LP_ALLOCATIONS_FILE || path.join(__dirname, "..", "lp-allocations.json");
/** The projected yield of the uncollateralized protocol, shown to LPs (15% APY by default). */
const PROJECTED_APY_BPS = Number(process.env.WATCHER_UNSECURED_APY_BPS || 1500);

function load() {
  try {
    return JSON.parse(fs.readFileSync(FILE, "utf8"));
  } catch {
    return {};
  }
}

function round() {
  const all = Object.values(load()).filter((a) => a.unsecuredBps > 0);
  const pledgedRlusd = all.reduce((s, a) => s + (Number(a.positionRlusd) * a.unsecuredBps) / 10_000, 0);
  return { projectedApyBps: PROJECTED_APY_BPS, lpCount: all.length, pledgedRlusd: pledgedRlusd.toFixed(6) };
}

/** `address`'s saved choice (0% when none) and the round's totals. */
export function allocationFor(address) {
  if (!xrpl.isValidClassicAddress(address)) throw new Error("classic address required");
  const saved = load()[address];
  return { address, unsecuredBps: saved?.unsecuredBps ?? 0, updatedAt: saved?.updatedAt ?? null, round: round() };
}

/** Saves `address`'s choice: `unsecuredBps` of its position (0 to 10,000) for the next round. */
export function saveAllocation({ address, unsecuredBps, positionRlusd }) {
  if (!xrpl.isValidClassicAddress(address)) throw new Error("classic address required");
  if (!Number.isInteger(unsecuredBps) || unsecuredBps < 0 || unsecuredBps > 10_000) {
    throw new Error("unsecuredBps must be a whole number from 0 to 10000");
  }
  const value = Number(positionRlusd);
  if (!Number.isFinite(value) || value < 0) throw new Error("positionRlusd must be a non-negative amount");
  const all = load();
  all[address] = { unsecuredBps, positionRlusd: value.toFixed(6), updatedAt: Math.floor(Date.now() / 1000) };
  fs.writeFileSync(FILE, JSON.stringify(all, null, 2));
  return allocationFor(address);
}

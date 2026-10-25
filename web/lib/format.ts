/** Display formatting shared by every page, so amounts, times and ids look the same everywhere. */

/**
 * An RLUSD amount with thousands separators and every decimal the TEE keeps (6),
 * so interest accruing by the minute stays visible.
 */
export function amount(value: string | number | null | undefined, max = 6) {
  const n = Number(value);
  if (value == null || value === "" || !Number.isFinite(n)) return "—";
  return n.toLocaleString(undefined, { maximumFractionDigits: max });
}

/** A USD price with exactly two decimals, such as $2.50. */
export function usd(value: string | number) {
  const n = Number(value);
  if (!Number.isFinite(n)) return "—";
  return `$${n.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

/** A time in seconds since 1970, such as "Oct 24, 07:25 PM". */
export function when(at?: number) {
  if (!at) return "—";
  return new Date(at * 1000).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** An XRPL transaction hash or ledger object id, shortened (XRPL ids have no 0x prefix). */
export function shortHash(hash: string) {
  return hash.length <= 12 ? hash : `${hash.slice(0, 8)}…`;
}

/** A classic address, shortened to its first and last characters. */
export function shortAddress(address: string) {
  return address.length <= 14 ? address : `${address.slice(0, 6)}…${address.slice(-4)}`;
}

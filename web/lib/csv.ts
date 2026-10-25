/** CSV export, built in the browser from what a page already shows. */

/**
 * One cell, quoted when needed. A cell starting with = + - @ (or a tab or
 * carriage return) gets a leading apostrophe, so a spreadsheet shows it as
 * text instead of running it as a formula: merchant names come from card
 * payments, so they are not trusted.
 */
function cell(value: unknown) {
  let s = value == null ? "" : String(value);
  if (/^[=+\-@\t\r]/.test(s)) s = `'${s}`;
  return /[",\n\r]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
}

export function toCsv(headers: string[], rows: unknown[][]) {
  return [headers, ...rows].map((r) => r.map(cell).join(",")).join("\r\n") + "\r\n";
}

/** Seconds since 1970 as an ISO 8601 UTC time, which spreadsheets sort and parse. */
export const isoTime = (at: number) => (at ? new Date(at * 1000).toISOString() : "");

/** Saves `rows` as `filename` in the browser's downloads. */
export function downloadCsv(filename: string, headers: string[], rows: unknown[][]) {
  // The byte-order mark makes Excel read the file as UTF-8 (− and … in amounts and hashes).
  const blob = new Blob(["﻿", toCsv(headers, rows)], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

/** Today as YYYY-MM-DD, for file names. */
export const today = () => new Date().toISOString().slice(0, 10);

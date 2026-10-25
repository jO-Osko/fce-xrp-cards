"use client";

const KEY = "xrpl-cc-session";

export type Session = {
  xrplAddress: string;
  tag: number;
  depositAccount: string;
};

export function loadSession(): Session | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = localStorage.getItem(KEY);
    return raw ? (JSON.parse(raw) as Session) : null;
  } catch {
    return null;
  }
}

export function saveSession(s: Session) {
  localStorage.setItem(KEY, JSON.stringify(s));
}

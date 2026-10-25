"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import {
  createXamanPayload,
  fetchXamanPayload,
  pendingFromPayload,
  XAMAN_POLL_MS,
  type XamanPending,
} from "./xaman";

/**
 * Signs one Xaman payload at a time: sign(body) shows the QR (via `pending`)
 * and resolves with the transaction id once signed (signIn() with the signer's
 * account), or rejects when the request expires or is cancelled.
 */
export function useXamanSign() {
  const [pending, setPending] = useState<XamanPending | null>(null);
  const timer = useRef<ReturnType<typeof setInterval> | null>(null);
  const reject = useRef<((e: Error) => void) | null>(null);

  const stop = useCallback(() => {
    if (timer.current) clearInterval(timer.current);
    timer.current = null;
    setPending(null);
  }, []);

  useEffect(() => stop, [stop]);

  const cancel = useCallback(() => {
    stop();
    reject.current?.(new Error("Xaman request cancelled"));
    reject.current = null;
  }, [stop]);

  const request = useCallback(
    async (body: Record<string, unknown>, field: "txid" | "account") => {
      const payload = await createXamanPayload(body);
      setPending(pendingFromPayload(payload));
      return new Promise<string>((resolve, rej) => {
        reject.current = rej;
        timer.current = setInterval(async () => {
          const status = await fetchXamanPayload(payload.uuid).catch(() => null);
          if (!status) return;
          if (status.meta?.signed) {
            stop();
            reject.current = null;
            const value = status.response?.[field];
            if (value) resolve(value);
            else rej(new Error(`Xaman signed but returned no ${field}`));
          } else if (status.meta?.expired || status.meta?.cancelled || status.meta?.resolved) {
            // resolved without signed: the user rejected it in Xaman.
            stop();
            reject.current = null;
            rej(new Error("Xaman request expired, was cancelled or was rejected"));
          }
        }, XAMAN_POLL_MS);
      });
    },
    [stop],
  );

  const sign = useCallback((body: Record<string, unknown>) => request(body, "txid"), [request]);
  const signIn = useCallback((body: Record<string, unknown>) => request(body, "account"), [request]);

  return { pending, sign, signIn, cancel };
}

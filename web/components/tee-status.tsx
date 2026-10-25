"use client";

import { FCC, FCC_DEPLOYED, coston2AddressUrl, coston2TxUrl, shortHex } from "@/lib/fcc";

/** Which Credit Server settles: the local mock TEE, or the real one on Coston2. Settlement page only. */
export function TeeStatus() {
  if (!FCC_DEPLOYED) {
    return (
      <p className="fcc-strip tee-status">
        <span>Credit Server: local mock TEE (settlement goes straight to it, without validators)</span>
        <span className="fcc-strip-sep" aria-hidden>
          ·
        </span>
        <span>real TEE on Coston2 not deployed</span>
      </p>
    );
  }
  return (
    <p className="fcc-strip tee-status">
      <span>
        TEE instruction sender{" "}
        <a
          className="mono"
          href={coston2AddressUrl(FCC.instructionSender)}
          target="_blank"
          rel="noopener noreferrer"
          title={FCC.instructionSender}
        >
          {shortHex(FCC.instructionSender, 8, 6)}
        </a>
      </span>
      <span className="fcc-strip-sep" aria-hidden>
        ·
      </span>
      <span>
        Machine{" "}
        <a
          className="mono"
          href={coston2TxUrl(FCC.productionTx)}
          target="_blank"
          rel="noopener noreferrer"
          title={FCC.teeId}
        >
          {shortHex(FCC.teeId, 6, 4)}
        </a>{" "}
        <span className={`badge ${FCC.machineStatus === "PRODUCTION" ? "ok" : "warn"}`}>{FCC.machineStatus}</span>
      </span>
    </p>
  );
}

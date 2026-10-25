"use client";

import {
  COSTON2_EXPLORER,
  FCC,
  coston2AddressUrl,
  coston2TxUrl,
  FCC_DEPLOYED,
  shortHex,
} from "@/lib/fcc";

/**
 * How FCC is used in this demo:
 * - REGISTER / CREDIT_DEPOSIT / RESERVE / GET_POSITION → direct actions
 *   (browser/watcher → tee-proxy POST /direct).
 * - SETTLE_DAILY / SETTLE_CONFIRM → on-chain instructions via InstructionSender
 *   (watcher key pays fee; poll submissionTag=threshold).
 * - Also on Coston2: extension register, FlareTeeManager, machine toProduction.
 */
export function FccPanel() {
  // Nothing to show while only the mock TEE runs.
  if (!FCC_DEPLOYED) return null;
  return (
    <section className="panel">
      <h2>FCC on Coston2</h2>
      <p className="lead">
        Card spends are <strong>direct actions</strong>; daily settlement is an{" "}
        <strong>on-chain TEE instruction</strong> through the instruction
        sender. Machine status: <strong>{FCC.machineStatus}</strong>.
      </p>
      <div className="fcc-grid">
        <div className="stat">
          <label>Extension ID</label>
          <strong className="mono">{FCC.extensionId}</strong>
        </div>
        <div className="stat">
          <label>Machine status</label>
          <strong className="machine">
            <span className="badge ok">{FCC.machineStatus}</span>
            <span className="mono">{shortHex(FCC.teeId, 6, 4)}</span>
          </strong>
        </div>
        <div className="stat">
          <label>Extension register</label>
          <strong className="mono">
            <a
              href={coston2TxUrl(FCC.registerTx)}
              target="_blank"
              rel="noopener noreferrer"
            >
              {shortHex(FCC.registerTx)}
            </a>
          </strong>
        </div>
        <div className="stat">
          <label>Machine register</label>
          <strong className="mono">
            <a
              href={coston2TxUrl(FCC.machineRegisterTx)}
              target="_blank"
              rel="noopener noreferrer"
            >
              {shortHex(FCC.machineRegisterTx)}
            </a>
          </strong>
        </div>
        <div className="stat">
          <label>toProduction</label>
          <strong className="mono">
            <a
              href={coston2TxUrl(FCC.productionTx)}
              target="_blank"
              rel="noopener noreferrer"
            >
              {shortHex(FCC.productionTx)}
            </a>
          </strong>
        </div>
        <div className="stat">
          <label>TEE instruction sender</label>
          <strong className="mono">
            <a
              href={coston2AddressUrl(FCC.instructionSender)}
              target="_blank"
              rel="noopener noreferrer"
              title={FCC.instructionSender}
            >
              {shortHex(FCC.instructionSender)}
            </a>
          </strong>
        </div>
        <div className="stat">
          <label>Flare TEE manager</label>
          <strong className="mono">
            <a
              href={coston2AddressUrl(FCC.flareTeeManager)}
              target="_blank"
              rel="noopener noreferrer"
            >
              {shortHex(FCC.flareTeeManager)}
            </a>
          </strong>
        </div>
        <div className="stat">
          <label>TEE proxy</label>
          <strong className="mono">
            <a href={`${FCC.proxyUrl}/info`} target="_blank" rel="noopener noreferrer">
              /info
            </a>
          </strong>
        </div>
        <div className="stat">
          <label>Explorer</label>
          <strong>
            <a href={COSTON2_EXPLORER} target="_blank" rel="noopener noreferrer">
              Coston2 explorer
            </a>
          </strong>
        </div>
      </div>
    </section>
  );
}

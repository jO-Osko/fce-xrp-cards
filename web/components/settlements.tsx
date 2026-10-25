import { EXPLORER_TX } from "@/lib/config";
import { coston2TxUrl, shortHex } from "@/lib/fcc";
import { amount, shortHash } from "@/lib/format";
import type { SettleResult } from "@/lib/settle-result";

export type SettlementRow = SettleResult & { at?: number };

function formatWhen(at?: number) {
  if (!at) return "—";
  return new Date(at * 1000).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function SettlementsList({
  settlements,
}: {
  settlements: SettlementRow[];
}) {
  if (settlements.length === 0) {
    return (
      <p className="empty-note">
        No settlements yet. Request daily settlement after a Mock Card payment.
      </p>
    );
  }

  return (
    <ul className="tx-list settle-list">
      {settlements.map((s) => {
        const explorer = s.explorerUrl || `${EXPLORER_TX}/${s.txHash}`;
        const flareTx = s.settleDailyFlareTx || s.settleConfirmFlareTx || "";
        const flareTitle = s.settleDailyFlareTx
          ? `SETTLE_DAILY ${s.settleDailyActionId || s.settleDailyFlareTx}`
          : s.settleConfirmFlareTx
            ? `SETTLE_CONFIRM ${s.settleConfirmActionId || s.settleConfirmFlareTx}`
            : "";
        return (
          <li key={s.txHash} className="tx-row settle-row">
            <time>{formatWhen(s.at)}</time>
            <span>
              Daily settlement
              {s.wireQueued ? (
                <>
                  {" "}
                  <span className="badge ok">Settled</span>
                </>
              ) : null}
            </span>
            <a
              className="mono"
              href={explorer}
              target="_blank"
              rel="noopener noreferrer"
              title={s.txHash}
            >
              XRPL {shortHash(s.txHash)}
            </a>
            {flareTx ? (
              <a
                className="mono muted"
                href={coston2TxUrl(flareTx)}
                target="_blank"
                rel="noopener noreferrer"
                title={flareTitle}
              >
                Flare {shortHex(flareTx)}
              </a>
            ) : s.settleDailyActionId ? (
              <span
                className="mono muted"
                title={`TEE instruction ${s.settleDailyActionId}`}
              >
                TEE {shortHex(s.settleDailyActionId)}
              </span>
            ) : null}
            <strong>
              {amount(s.rlusdAmount)} RLUSD
              {s.treasuryRlusd && Number(s.treasuryRlusd) > 0 ? (
                <span className="muted" title={s.treasuryTxHash ? `treasury tx ${s.treasuryTxHash}` : undefined}>
                  {" "}+ {amount(s.treasuryRlusd)} to treasury
                </span>
              ) : null}
            </strong>
          </li>
        );
      })}
    </ul>
  );
}

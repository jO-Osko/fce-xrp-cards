import { dropsToXrp } from "@/lib/api";
import { EXPLORER_TX } from "@/lib/config";
import { shortHash } from "@/lib/format";

export type Deposit = {
  txHash: string;
  drops: string;
  at: number;
};

function formatWhen(at: number) {
  if (!at) return "—";
  return new Date(at * 1000).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function DepositsList({ deposits }: { deposits: Deposit[] }) {
  if (deposits.length === 0) {
    return (
      <p className="empty-note">
        No deposits credited yet. A deposit shows up here once the watcher has
        credited it to your collateral.
      </p>
    );
  }

  return (
    <ul className="tx-list">
      {deposits.map((d) => (
        <li key={d.txHash} className="tx-row">
          <time>{formatWhen(d.at)}</time>
          <span>XRP collateral deposit</span>
          <a
            className="mono"
            href={`${EXPLORER_TX}/${d.txHash}`}
            target="_blank"
            rel="noopener noreferrer"
          >
            {shortHash(d.txHash)}
          </a>
          <strong>{dropsToXrp(d.drops)} XRP</strong>
        </li>
      ))}
    </ul>
  );
}

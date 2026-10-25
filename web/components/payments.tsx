import { shortHex, teeDirectActionUrl } from "@/lib/fcc";

export type CardPayment = {
  label?: string;
  rlusdAmount: string;
  xrpLockedDrops?: string;
  at: number;
  /** FCC direct-action id when TEE_MODE=fcc (tee-proxy result, not a Coston2 tx) */
  flareActionId?: string;
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

export function PaymentsList({ payments }: { payments: CardPayment[] }) {
  if (payments.length === 0) {
    return <p className="empty-note">No card payments yet.</p>;
  }

  return (
    <ul className="tx-list">
      {payments.map((p, i) => (
        <li key={`${p.at}-${p.rlusdAmount}-${i}`} className="tx-row">
          <time>{formatWhen(p.at)}</time>
          <span>{p.label || "Card payment"}</span>
          {p.flareActionId ? (
            <a
              className="mono muted"
              href={teeDirectActionUrl(p.flareActionId)}
              target="_blank"
              rel="noopener noreferrer"
              title={`TEE direct action ${p.flareActionId}`}
            >
              TEE {shortHex(p.flareActionId)}
            </a>
          ) : (
            <span className="muted">off-chain</span>
          )}
          <strong>{p.rlusdAmount} RLUSD</strong>
        </li>
      ))}
    </ul>
  );
}

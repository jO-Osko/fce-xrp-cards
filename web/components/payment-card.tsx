"use client";

type Props = {
  tag?: number | null;
  xrplAddress?: string | null;
  amount: string;
  status?: "idle" | "authorizing" | "approved" | "declined";
};

function maskAddress(addr?: string | null) {
  if (!addr || addr.length < 10) return "···· ···· ···· ····";
  const end = addr.slice(-4);
  return `r··· ···· ···· ${end}`;
}

function formatPanFromTag(tag?: number | null) {
  const n = String(tag ?? 0).padStart(4, "0").slice(-4);
  return `5280 14${n.slice(0, 2)} **** ${n}`;
}

export function PaymentCard({ tag, xrplAddress, amount, status = "idle" }: Props) {
  return (
    <div className={`pay-card pay-card--${status}`} aria-hidden={false}>
      <div className="pay-card__shine" />
      <div className="pay-card__top">
        <span className="pay-card__brand">
          XRPL<span> Card</span>
        </span>
        <span className="pay-card__network" title="Mock network">
          <i className="pay-card__orb pay-card__orb--a" />
          <i className="pay-card__orb pay-card__orb--b" />
        </span>
      </div>

      <div className="pay-card__chip" />

      <div className="pay-card__pan mono">{formatPanFromTag(tag)}</div>

      <div className="pay-card__row">
        <div>
          <div className="pay-card__label">Cardholder tag</div>
          <div className="pay-card__value">{tag ?? "—"}</div>
        </div>
        <div>
          <div className="pay-card__label">Amount</div>
          <div className="pay-card__value">
            {amount || "0.00"} <span className="pay-card__ccy">RLUSD</span>
          </div>
        </div>
      </div>

      <div className="pay-card__footer">
        <div>
          <div className="pay-card__label">Linked XRPL</div>
          <div className="pay-card__addr mono">{maskAddress(xrplAddress)}</div>
        </div>
        <div className="pay-card__status">
          {status === "authorizing" && "Authorizing…"}
          {status === "approved" && "Approved"}
          {status === "declined" && "Declined"}
          {status === "idle" && "Demo card"}
        </div>
      </div>
    </div>
  );
}

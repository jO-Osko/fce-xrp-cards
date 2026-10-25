/** A ratio against a limit: the fill carries severity, the track is a lighter step of the same hue. */
export function Meter({ ratio, label, color }: { ratio: number; label: string; color?: string }) {
  const pct = Math.max(0, Math.min(1, ratio)) * 100;
  const fill = color ?? (ratio >= 0.9 ? "var(--red)" : ratio >= 0.6 ? "var(--amber)" : "var(--green)");
  return (
    <div className="meter" role="meter" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(pct)} aria-label={label}>
      <div className="meter__track" style={{ background: `color-mix(in srgb, ${fill} 18%, transparent)` }}>
        <div className="meter__fill" style={{ width: `${pct}%`, background: fill }} />
      </div>
    </div>
  );
}

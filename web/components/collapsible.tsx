"use client";

/**
 * A list or table behind a clickable header, closed by default. The header
 * shows how many entries it holds. It is a native <details>, so it stays open
 * across the pages' background refreshes once opened.
 */
export function Collapsible({
  title,
  count,
  actions,
  children,
}: {
  title: React.ReactNode;
  count?: number;
  /** Controls shown next to the header, such as a Refresh button; clicking them does not toggle the list. */
  actions?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <details className="collapsible">
      <summary>
        <span className="collapsible-title">{title}</span>
        {count != null && <span className="collapsible-count">{count === 0 ? "none" : count}</span>}
        {actions && (
          <span className="collapsible-actions" onClick={(e) => e.preventDefault()}>
            {actions}
          </span>
        )}
      </summary>
      <div className="collapsible-body">{children}</div>
    </details>
  );
}

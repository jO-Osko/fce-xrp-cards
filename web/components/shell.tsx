"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { ThemeToggle } from "@/components/theme-toggle";

/**
 * Three apps in one: the cardholder's (their own card and loans), the
 * liquidity provider's under /lp (earn yield on RLUSD in the card's lending
 * pool) and the operator's under /global (card processor, operator, loan
 * broker: global statistics, settlement, broker decisions).
 */
const AREAS = {
  user: {
    name: "Cardholder app",
    switchLabel: "Cardholder",
    description: "your card and loans",
    home: "/",
    links: [
      { href: "/", label: "Onboard" },
      { href: "/position", label: "Position" },
      { href: "/card", label: "Mock Card" },
      { href: "/loans", label: "Loans" },
      { href: "/stats", label: "My statistics" },
      { href: "/faucets", label: "Faucets" },
    ],
  },
  lp: {
    name: "Liquidity provider app",
    switchLabel: "Liquidity provider",
    description: "earn yield on RLUSD in the card's lending pool",
    home: "/lp",
    links: [
      { href: "/lp", label: "Yield" },
      { href: "/lp/liquidity", label: "Liquidity" },
      { href: "/lp/next-round", label: "Next round" },
    ],
  },
  global: {
    name: "Operator app",
    switchLabel: "Operator",
    description: "card processor, operator and loan broker",
    home: "/global",
    links: [
      { href: "/global", label: "Global statistics" },
      { href: "/global/settlement", label: "Settlement" },
      { href: "/global/broker", label: "Mock Broker" },
    ],
  },
} as const;

export function Shell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const under = (p: string) => pathname === p || pathname.startsWith(`${p}/`);
  const areaKey: keyof typeof AREAS = under("/global") ? "global" : under("/lp") ? "lp" : "user";
  const area = AREAS[areaKey];
  const links = area.links;
  return (
    <main data-area={areaKey}>
      <div className="nav">
        <div className="nav-top">
          <div className="brand-block">
            <div className="brand">
              XRPL Credit Card <span>· Flare TEE</span>
            </div>
            <p className="area-label">
              You are in the <strong className="area-badge">{area.name}</strong>
              <span className="area-desc"> — {area.description}</span>
            </p>
          </div>
          <div className="nav-actions">
            <nav className="area-switch" aria-label="Switch app">
              {(Object.keys(AREAS) as Array<keyof typeof AREAS>).map((k) => (
                <Link key={k} href={AREAS[k].home} data-active={k === areaKey ? "true" : "false"} aria-current={k === areaKey ? "page" : undefined}>
                  {AREAS[k].switchLabel}
                </Link>
              ))}
            </nav>
            <ThemeToggle />
          </div>
        </div>
        <nav className="tabs">
          {links.map((l) => (
            <Link
              key={l.href}
              href={l.href}
              className="tab"
              data-active={pathname === l.href ? "true" : "false"}
            >
              {l.label}
            </Link>
          ))}
        </nav>
      </div>
      {children}
    </main>
  );
}

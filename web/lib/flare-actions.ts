/** Client-side map of mock-card payments → FCC direct-action ids (Coston2 TEE). */

const KEY = "xrpl-cc-flare-actions";

export type FlareActionRef = {
  tag: number;
  at: number;
  rlusdAmount: string;
  actionId: string;
};

function loadAll(): FlareActionRef[] {
  if (typeof window === "undefined") return [];
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as FlareActionRef[];
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function saveAll(rows: FlareActionRef[]) {
  localStorage.setItem(KEY, JSON.stringify(rows.slice(0, 200)));
}

export function rememberFlareAction(ref: FlareActionRef) {
  const all = loadAll().filter(
    (r) =>
      !(
        r.tag === ref.tag &&
        r.at === ref.at &&
        r.rlusdAmount === ref.rlusdAmount
      ),
  );
  all.unshift(ref);
  saveAll(all);
}

export function attachFlareActions<
  T extends { at: number; rlusdAmount: string },
>(tag: number, payments: T[]): (T & { flareActionId?: string })[] {
  const all = loadAll().filter((r) => r.tag === tag);
  return payments.map((p) => {
    const hit = all.find(
      (r) => r.at === p.at && r.rlusdAmount === p.rlusdAmount,
    );
    return hit ? { ...p, flareActionId: hit.actionId } : p;
  });
}

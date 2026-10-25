"use client";

import { useEffect, useState } from "react";

type Theme = "light" | "dark";

/** localStorage key; read before paint by the script in app/layout.tsx. */
export const THEME_KEY = "xrpl-cc-theme";

/** The theme in effect: the one picked here, else the system's. */
function currentTheme(): Theme {
  const picked = document.documentElement.dataset.theme;
  if (picked === "light" || picked === "dark") return picked;
  return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

export function ThemeToggle() {
  // Unknown until mounted (the server cannot know), so nothing flips after hydration.
  const [theme, setTheme] = useState<Theme | null>(null);

  useEffect(() => {
    setTheme(currentTheme());
    // Follow system changes while nothing has been picked.
    const mq = window.matchMedia("(prefers-color-scheme: light)");
    const onChange = () => setTheme(currentTheme());
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, []);

  function toggle() {
    const next: Theme = currentTheme() === "dark" ? "light" : "dark";
    document.documentElement.dataset.theme = next;
    try {
      localStorage.setItem(THEME_KEY, next);
    } catch {
      /* storage unavailable: the choice lasts for this page only */
    }
    setTheme(next);
  }

  const target = theme === "dark" ? "light" : "dark";
  return (
    <button
      type="button"
      className="theme-toggle"
      onClick={toggle}
      aria-label={`Switch to ${target} mode`}
      title={`Switch to ${target} mode`}
      style={{ visibility: theme ? "visible" : "hidden" }}
    >
      <span aria-hidden>{target === "light" ? "☀" : "☾"}</span>
      {target === "light" ? "Light" : "Dark"}
    </button>
  );
}

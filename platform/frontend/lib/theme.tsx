"use client";

import { createContext, useCallback, useContext, useEffect, useState } from "react";

export type Theme = "light" | "dark";

export const THEME_STORAGE_KEY = "anp.theme";

// resolveTheme 纯函数：stored 优先，否则跟随系统。inline script 与 ThemeProvider 共用。
export function resolveTheme(stored: string | null, systemDark: boolean): Theme {
  if (stored === "light" || stored === "dark") return stored;
  return systemDark ? "dark" : "light";
}

// applyTheme 把主题应用到 <html> + 持久化。仅浏览器端调。
export function applyTheme(theme: Theme) {
  const root = document.documentElement;
  root.classList.toggle("dark", theme === "dark");
  try {
    localStorage.setItem(THEME_STORAGE_KEY, theme);
  } catch {}
}

// readInitialTheme 挂载时读 <html> 当前 class（inline script 已设）作初始值，避免 SSR/CSR 不一致。
function readInitialTheme(): Theme {
  if (typeof document === "undefined") return "light";
  return document.documentElement.classList.contains("dark") ? "dark" : "light";
}

interface ThemeContextValue {
  theme: Theme;
  toggle: () => void;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const [theme, setTheme] = useState<Theme>(readInitialTheme);

  // 系统实时跟随：用户未手动选（无 localStorage）时，系统切换实时跟。
  useEffect(() => {
    const stored =
      typeof localStorage !== "undefined" ? localStorage.getItem(THEME_STORAGE_KEY) : null;
    if (stored) return; // 用户已手动选，不跟随系统
    const mql = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = (e: MediaQueryListEvent) => {
      const t: Theme = e.matches ? "dark" : "light";
      document.documentElement.classList.toggle("dark", t === "dark");
      setTheme(t);
    };
    mql.addEventListener("change", onChange);
    return () => mql.removeEventListener("change", onChange);
  }, []);

  const toggle = useCallback(() => {
    setTheme((prev) => {
      const next: Theme = prev === "dark" ? "light" : "dark";
      applyTheme(next);
      return next;
    });
  }, []);

  return <ThemeContext.Provider value={{ theme, toggle }}>{children}</ThemeContext.Provider>;
}

export function useTheme(): ThemeContextValue {
  const ctx = useContext(ThemeContext);
  if (!ctx) throw new Error("useTheme 必须在 ThemeProvider 内使用");
  return ctx;
}

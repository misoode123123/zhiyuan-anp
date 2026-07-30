"use client";

import { createContext, useCallback, useContext, useEffect, useState } from "react";
import { THEME_STORAGE_KEY } from "./theme-constants";

export type Theme = "light" | "dark";

export { THEME_STORAGE_KEY };

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
    if (stored) {
      // 同步 state 到 stored：SSR 渲染时 readInitialTheme 无 document 返回 'light'，
      // hydration 后图标需对齐实际主题（用户选了 dark 但 server 渲染了太阳图标）。
      setTheme(stored === "dark" ? "dark" : "light");
      return; // 用户已手动选，不跟随系统
    }
    const mql = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = (e: MediaQueryListEvent) => {
      // 重读 localStorage：用户在 mount 后手动切换过会写入 stored，此时停止跟随系统。
      const stored = localStorage.getItem(THEME_STORAGE_KEY);
      if (stored) return;
      const t: Theme = e.matches ? "dark" : "light";
      document.documentElement.classList.toggle("dark", t === "dark");
      setTheme(t);
    };
    mql.addEventListener("change", onChange);
    return () => mql.removeEventListener("change", onChange);
  }, []);

  const toggle = useCallback(() => {
    // 不在 setTheme updater 里调副作用（React 反模式，StrictMode 双调用致 localStorage 没存）。
    // 先算 next → applyTheme（设 html class + 存 localStorage）→ setTheme。
    const next: Theme = theme === "dark" ? "light" : "dark";
    applyTheme(next);
    setTheme(next);
  }, [theme]);

  return <ThemeContext.Provider value={{ theme, toggle }}>{children}</ThemeContext.Provider>;
}

export function useTheme(): ThemeContextValue {
  const ctx = useContext(ThemeContext);
  if (!ctx) throw new Error("useTheme 必须在 ThemeProvider 内使用");
  return ctx;
}

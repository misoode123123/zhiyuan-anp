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

// setThemeClass 只同步 <html> 的 dark class，不写 localStorage。
// mount 兜底用：即便防 FOUC 内联脚本被 CSP 拦截/产物过期未生效，hydration 后也能立即对齐 DOM，
// 杜绝「刷新变亮色」。内联脚本正常时本调用幂等，无副作用。
export function setThemeClass(theme: Theme) {
  document.documentElement.classList.toggle("dark", theme === "dark");
}

// applyTheme 把主题应用到 <html> + 持久化。仅浏览器端调（用户主动切换时）。
export function applyTheme(theme: Theme) {
  setThemeClass(theme);
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

  // 挂载时确定主题并对齐 DOM + state（兜底，不单点依赖内联脚本），再按需跟随系统。
  useEffect(() => {
    const mql = window.matchMedia("(prefers-color-scheme: dark)");
    const stored =
      typeof localStorage !== "undefined" ? localStorage.getItem(THEME_STORAGE_KEY) : null;
    const initial = resolveTheme(stored, mql.matches);
    // 兜底同步 DOM：内联脚本正常时幂等；失效（拦截/产物过期）时在此纠正。
    // 旧实现 stored 分支只 setTheme 不碰 DOM → 内联脚本一旦失效就「刷新变亮色、图标仍月亮」。
    setThemeClass(initial);
    setTheme(initial);

    // 用户已手动选（stored 合法）→ 不跟随系统。
    if (stored === "light" || stored === "dark") return;

    const onChange = (e: MediaQueryListEvent) => {
      // 重读 localStorage：用户在 mount 后手动切换过会写入 stored，此时停止跟随系统。
      if (localStorage.getItem(THEME_STORAGE_KEY)) return;
      const t = resolveTheme(null, e.matches);
      setThemeClass(t);
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

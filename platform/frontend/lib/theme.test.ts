import { describe, expect, it } from "vitest";
import { resolveTheme, THEME_STORAGE_KEY } from "./theme";
import { buildThemeInlineScript } from "./theme-constants";

describe("resolveTheme", () => {
  it("stored 值优先于系统", () => {
    expect(resolveTheme("dark", false)).toBe("dark");
    expect(resolveTheme("light", true)).toBe("light");
  });

  it("无 stored 跟随系统", () => {
    expect(resolveTheme(null, true)).toBe("dark");
    expect(resolveTheme(null, false)).toBe("light");
  });

  it("非法 stored 当作无 stored", () => {
    expect(resolveTheme("", true)).toBe("dark");
    expect(resolveTheme("system", false)).toBe("light");
  });
});

describe("THEME_STORAGE_KEY", () => {
  it("固定值", () => {
    expect(THEME_STORAGE_KEY).toBe("anp.theme");
  });
});

describe("buildThemeInlineScript", () => {
  const script = buildThemeInlineScript();

  it("用真实 key 名读 localStorage（不是 undefined）", () => {
    // 回归：layout.tsx 是 server component，曾从 "use client" 模块导入 const 导致
    // server 侧拿到 undefined，脚本变成 localStorage.getItem(undefined)，刷新丢主题。
    expect(script).toContain('localStorage.getItem("anp.theme")');
    expect(script).not.toContain("localStorage.getItem(undefined)");
  });

  it("包含设 dark class 的逻辑（防 FOUC）", () => {
    expect(script).toContain("classList.toggle('dark',d)");
  });
});

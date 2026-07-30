import { describe, expect, it } from "vitest";
import { resolveTheme, THEME_STORAGE_KEY } from "./theme";

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

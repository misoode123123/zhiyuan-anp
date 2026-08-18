// tab 状态机纯函数测试：开合/去重/选中转移/删除应用清理。
import { describe, it, expect } from "vitest";
import { EMPTY_TABS, openTab, selectTab, closeTab, pruneTabs, type TabState } from "./app-tabs";

describe("openTab", () => {
  it("新应用：追加到末尾并激活", () => {
    const t = openTab(EMPTY_TABS, "app_a");
    expect(t).toEqual({ ids: ["app_a"], activeId: "app_a" });
  });
  it("已打开应用：不重复追加，仅激活，顺序不变", () => {
    let t = openTab(EMPTY_TABS, "app_a");
    t = openTab(t, "app_b");
    t = openTab(t, "app_a");
    expect(t).toEqual({ ids: ["app_a", "app_b"], activeId: "app_a" });
  });
});

describe("selectTab", () => {
  it("已打开：切换激活", () => {
    const t: TabState = { ids: ["app_a", "app_b"], activeId: "app_a" };
    expect(selectTab(t, "app_b").activeId).toBe("app_b");
  });
  it("未打开：原样返回（入口统一走 openTab）", () => {
    const t: TabState = { ids: ["app_a"], activeId: "app_a" };
    expect(selectTab(t, "app_zz")).toBe(t);
  });
});

describe("closeTab", () => {
  const t: TabState = { ids: ["app_a", "app_b", "app_c"], activeId: "app_b" };
  it("关非激活 tab：激活不变", () => {
    const r = closeTab(t, "app_a");
    expect(r).toEqual({ ids: ["app_b", "app_c"], activeId: "app_b" });
  });
  it("关激活 tab：激活转移给右侧", () => {
    expect(closeTab(t, "app_b")).toEqual({ ids: ["app_a", "app_c"], activeId: "app_c" });
  });
  it("关最右激活 tab：激活转移给左侧", () => {
    const r = closeTab({ ids: ["app_a", "app_b"], activeId: "app_b" }, "app_b");
    expect(r).toEqual({ ids: ["app_a"], activeId: "app_a" });
  });
  it("关唯一 tab：回空态", () => {
    expect(closeTab({ ids: ["app_a"], activeId: "app_a" }, "app_a")).toEqual(EMPTY_TABS);
  });
});

describe("pruneTabs（应用删除后清理）", () => {
  it("ids 过滤 + activeId 失效时兜底最后一个", () => {
    const t: TabState = { ids: ["app_a", "app_b"], activeId: "app_a" };
    expect(pruneTabs(t, ["app_b"])).toEqual({ ids: ["app_b"], activeId: "app_b" });
  });
  it("无变化：返回原引用（避免无谓重渲染）", () => {
    const t: TabState = { ids: ["app_a"], activeId: "app_a" };
    expect(pruneTabs(t, ["app_a", "app_x"])).toBe(t);
  });
});

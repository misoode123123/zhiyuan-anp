// lib/api.ts 的 SSR 安全行为测试（示范测试）。
// 这些函数在浏览器环境会访问 window/localStorage，但都做了 SSR 守卫：
//   if (typeof window === "undefined") return <默认值>;
// vitest 默认 node 环境 window === undefined，正好覆盖「服务端渲染」分支，
// 无需任何 mock —— 用于锁定 SSR 时的回退默认值（改这些默认会破坏 RSC 渲染）。
//
// 注意：浏览器侧行为（写 localStorage、dispatchEvent）不在本文件覆盖范围；
// 若后续要测，需引入 jsdom 环境 + beforeEach 清 localStorage，本次不做。
import { describe, it, expect } from "vitest";
import {
  currentUser,
  currentProjectSpace,
  getAuthToken,
  isLoggedIn,
  API_BASE_URL,
  AGENT_RUNTIME_URL,
} from "./api";

describe("lib/api - SSR 安全回退（node 环境 window 未定义）", () => {
  it("currentUser() 回退默认管理员（与后端 SeedBootstrapAdmin 对齐）", () => {
    expect(currentUser()).toBe("admin");
  });

  it("currentProjectSpace() 回退空串（未选空间）", () => {
    expect(currentProjectSpace()).toBe("");
  });

  it("getAuthToken() 未登录时返回 null", () => {
    expect(getAuthToken()).toBeNull();
  });

  it("isLoggedIn() 在无 token 时返回 false", () => {
    expect(isLoggedIn()).toBe(false);
  });
});

describe("lib/api - 默认 endpoint 常量", () => {
  it("API_BASE_URL 默认指向后端 :8080", () => {
    // 未设置 NEXT_PUBLIC_API_BASE_URL 时回退到默认值（此断言锁定默认契约）
    expect(API_BASE_URL).toMatch(/:8080\/api\/v1/);
  });

  it("AGENT_RUNTIME_URL 默认指向运行时 :8001", () => {
    expect(AGENT_RUNTIME_URL).toMatch(/:8001/);
  });
});

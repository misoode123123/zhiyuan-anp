// 组件测试全局 setup（仅 components project 使用，jsdom 环境）。
// - 注册 @testing-library/jest-dom DOM 断言匹配器（toBeInTheDocument 等）；
// - 每个测试后清理 DOM、定时器、存储，避免交叉污染。
// 注：fetch mock 不在此装（setupFile 的 global 修改不稳定透传到测试体）；
// 各测试用 useFetchMock() 在测试体内强制装一个 vi.fn 到 globalThis.fetch。
import "@testing-library/jest-dom/vitest";
import { afterEach, vi } from "vitest";
import { cleanup } from "@testing-library/react";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  if (typeof window !== "undefined") {
    window.localStorage.clear();
    window.sessionStorage.clear();
  }
});

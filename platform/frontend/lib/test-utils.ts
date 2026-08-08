// 组件测试公用：装一个 vi.fn 到 globalThis.fetch，返回供配置。
// jsdom 30 自带原生 fetch 且属性常不可直接赋值，用 Object.defineProperty 强制覆写。
// 在测试体内调用（组件静态 import 之后）：组件里的裸 fetch 在运行时解析到 globalThis.fetch。
import { vi } from "vitest";

export function installFetchMock() {
  const fetchMock = vi.fn();
  Object.defineProperty(globalThis, "fetch", {
    value: fetchMock,
    writable: true,
    configurable: true,
  });
  return fetchMock;
}

// 成功信封响应（含 res.ok + res.json()，兼容 apiGet 与裸 fetch.then(json) 链）。
export function ok<T>(data: T, code = 0) {
  return { ok: true, status: 200, json: async () => ({ code, data }) } as unknown as Response;
}

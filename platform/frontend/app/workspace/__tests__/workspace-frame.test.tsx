// WorkspaceFrame 编码工作台 boot 测试（无需求化 + 双按钮入口 + 绑需求）：
// - 工具栏内 ModelSelect 渲染；
// - 点「▶ 继续编码」→ /workspace POST 不带 force_new（复用上次会话、不重发上下文）；
// - 点空状态「🆕 新会话」→ /workspace POST force_new=true（新建 + 注入应用上下文）；
// - 绑需求开发（带 ?req=…）→ /workspace POST body 含 requirement_id（注入由后端按 force_new 决定）。
//
// 用真实定时器（boot 有 400ms 防抖 < waitFor 默认 1000ms，无需 fake timers，更稳）。
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { installFetchMock, ok } from "@/lib/test-utils";

// useSearchParams 用 vi.fn 以便按用例切换（绑需求用例带 &req=…）。
const useSearchParamsMock = vi.hoisted(() => vi.fn());
vi.mock("next/navigation", () => ({
  useSearchParams: () => useSearchParamsMock(),
}));

import WorkspaceFrame from "../workspace-frame";

// 共用 mock：/detail 返应用；/users/me/models 返授权模型；/workspace POST 返 url。
// async → 返回 Promise(thenable)，组件用 fetch().then(r=>r.json()) 才不报 .then is not a function。
async function mockImpl(url: string | URL, init?: RequestInit) {
  const u = String(url);
  const method = init?.method ?? "GET";
  if (u.endsWith("/detail")) return ok({ application: {} });
  if (u.endsWith("/users/me/models"))
    return ok([{ id: "cmd_glm51", provider_id: "p", name: "glm-5.1" }]);
  if (u.endsWith("/workspace") && method === "POST") return ok({ url: "http://ws/x" });
  return ok({});
}

// 从 fetchMock 调用里取出第一个 POST /workspace 的 body。
function firstWorkspaceBody(fetchMock: ReturnType<typeof installFetchMock>) {
  const wsCall = fetchMock.mock.calls.find(
    (c) => String(c[0]).endsWith("/workspace") && (c[1] as RequestInit)?.method === "POST"
  );
  return JSON.parse(String((wsCall![1] as RequestInit).body));
}

describe("WorkspaceFrame 编码工作台", () => {
  beforeEach(() => {
    // 默认无需求绑定。
    useSearchParamsMock.mockReturnValue(new URLSearchParams("app=a1&ps=p1&tool=opencode"));
  });

  it("工具栏渲染 ModelSelect", async () => {
    const fetchMock = installFetchMock();
    fetchMock.mockResolvedValue(ok([{ id: "cmd_glm51", provider_id: "p", name: "glm-5.1" }]));
    render(<WorkspaceFrame />);
    expect(screen.getByText("模型")).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole("option", { name: "glm-5.1" })).toBeInTheDocument()
    );
  });

  it("点「继续编码」→ /workspace POST：复用上次会话（无 force_new、无 requirement_id/prompt）", async () => {
    const fetchMock = installFetchMock();
    fetchMock.mockImplementation(mockImpl);

    render(<WorkspaceFrame />);
    await waitFor(() =>
      expect((screen.getByRole("combobox") as HTMLSelectElement).value).toBe("cmd_glm51")
    );

    fireEvent.click(await screen.findByText("▶ 继续编码"));

    await waitFor(() => expect(firstWorkspaceBody(fetchMock)).toBeTruthy());
    const body = firstWorkspaceBody(fetchMock);
    expect(body.tool).toBe("opencode");
    expect(body.model).toBe("cmd_glm51");
    // 继续编码 = 复用：不带 force_new，不重发需求/上下文
    expect(body.force_new).toBeUndefined();
    expect(body.requirement_id).toBeUndefined();
    expect(body.prompt).toBeUndefined();
  });

  it("点空状态「新会话」→ /workspace POST：force_new=true（新建 + 后端注入应用上下文）", async () => {
    const fetchMock = installFetchMock();
    fetchMock.mockImplementation(mockImpl);

    render(<WorkspaceFrame />);
    await waitFor(() =>
      expect((screen.getByRole("combobox") as HTMLSelectElement).value).toBe("cmd_glm51")
    );

    // 空状态「🆕 新会话」按钮（区别于工具栏仅「🆕」的按钮）
    fireEvent.click(await screen.findByText("🆕 新会话"));

    await waitFor(() => expect(firstWorkspaceBody(fetchMock)).toBeTruthy());
    const body = firstWorkspaceBody(fetchMock);
    expect(body.force_new).toBe(true);
    expect(body.requirement_id).toBeUndefined();
  });

  it("绑需求开发（?req=r1）→ /workspace POST body 含 requirement_id（新会话注入需求规格）", async () => {
    const fetchMock = installFetchMock();
    fetchMock.mockImplementation(mockImpl);
    useSearchParamsMock.mockReturnValue(
      new URLSearchParams(
        "app=a1&ps=p1&tool=opencode&req=r1&rtitle=" + encodeURIComponent("用户登录")
      )
    );

    render(<WorkspaceFrame />);
    await waitFor(() =>
      expect((screen.getByRole("combobox") as HTMLSelectElement).value).toBe("cmd_glm51")
    );

    // 绑需求横幅可见
    expect(screen.getByText(/已绑定需求「用户登录」/)).toBeInTheDocument();

    fireEvent.click(await screen.findByText("🆕 新会话"));

    await waitFor(() => expect(firstWorkspaceBody(fetchMock)).toBeTruthy());
    const body = firstWorkspaceBody(fetchMock);
    // requirement_id 透传给后端；force_new=true 触发后端按 requirement_id 拼需求规格注入
    expect(body.requirement_id).toBe("r1");
    expect(body.force_new).toBe(true);
  });
});

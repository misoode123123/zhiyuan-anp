// WorkspaceFrame 编码工作台 boot 测试：
// - 工具栏内 ModelSelect 渲染；
// - 认领需求后触发 /workspace POST，其 body.model 取自 ModelSelect seed 的授权模型。
//
// 用真实定时器（boot 有 400ms 防抖 < waitFor 默认 1000ms，无需 fake timers，更稳）。
// Sidebar 整体 mock 成一个按钮，点击即触发 onStartReq → 认领 → setSelectedReq → boot。
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { installFetchMock, ok } from "@/lib/test-utils";

vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams("app=a1&ps=p1&tool=opencode"),
}));

vi.mock("../sidebar", () => ({
  Sidebar: ({ onStartReq }: { onStartReq: (id: string, fresh?: boolean) => void }) => (
    <div>
      <button onClick={() => onStartReq("req1")}>start-req</button>
      <button onClick={() => onStartReq("req1", true)}>start-req-fresh</button>
    </div>
  ),
}));

import WorkspaceFrame from "../workspace-frame";

describe("WorkspaceFrame 编码工作台", () => {
  it("工具栏渲染 ModelSelect", async () => {
    const fetchMock = installFetchMock();
    fetchMock.mockResolvedValue(ok([{ id: "cmd_glm51", provider_id: "p", name: "glm-5.1" }]));
    render(<WorkspaceFrame />);
    expect(screen.getByText("模型")).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole("option", { name: "glm-5.1" })).toBeInTheDocument()
    );
  });

  it("认领需求后 /workspace POST body.model = 授权模型（cmd_xxx）", async () => {
    const fetchMock = installFetchMock();
    fetchMock.mockImplementation(async (url: string | URL, init?: RequestInit) => {
      const u = String(url);
      const method = init?.method ?? "GET";
      if (u.endsWith("/detail"))
        return ok({ application: {}, requirements: [], changes: [], releases: [] });
      if (u.includes("/assign") && method === "POST") return ok({});
      if (u.endsWith("/users/me/models"))
        return ok([{ id: "cmd_glm51", provider_id: "p", name: "glm-5.1" }]);
      if (u.endsWith("/workspace") && method === "POST") return ok({ url: "http://ws/x" });
      return ok({});
    });

    render(<WorkspaceFrame />);

    // 等 ModelSelect 把授权模型 seed 进 state（modelRef 每次渲染同步最新值）
    await waitFor(() =>
      expect((screen.getByRole("combobox") as HTMLSelectElement).value).toBe("cmd_glm51")
    );

    // 认领需求 → 触发 boot（400ms 防抖后 POST /workspace）
    fireEvent.click(await screen.findByText("start-req"));

    await waitFor(() => {
      const ws = fetchMock.mock.calls.find(
        (c) => String(c[0]).endsWith("/workspace") && (c[1] as RequestInit)?.method === "POST"
      );
      expect(ws).toBeDefined();
    });

    const wsCall = fetchMock.mock.calls.find(
      (c) => String(c[0]).endsWith("/workspace") && (c[1] as RequestInit)?.method === "POST"
    );
    const body = JSON.parse(String((wsCall![1] as RequestInit).body));
    expect(body.tool).toBe("opencode");
    expect(body.requirement_id).toBe("req1");
    expect(body.model).toBe("cmd_glm51");
  });

  it("需求列表「新会话」→ /workspace POST body.force_new = true", async () => {
    const fetchMock = installFetchMock();
    fetchMock.mockImplementation(async (url: string | URL, init?: RequestInit) => {
      const u = String(url);
      const method = init?.method ?? "GET";
      if (u.endsWith("/detail"))
        return ok({ application: {}, requirements: [], changes: [], releases: [] });
      if (u.includes("/assign") && method === "POST") return ok({});
      if (u.endsWith("/users/me/models")) return ok([]);
      if (u.endsWith("/workspace") && method === "POST") return ok({ url: "http://ws/x" });
      return ok({});
    });

    render(<WorkspaceFrame />);

    // 点 mock 的「fresh」入口 → onStartReq("req1", true) → force_new 透传到 boot body
    fireEvent.click(await screen.findByText("start-req-fresh"));

    await waitFor(() => {
      const ws = fetchMock.mock.calls.find(
        (c) => String(c[0]).endsWith("/workspace") && (c[1] as RequestInit)?.method === "POST"
      );
      expect(ws).toBeDefined();
    });

    const wsCall = fetchMock.mock.calls.find(
      (c) => String(c[0]).endsWith("/workspace") && (c[1] as RequestInit)?.method === "POST"
    );
    const body = JSON.parse(String((wsCall![1] as RequestInit).body));
    expect(body.force_new).toBe(true);
  });
});

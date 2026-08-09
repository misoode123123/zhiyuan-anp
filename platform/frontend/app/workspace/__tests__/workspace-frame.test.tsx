// WorkspaceFrame 编码工作台 boot 测试（无需求化）：
// - 工具栏内 ModelSelect 渲染；
// - 点「🚀 开始编码」自主发起 → /workspace POST，body.model = 授权模型、force_new=true、无 requirement_id。
//
// 用真实定时器（boot 有 400ms 防抖 < waitFor 默认 1000ms，无需 fake timers，更稳）。
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { installFetchMock, ok } from "@/lib/test-utils";

vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams("app=a1&ps=p1&tool=opencode"),
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

  it("点「开始编码」自主发起 → /workspace POST：model=授权模型、force_new=true、无 requirement_id", async () => {
    const fetchMock = installFetchMock();
    fetchMock.mockImplementation(async (url: string | URL, init?: RequestInit) => {
      const u = String(url);
      const method = init?.method ?? "GET";
      if (u.endsWith("/detail")) return ok({ application: {} });
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

    // 点「开始编码」→ 自主发起 boot（400ms 防抖后 POST /workspace）
    fireEvent.click(await screen.findByText("🚀 开始编码"));

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
    expect(body.model).toBe("cmd_glm51");
    expect(body.force_new).toBe(true);
    // 无需求化：不应再带 requirement_id / prompt
    expect(body.requirement_id).toBeUndefined();
    expect(body.prompt).toBeUndefined();
  });
});

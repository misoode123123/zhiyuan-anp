// WorkspaceToolbar 组件测试：验证 ModelSelect 已挂载到工作台工具栏。
// ModelSelect 自身（/users/me/models 拉取 + 受控 onChange）由 lib/model-select.test.ts
// 覆盖纯逻辑；本测试只锁定「工具栏里渲染了模型下拉」这一接线。
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { WorkspaceToolbar, type DeployState } from "../workspace-toolbar";
import { installFetchMock, ok } from "@/lib/test-utils";

const baseProps = {
  appID: "app_1",
  appName: "我的应用",
  tool: "opencode",
  model: "",
  onModelChange: () => {},
  deployState: "idle" as DeployState,
  testUrl: "",
  deployErr: "",
  onDeploy: () => {},
  onRegister: () => {},
  registering: false,
  onOpenWindow: () => {},
  onReconnect: () => {},
  onNewSession: () => {},
  drawerOpen: true,
  onToggleDrawer: () => {},
};

describe("WorkspaceToolbar", () => {
  it("渲染 ModelSelect（模型下拉出现在工具栏）", async () => {
    const fetchMock = installFetchMock();
    fetchMock.mockResolvedValue(ok([{ id: "cmd_glm51", provider_id: "p", name: "glm-5.1" }]));

    render(<WorkspaceToolbar {...baseProps} />);

    // ModelSelect 永远渲染「模型」label；waitFor 等 /users/me/models resolve 后授权模型 option 出现
    expect(screen.getByText("模型")).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByRole("option", { name: "glm-5.1" })).toBeInTheDocument();
    });
    // 证明 /users/me/models 被工具栏内的 ModelSelect 发起
    expect(fetchMock.mock.calls.some((c) => String(c[0]).endsWith("/users/me/models"))).toBe(true);
  });

  it("点击「🆕 新会话」触发 onNewSession", async () => {
    const fetchMock = installFetchMock();
    fetchMock.mockResolvedValue(ok([{ id: "cmd_glm51", provider_id: "p", name: "glm-5.1" }]));

    const onNewSession = vi.fn();
    render(<WorkspaceToolbar {...baseProps} onNewSession={onNewSession} />);

    // 等 ModelSelect 的 /users/me/models 落定，避免未处理 promise 噪声
    await waitFor(() =>
      expect(screen.getByRole("option", { name: "glm-5.1" })).toBeInTheDocument()
    );

    fireEvent.click(screen.getByTitle(/开新会话/));
    expect(onNewSession).toHaveBeenCalledOnce();
  });
});

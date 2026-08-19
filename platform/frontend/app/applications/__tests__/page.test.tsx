// ApplicationsPage 壳冒烟（jsdom）：总览条渲染应用格 → 点格开 tab 显示面板 →
// ✕ 关闭回空态。fetch 全 mock（installFetchMock）；next/navigation mock 掉
// （AppTabPanel 的 useRouter 在 jsdom 无 router 上下文会抛错）。
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import ApplicationsPage from "../page";
import { installFetchMock, ok } from "@/lib/test-utils";
import type { App } from "../_lib/types";

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn() }),
}));

const app: App = {
  id: "app_smoke",
  name: "冒烟应用",
  repo_dir: "/data/repos/s",
  internal_port: 8080,
  image: "img",
  container_name: "c",
  host_port: 9103,
  url: "http://x",
  version: 1,
  status: "running",
  last_error: "",
  build_log: "",
  deploy_mode: "managed",
  external_url: "",
  app_kind: "web",
  network_mode: "bridge",
  updated_at: "2026-08-19T00:00:00Z",
  instances: [
    {
      env: "test",
      status: "running",
      url: "u",
      version: 1,
      host_port: 9103,
      image: "i",
      updated_at: "t",
    },
  ],
};

const detailData = {
  application: app,
  requirements: [],
  changes: [],
  releases: [],
  commits: [],
  instances: app.instances!,
  sessions: [],
  tasks: [],
  routes: [],
  deps: [],
  deploy_history: [],
};

function install() {
  const m = installFetchMock();
  m.mockImplementation((input: unknown) => {
    const url = String(input);
    if (url.endsWith("/project-spaces"))
      return Promise.resolve(ok([{ id: "ps_default", name: "默认", slug: "default" }]));
    if (url.endsWith("/deploy-nodes")) return Promise.resolve(ok([]));
    // /deps 须返数组（DepsSection 直接 .map；不能落进下方 detail 兜底分支）
    if (url.endsWith("/deps")) return Promise.resolve(ok([]));
    if (url.includes("/apps/app_smoke/")) return Promise.resolve(ok(detailData));
    if (url.endsWith("/apps")) return Promise.resolve(ok([app]));
    if (url.endsWith("/stats?env=prod"))
      return Promise.resolve(ok({ health: "up", stats: {}, deployed: false }));
    return Promise.resolve(ok({}));
  });
  return m;
}

describe("ApplicationsPage tab 化壳", () => {
  it("总览条渲染应用格；点击开 tab 显示面板；✕ 关闭回空态", async () => {
    install();
    render(<ApplicationsPage />);
    // 等应用格出现（overview-bar 首帧即渲染，apps 异步到）；未选空态提示（有应用但未开 tab）
    await waitFor(() => screen.getByText("冒烟应用"));
    expect(screen.getByText("点击上方应用格子打开工作区。")).toBeInTheDocument();
    fireEvent.click(screen.getByText("冒烟应用"));
    await waitFor(() => screen.getByTestId("env-card-test")); // 面板渲染（test 环境卡）
    expect(screen.getByTestId("app-tab-panel")).toBeInTheDocument();
    expect(screen.getByTitle("关闭 tab")).toBeInTheDocument(); // tab 页签行 ✕
    fireEvent.click(screen.getByTitle("关闭 tab"));
    await waitFor(() => expect(screen.queryByTestId("env-card-test")).toBeNull());
    // 关闭后回空态、页签行消失
    expect(screen.queryByTitle("关闭 tab")).toBeNull();
    expect(screen.getByText("点击上方应用格子打开工作区。")).toBeInTheDocument();
  });
});

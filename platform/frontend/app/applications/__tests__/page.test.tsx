// ApplicationsPage 壳冒烟（jsdom）：总览条渲染应用格 → 点格开 tab 显示面板 →
// ✕ 关闭回空态。fetch 全 mock（installFetchMock）；next/navigation mock 掉
// （AppTabPanel 的 useRouter 在 jsdom 无 router 上下文会抛错）。
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor, within, act } from "@testing-library/react";
import ApplicationsPage from "../page";
import { installFetchMock, ok } from "@/lib/test-utils";
import type { App, Detail } from "../_lib/types";

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

// 第二应用（双用例：跨 tab 切换不串台）。repo_dir 独立 → 断言「B 面板已挂载」可定位。
const appB: App = { ...app, id: "app_second", name: "第二应用", repo_dir: "/data/repos/b" };

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

// fetch 全 mock：apps/logs/detail 均按 app id 区分返回（「不串台」断言依赖可区分内容）。
function install(apps: App[] = [app], logsByApp: Record<string, string> = {}) {
  const detailByApp = new Map(apps.map((a) => [a.id, { ...detailData, application: a }]));
  const m = installFetchMock();
  m.mockImplementation((input: unknown) => {
    const url = String(input);
    if (url.endsWith("/project-spaces"))
      return Promise.resolve(ok([{ id: "ps_default", name: "默认", slug: "default" }]));
    if (url.endsWith("/deploy-nodes")) return Promise.resolve(ok([]));
    // /deps 须返数组（DepsSection 直接 .map；不能落进下方 detail 兜底分支）
    if (url.endsWith("/deps")) return Promise.resolve(ok([]));
    const logs = url.match(/\/apps\/([^/]+)\/logs$/);
    if (logs) return Promise.resolve(ok({ logs: logsByApp[logs[1]] ?? "(无)" }));
    const detail = url.match(/\/apps\/([^/]+)\/detail$/);
    if (detail) return Promise.resolve(ok(detailByApp.get(detail[1]) ?? detailData));
    if (url.endsWith("/apps")) return Promise.resolve(ok(apps));
    if (url.endsWith("/stats?env=prod"))
      return Promise.resolve(ok({ health: "up", stats: {}, deployed: false }));
    return Promise.resolve(ok({}));
  });
  return m;
}

// detail 路径调用计数（/apps/<id>/detail 命中次数；loadChanges 与面板 loadDetail 共用此路径）
function detailHits(m: { mock: { calls: unknown[][] } }, appID: string): number {
  return m.mock.calls.filter((c) => String(c[0]).endsWith(`/apps/${appID}/detail`)).length;
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

  // T9 评审 Important #1：openPanel/logs 等是壳级 state，切 tab 若不重挂+清态，
  // B 面板会直接渲染 A 的日志内容（数据误归属）。
  it("A 开日志后切 B：B 不显示 A 的日志；回 A 再开日志正常", async () => {
    install([app, appB], { app_smoke: "A 的日志内容", app_second: "B 的日志内容" });
    render(<ApplicationsPage />);
    await waitFor(() => screen.getByTestId("overview-bar"));
    const bar = screen.getByTestId("overview-bar");
    await waitFor(() => within(bar).getByText("冒烟应用")); // 等两格都到
    const cell = (name: string) => within(bar).getByText(name); // 总览格（页签名同名，避免歧义）
    // 开 A tab → 点工具行「日志」（app-tab-panel 工具行按钮）→ 日志面板出现
    fireEvent.click(cell("冒烟应用"));
    await waitFor(() => screen.getByTestId("app-tab-panel"));
    fireEvent.click(screen.getAllByText("日志")[0]);
    await waitFor(() => expect(screen.getByText("A 的日志内容")).toBeInTheDocument());
    // 切 B（点 B 总览格）：A 的日志内容不得残留（面板重挂+展开态已清）
    fireEvent.click(cell("第二应用"));
    await waitFor(() => screen.getAllByText("/data/repos/b")); // B 面板已挂载（repo code 块）
    expect(screen.queryByText("A 的日志内容")).toBeNull();
    expect(screen.queryByText("B 的日志内容")).toBeNull(); // 展开态已清 ≠ B 数据误拉
    // 回 A：再点「日志」正常显示 A 的内容
    fireEvent.click(cell("冒烟应用"));
    await waitFor(() => screen.getAllByText("/data/repos/s"));
    fireEvent.click(screen.getAllByText("日志")[0]);
    await waitFor(() => expect(screen.getByText("A 的日志内容")).toBeInTheDocument());
  });

  // T9 评审追修 round 2 Important：closeTab 激活转移路径——关激活 tab 时 activeId 转移
  // 相邻（app-tabs closeTab），壳级 openPanel/logs 若不清，新激活面板会渲染旧应用日志。
  it("A 开日志 → ✕ 关 A tab（激活转移 B）：B 面板不显示 A 的日志", async () => {
    install([app, appB], { app_smoke: "A 的日志内容", app_second: "B 的日志内容" });
    render(<ApplicationsPage />);
    await waitFor(() => screen.getByTestId("overview-bar"));
    const bar = screen.getByTestId("overview-bar");
    await waitFor(() => within(bar).getByText("冒烟应用")); // 等两格都到
    const cell = (name: string) => within(bar).getByText(name);
    // 开 A → 开 B（B 激活）→ 回 A 激活 → A 开日志 → ✕ 关 A：激活转移 B（B 仍在）
    fireEvent.click(cell("冒烟应用"));
    await waitFor(() => screen.getByTestId("app-tab-panel"));
    fireEvent.click(cell("第二应用"));
    await waitFor(() => screen.getAllByText("/data/repos/b"));
    fireEvent.click(cell("冒烟应用"));
    await waitFor(() => screen.getAllByText("/data/repos/s"));
    fireEvent.click(screen.getAllByText("日志")[0]);
    await waitFor(() => expect(screen.getByText("A 的日志内容")).toBeInTheDocument());
    // 页签行 ✕（ids 顺序 [A,B] → [0]=A 的 ✕）
    fireEvent.click(screen.getAllByTitle("关闭 tab")[0]);
    await waitFor(() => screen.getAllByText("/data/repos/b")); // B 面板已挂载（激活转移）
    expect(screen.queryByText("A 的日志内容")).toBeNull(); // A 日志不得残留
    expect(screen.queryByText("B 的日志内容")).toBeNull(); // 展开态已清 ≠ B 数据误拉
  });

  // 终审 Important #1：detail 原仅挂载/重试/闭环动作拉取，部署完成后（壳 3s 轮询推来
  // building→running+version+1）卡内时间线不刷新。fake timers 驱动壳轮询 tick，mock 返回
  // 可变 live 状态（/apps 与 detail 同源）；v2 部署完成后 detail 带 1 条 test 历史：
  // 不重拉则时间线停在首帧 0 条，重拉后「部署历史（1）」可见。
  it("running→building→running+version+1：部署完成即重拉 detail（时间线含新部署）", async () => {
    vi.useFakeTimers();
    // 可变后端状态：轮询间由测试推演（building → running+v2 + 新增部署历史）
    const live: { apps: App[]; history: Detail["deploy_history"] } = {
      apps: [{ ...app, status: "running", version: 1 }],
      history: [],
    };
    const m = installFetchMock();
    m.mockImplementation((input: unknown) => {
      const url = String(input);
      if (url.endsWith("/project-spaces"))
        return Promise.resolve(ok([{ id: "ps_default", name: "默认", slug: "default" }]));
      if (url.endsWith("/deploy-nodes")) return Promise.resolve(ok([]));
      if (url.endsWith("/deps")) return Promise.resolve(ok([]));
      if (url.endsWith(`/apps/${app.id}/detail`))
        return Promise.resolve(
          ok({ ...detailData, application: live.apps[0], deploy_history: live.history })
        );
      if (url.endsWith("/apps")) return Promise.resolve(ok(live.apps));
      if (url.endsWith("/stats?env=prod"))
        return Promise.resolve(ok({ health: "up", stats: {}, deployed: false }));
      return Promise.resolve(ok({}));
    });
    render(<ApplicationsPage />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10); // 挂载拉 spaces/apps/detail（flush 微任务）
    });
    fireEvent.click(screen.getByText("冒烟应用"));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10); // 面板挂载 detail #1（首帧 0 条历史）
    });
    expect(screen.getByTestId("env-card-test")).toBeInTheDocument();
    expect(screen.queryByText(/部署历史/)).toBeNull();
    // 轮询 tick 1：apps 推来 building（status 变化但未离开展示中 → 面板不重拉）
    live.apps = [{ ...app, status: "building", version: 1 }];
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });
    expect(screen.getByText("构建部署中...")).toBeInTheDocument();
    const hitsDuringBuilding = detailHits(m, app.id);
    // 轮询 tick 2：部署完成 running + version+1（离开展示中且版本增大 → 重拉 detail）
    live.apps = [{ ...app, status: "running", version: 2 }];
    live.history = [
      {
        id: 42,
        env: "test",
        version: 2,
        engine: "fixed",
        result: "success",
        operator: "yxt",
        duration_sec: 8,
        created_at: "2026-08-19T00:00:30Z",
      },
    ];
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });
    // 时间线即时可见新部署（不切走再切回）
    expect(screen.getByText("部署历史（1）")).toBeInTheDocument();
    // 计数：本轮 tick 的 loadChanges(经 [apps] effect) +1、面板 loadDetail +1——恰好一次
    // 重拉，非每轮重拉
    expect(detailHits(m, app.id)).toBe(hitsDuringBuilding + 2);
  });
});

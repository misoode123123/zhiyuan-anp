"use client";

import { ModelSelect } from "@/app/_components/model-select";

export type DeployState = "idle" | "building" | "running" | "failed";

// 编码工作台顶部工具条:应用名/工具 + 抽屉开关 + 构建部署(test)+ 部署状态 + opencode 新窗口/重连。
// 纯展示+回调,状态由 WorkspaceFrame 注入。
export function WorkspaceToolbar({
  appID,
  appName,
  tool,
  model,
  onModelChange,
  deployState,
  testUrl,
  deployErr,
  onDeploy,
  onRegister,
  registering,
  onOpenWindow,
  onReconnect,
  onNewSession,
  drawerOpen,
  onToggleDrawer,
}: {
  appID: string;
  appName?: string;
  tool: string;
  model: string;
  onModelChange: (v: string) => void;
  deployState: DeployState;
  testUrl: string;
  deployErr: string;
  onDeploy: () => void;
  onRegister: () => void;
  registering: boolean;
  onOpenWindow: () => void;
  onReconnect: () => void;
  onNewSession: () => void;
  drawerOpen: boolean;
  onToggleDrawer: () => void;
}) {
  return (
    <div className="border-b border-border bg-surface-2">
      <div className="flex items-center justify-between gap-2 px-3 py-1 text-xs">
        <span className="flex min-w-0 items-center gap-2">
          {!drawerOpen && (
            <button onClick={onToggleDrawer} className="text-text-muted" title="展开项目上下文">
              ☰
            </button>
          )}
          <span className="truncate text-text-muted">
            🧑‍💻 编码工作台 ·{" "}
            <span className="font-semibold text-text">{appName || appID || "?"}</span> · {tool}
          </span>
        </span>
        <span className="flex shrink-0 items-center gap-3">
          <ModelSelect
            taskType="code"
            value={model}
            onChange={onModelChange}
            className="min-w-[140px]"
          />
          <button
            onClick={onDeploy}
            disabled={deployState === "building"}
            className={`rounded px-2 py-0.5 ${deployState === "building" ? "bg-warn/20 text-warn" : "bg-accent text-accent-fg"}`}
            title="把当前代码构建并部署到 test 环境(需先在 opencode 里 commit)"
          >
            {deployState === "building" ? "构建中…" : "⚙ 构建部署(test)"}
          </button>
          <button
            onClick={onRegister}
            disabled={registering}
            className="rounded bg-warn/20 px-2 py-0.5 text-warn"
            title="把 opencode 编码的产出登记为待审批变更;审批通过才能上线 prod"
          >
            {registering ? "登记中…" : "📝 登记变更"}
          </button>
          <button onClick={onOpenWindow} className="text-accent" title="opencode 开新窗口">
            ↗
          </button>
          <button onClick={onReconnect} className="text-text-muted" title="重连工作台">
            重连
          </button>
          <button
            onClick={onNewSession}
            className="text-accent"
            title="开新会话（丢弃当前上下文，需求内容用「🤖 AI 编码」重新注入）"
          >
            🆕 新会话
          </button>
          <a href="/applications" className="text-accent" title="返回应用部署">
            ← 应用
          </a>
        </span>
      </div>
      {deployState === "running" && testUrl && (
        <div className="flex items-center gap-2 bg-success/10 px-3 py-1 text-success">
          <span>✅ test 已部署,点击打开：</span>
          <a
            href={testUrl}
            target="_blank"
            rel="noreferrer"
            className="rounded bg-success px-3 py-0.5 font-medium text-accent-fg"
          >
            ▶ 打开 test 环境
          </a>
        </div>
      )}
      {deployState === "failed" && deployErr && (
        <div className="bg-danger/10 px-3 py-0.5 text-danger">❌ {deployErr}</div>
      )}
      <div className="px-3 py-0.5 text-[11px] text-text-muted">
        💡 步骤：① 在 opencode 对话框输入"提交代码"让 AI commit → ② 点「构建部署(test)」→ ③ 点「打开
        test 环境」查看效果
      </div>
    </div>
  );
}

"use client";

// tab 面板自包含展示块（自 app-tab-panel.tsx 原样抽出，JSX/文案/类名零改动）：
// StatusBanners=顶部状态横幅组（importing/building·preparing/环境构建/failed 兜底）；
// ExternalProbeCard=external 应用探活卡。抽出动机：app-tab-panel 守「组件 ≤400 行」纪律。
import type { App, AppStats } from "../_lib/types";

// 面板顶部状态横幅组（自 AppTabPanel 抽出，JSX 等价）：importing / building·preparing /
// running 中环境构建 / app 级 failed 兜底（构建准备期失败两卡均无 failed 实例→卡内无
// 重试按钮，此处兜底）。
export function StatusBanners(props: {
  app: App;
  isImporting: boolean;
  retryEnv: string;
  onRetryFixed: () => void;
}) {
  const { app, isImporting, onRetryFixed } = props;
  return (
    <>
      {isImporting && (
        <div className="mb-2 flex items-center gap-2 rounded bg-warn/10 px-3 py-1.5 text-sm text-warn">
          <span className="inline-block h-3 w-3 animate-spin rounded-full border-2 border-warn border-t-transparent"></span>
          导入已有项目中...
          <span className="truncate text-xs text-warn">{app.last_error || "(等待进度...)"}</span>
          <span className="ml-auto text-xs text-warn">每3秒自动刷新</span>
        </div>
      )}
      {(app.status === "building" || app.status === "preparing") && (
        <div className="mb-2 flex items-center gap-2 rounded bg-accent/10 px-3 py-1.5 text-sm text-accent">
          <span className="inline-block h-3 w-3 animate-spin rounded-full border-2 border-accent border-t-transparent"></span>
          {app.status === "preparing" ? "AI 部署中（简报→执行→验证）..." : "构建部署中..."}
          {app.instances?.find((i) => i.status === "building" || i.status === "preparing") && (
            <span className="text-xs text-accent">
              {app.instances.find((i) => i.status === "building" || i.status === "preparing")
                ?.url || ""}
            </span>
          )}
          <span className="ml-auto text-xs text-accent">每3秒自动刷新</span>
        </div>
      )}
      {app.status === "running" && app.instances?.some((i) => i.status === "building") && (
        <div className="mb-2 flex items-center gap-2 rounded bg-accent/10 px-3 py-1.5 text-sm text-accent">
          <span className="inline-block h-3 w-3 animate-spin rounded-full border-2 border-accent border-t-transparent"></span>
          {app.instances.find((i) => i.status === "building")?.env} 环境构建中...
          <span className="ml-auto text-xs text-accent">每3秒自动刷新</span>
        </div>
      )}
      {app.status === "failed" && (
        <div className="mb-2 rounded bg-danger/10 px-3 py-1.5 text-sm text-danger">
          <div className="flex items-center gap-2">
            <span className="flex-1">
              ❌ 构建失败：{app.last_error?.slice(0, 100) || "(无错误摘要)"}
            </span>
            <button
              onClick={onRetryFixed}
              className="shrink-0 rounded bg-surface-2 px-2 py-0.5 text-xs text-text"
              title="放弃 AI 引擎，用固定部署引擎重试本次构建部署（spec §5：失败不静默降级，由人工选择）"
            >
              🔧 用固定引擎重试
            </button>
          </div>
          {app.build_log && (
            <details className="mt-1">
              <summary className="cursor-pointer text-xs text-danger">查看构建日志详情</summary>
              <pre className="mt-1 max-h-64 overflow-auto rounded bg-neutral-900 p-2 text-xs text-green-300 whitespace-pre-wrap">
                {app.build_log}
              </pre>
            </details>
          )}
        </div>
      )}
    </>
  );
}

// external 应用探活卡（自上方环境并排区抽出，JSX 等价）：无容器/test-prod 实例，
// 只显示 external_url 健康状态；stats 未采到（deployed 无值）时不渲染。
export function ExternalProbeCard({ appStats }: { appStats?: AppStats }) {
  if (!appStats?.deployed) return null;
  return (
    <div className="mt-2 rounded bg-accent/10 p-2 text-xs">
      <div className="flex flex-wrap items-center gap-2">
        <span className="rounded bg-accent/10 px-1.5 py-0.5 font-medium text-accent">
          external 探活
        </span>
        <span className="text-text-muted">健康</span>
        <span
          className={(appStats.health === "up" ? "text-success" : "text-danger") + " font-medium"}
        >
          {appStats.health}
        </span>
      </div>
    </div>
  );
}

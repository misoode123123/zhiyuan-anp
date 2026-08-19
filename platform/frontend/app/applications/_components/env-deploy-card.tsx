"use client";

// 环境并排卡（spec §2）：tab 内 test/prod 左右各一张。
// 组成（自原 page.tsx 等价平移重排，样式类沿用原值）：
//   头：🧪测试test / 🚀生产prod 徽章 + 实例状态徽章（STATUS_COLOR）+ vN（原 :587-608）；
//   体：实例 URL 链接（无实例=提示文案「未部署/未上线」）+ prod 健康（stats.health/cpu/mem，
//       原 :609-642 同款）+ prod 待上线变更摘要（唯一新写小块：approved 由壳过滤传入，
//       卡内只显示「待上线变更 N 条」+ title 悬浮明细；逐条列表保留在五栏 Detail「研发」栏）；
//   时间线：historyForEnv(history, env) → 折叠列表（原 :928-976 平移，dh 字段渲染一字不改）；
//   按钮（吸附本环境）：test=[构建部署(test·master)][日志]；prod=[🚀上线(prod)]；
//       实例 failed 时显示 [🔧用固定引擎重试]（文案/提示取原值 :333-351）。
import { STATUS_COLOR, historyForEnv, isDockerKind } from "../_lib/predicates";
import type { App, AppStats, ChangeSummary, Detail, Instance } from "../_lib/types";

function changeSummaryText(c: ChangeSummary): string {
  // 与原待上线块同款提取：【总结】行优先，回退 id 前 12 位，截 50（原 :660-662）。
  return ((c.output || "").match(/【总结】(.+)/)?.[1] || c.id.slice(0, 12)).slice(0, 50);
}

export function EnvDeployCard(props: {
  app: App;
  env: "test" | "prod";
  instance?: Instance;
  stats?: AppStats;
  history: Detail["deploy_history"]; // 该应用全量历史（卡内 historyForEnv 过滤）
  pendingChanges?: ChangeSummary[]; // prod：已 approved 的待上线变更（壳过滤后传入）
  deployMsg?: string;
  busy: boolean; // importing 等禁用态
  onDeploy: () => void; // test：act(id,"deploy","test",selectedNode)
  onPromote: () => void; // prod：promoteWithNode(id, selectedNode)
  onRetryFixed: () => void; // 失败重试：act(id,"deploy",env,selectedNode,"fixed")
  onLogs: () => void; // 构建日志（test 卡）
}) {
  const { app, env, instance, stats, history, pendingChanges, deployMsg, busy } = props;
  const { onDeploy, onPromote, onRetryFixed, onLogs } = props;
  const hist = historyForEnv(history, env);
  const label = env === "prod" ? "🚀 生产 prod" : "🧪 测试 test";
  // 容器部署按钮仅 web/service/headless 形态（desktop/mobile/cli 走构建产物流程，I-4）
  const canDeploy = isDockerKind(app.app_kind);
  const pending = env === "prod" ? (pendingChanges ?? []) : [];
  return (
    <div className="rounded bg-bg p-2 text-xs" data-testid={`env-card-${env}`}>
      <div className="flex items-center gap-2">
        <span
          className={`rounded px-1.5 py-0.5 font-medium ${env === "prod" ? "bg-accent/10 text-accent" : "bg-warn/10 text-warn"}`}
        >
          {label}
        </span>
        {instance && (
          <span
            className={`rounded px-1.5 py-0.5 ${STATUS_COLOR[instance.status] ?? "bg-surface-2"}`}
          >
            {instance.status}
          </span>
        )}
        {instance && instance.version > 0 && (
          <span className="text-text-muted">v{instance.version}</span>
        )}
      </div>
      {deployMsg && <div className="mt-1 text-accent">{deployMsg}</div>}
      {instance?.url ? (
        <a
          href={instance.url}
          target="_blank"
          rel="noreferrer"
          className="mt-1 block truncate text-accent hover:underline"
        >
          {instance.url}
        </a>
      ) : (
        <div className="mt-1 text-text-muted">
          {env === "prod" ? "未上线（点「上线」部署）" : "未部署（发布或「构建部署」）"}
        </div>
      )}
      {env === "prod" && stats?.deployed && (
        <div className="mt-1 flex flex-wrap items-center gap-2 text-[11px]">
          <span className="text-text-muted">健康</span>
          <span
            className={(stats.health === "up" ? "text-success" : "text-danger") + " font-medium"}
          >
            {stats.health}
          </span>
          {stats.cpu && (
            <span className="text-text-muted">
              CPU {stats.cpu} · 内存 {stats.mem}
            </span>
          )}
        </div>
      )}
      {env === "prod" && pending.length > 0 && (
        <div
          className="mt-1 truncate rounded bg-warn/10 p-1 text-[11px] text-warn"
          title={pending
            .map((c) =>
              c.created_at
                ? changeSummaryText(c) +
                  "（" +
                  new Date(c.created_at).toLocaleString("zh-CN", {
                    hour12: false,
                    month: "2-digit",
                    day: "2-digit",
                    hour: "2-digit",
                    minute: "2-digit",
                  }) +
                  "）"
                : changeSummaryText(c)
            )
            .join("\n")}
        >
          📋 待上线变更（{pending.length} 条）：{changeSummaryText(pending[0])}
        </div>
      )}
      {hist.length > 0 && (
        <div className="mt-1">
          <details>
            <summary className="cursor-pointer text-text-muted">部署历史（{hist.length}）</summary>
            <div className="mt-1 max-h-48 space-y-0.5 overflow-y-auto">
              {hist.map((dh) => (
                <div key={dh.id} className="flex items-center gap-1 text-xs">
                  <span className="text-text-muted">
                    {new Date(dh.created_at).toLocaleString("zh-CN", {
                      hour12: false,
                    })}
                  </span>
                  <span className="rounded bg-surface-2 px-1">v{dh.version}</span>
                  <span title={dh.engine === "ai" ? "AI 引擎" : "固定引擎"}>
                    {dh.engine === "ai" ? "🤖" : "🔧"}
                  </span>
                  <span
                    className={
                      dh.result === "success"
                        ? "text-success"
                        : dh.result === "failed"
                          ? "text-danger"
                          : "text-warn"
                    }
                  >
                    {dh.result === "success" ? "成功" : dh.result === "failed" ? "失败" : "部署中…"}
                  </span>
                  {dh.duration_sec != null && <span>{dh.duration_sec}s</span>}
                  <span className="text-text-muted">{dh.operator}</span>
                  {(dh.error_summary || dh.notes) && (
                    <span
                      className="text-text-muted"
                      title={`${dh.error_summary ?? ""}${dh.notes ? "\n" + dh.notes : ""}`}
                    >
                      ⓘ
                    </span>
                  )}
                </div>
              ))}
            </div>
          </details>
        </div>
      )}
      <div className="mt-2 flex flex-wrap items-center gap-1">
        {env === "test" && canDeploy && (
          <button
            onClick={onDeploy}
            className="rounded bg-accent/10 px-2 py-0.5 text-xs text-accent disabled:cursor-not-allowed disabled:opacity-40"
            disabled={busy}
            title={
              busy
                ? "导入完成前不可部署"
                : "从主仓(master)代码构建部署到 test 环境——不含编码工作台 dev 分支未合并的改动；要部署 AI 最新代码请到编码工作台点「构建部署」"
            }
          >
            构建部署(test·master)
          </button>
        )}
        {env === "test" && (
          <button onClick={onLogs} className="rounded bg-surface-2 px-2 py-0.5 text-xs">
            日志
          </button>
        )}
        {env === "prod" && canDeploy && (
          <button
            onClick={onPromote}
            className="rounded bg-success/10 px-2 py-0.5 text-xs text-success disabled:cursor-not-allowed disabled:opacity-40"
            disabled={busy}
            title={busy ? "导入完成前不可上线" : "上线到 prod"}
          >
            🚀 上线(prod)
          </button>
        )}
        {instance?.status === "failed" && (
          <button
            onClick={onRetryFixed}
            className="shrink-0 rounded bg-surface-2 px-2 py-0.5 text-xs text-text"
            title="放弃 AI 引擎，用固定部署引擎重试本次构建部署（spec §5：失败不静默降级，由人工选择）"
          >
            🔧 用固定引擎重试
          </button>
        )}
      </div>
    </div>
  );
}

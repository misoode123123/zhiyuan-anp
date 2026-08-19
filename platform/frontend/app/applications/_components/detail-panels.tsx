"use client";

// 五栏 Detail（需求/研发/部署/运行/依赖）+ 登记变更面板 + 版本历史 + logs/reqs/env
// 三个展开面板（自 page.tsx 等价平移：渲染结构/className/文案零改动，仅 a.id→app.id、
// 动作函数→props 回调、state setter→props 三类机械替换）。T6 变化②：「部署」栏的
// deploy_history 时间线折叠块不迁（已按 env 过滤进环境卡）。
// 受控组件：detail 为 null 且无展开面板时返回 null（T8 tab 面板接管 detail 加载/重试态）。
import type { Dispatch, SetStateAction } from "react";
import { ChangeOutput } from "@/app/_components/change-output";
import { ExpandPanels } from "./expand-panels";
import type { App, AppStats, Detail, EnvForm, EnvVar, Req } from "../_lib/types";
import type { ExpandedKey } from "../_lib/use-app-actions";

export function DetailPanels({
  app,
  detail,
  appStats,
  regFor,
  setRegFor,
  regReq,
  setRegReq,
  regNote,
  setRegNote,
  regBusy,
  envList,
  envForm,
  setEnvForm,
  logs,
  appReqs,
  openPanel,
  openFor,
  onApprove,
  onReject,
  onRelease,
  onMerge,
  onRegisterChange,
  onDeployCommit,
  onSaveEnv,
  onRemoveEnv,
}: {
  app: App;
  detail: Detail | null;
  appStats?: AppStats;
  regFor: string;
  setRegFor: Dispatch<SetStateAction<string>>;
  regReq: string;
  setRegReq: Dispatch<SetStateAction<string>>;
  regNote: string;
  setRegNote: Dispatch<SetStateAction<string>>;
  regBusy: boolean;
  envList: EnvVar[];
  envForm: EnvForm;
  setEnvForm: Dispatch<SetStateAction<EnvForm>>;
  logs: string;
  appReqs: Req[];
  openPanel: ExpandedKey | "";
  openFor: string;
  onApprove: (appID: string, chgID: string) => Promise<void>;
  onReject: (appID: string, chgID: string) => Promise<void>;
  onRelease: (appID: string, chgID: string) => Promise<void>;
  onMerge: (appID: string, chgID: string, reqID?: string) => Promise<void>;
  onRegisterChange: (appID: string, reqID: string, note: string) => Promise<void>;
  onDeployCommit: (appID: string, sha: string) => Promise<void>;
  onSaveEnv: (id: string) => Promise<void>;
  onRemoveEnv: (id: string, key: string) => Promise<void>;
}) {
  if (!detail && openFor !== app.id) return null;
  return (
    <>
      <ExpandPanels
        app={app}
        envList={envList}
        envForm={envForm}
        setEnvForm={setEnvForm}
        logs={logs}
        appReqs={appReqs}
        openPanel={openPanel}
        openFor={openFor}
        onSaveEnv={onSaveEnv}
        onRemoveEnv={onRemoveEnv}
      />
      {detail && (
        <>
          <div className="mt-2 grid grid-cols-1 gap-2 rounded bg-bg p-2 text-xs md:grid-cols-5">
            {/* 需求 */}
            <div>
              <div className="mb-1 font-medium text-text-muted">
                需求（{detail.requirements.length}）
              </div>
              {detail.requirements.map((q) => (
                <div key={q.id} className="truncate">
                  <span className={q.status === "delivered" ? "text-success" : "text-text-muted"}>
                    ●
                  </span>{" "}
                  {q.title}
                </div>
              ))}
              {detail.requirements.length === 0 && <div className="text-text-muted">无</div>}
            </div>

            {/* 研发：编码会话 + 异步任务 + 变更（含闭环按钮）+ git */}
            <div>
              <div className="mb-1 font-medium text-text-muted">研发</div>
              <div className="text-text-muted">编码会话（{detail.sessions.length}）</div>
              {detail.sessions.map((s) => (
                <div key={s.id} className="truncate">
                  <span className="text-accent">●</span> {s.tool} · {s.prompt_count}轮
                </div>
              ))}
              <div className="mt-1 text-text-muted">异步任务（{detail.tasks.length}）</div>
              {detail.tasks.map((tk) => (
                <div key={tk.id} className="truncate">
                  <span
                    className={
                      tk.status === "completed"
                        ? "text-success"
                        : tk.status === "failed"
                          ? "text-danger"
                          : "text-warn"
                    }
                  >
                    ●
                  </span>{" "}
                  {tk.kind} · {tk.status}
                </div>
              ))}
              <div className="mt-1 text-text-muted">变更（{detail.changes.length}）</div>
              {detail.changes.map((c) => (
                <div key={c.id} className="mb-1.5 rounded border border-border p-1.5">
                  <div>
                    <span
                      className={
                        c.status === "approved"
                          ? "text-success"
                          : c.status === "released"
                            ? "text-accent"
                            : "text-warn"
                      }
                    >
                      ●
                    </span>{" "}
                    {c.kind} · {c.status}
                  </div>
                  {c.output && <ChangeOutput output={c.output} />}
                  <div className="mt-1 flex flex-wrap gap-1">
                    {c.status === "pending" && (
                      <>
                        <button
                          onClick={() => onApprove(app.id, c.id)}
                          className="rounded bg-success/10 px-1.5 py-0.5 text-success hover:bg-success/20"
                        >
                          审批通过
                        </button>
                        <button
                          onClick={() => onReject(app.id, c.id)}
                          className="rounded bg-warn/10 px-1.5 py-0.5 text-warn hover:bg-warn/20"
                        >
                          拒绝
                        </button>
                      </>
                    )}
                    {c.status === "approved" && (
                      <>
                        <button
                          onClick={() => onRelease(app.id, c.id)}
                          className="rounded bg-accent/10 px-1.5 py-0.5 text-accent hover:bg-accent/20"
                          title="建发布版本 + 标关联需求 delivered + 触发部署"
                        >
                          发布上线
                        </button>
                        <button
                          onClick={() => onMerge(app.id, c.id, c.source_id)}
                          className="rounded bg-success/10 px-1.5 py-0.5 text-success hover:bg-success/20"
                          title="合并 dev→main + 标 delivered + 释放认领"
                        >
                          合并main
                        </button>
                      </>
                    )}
                  </div>
                </div>
              ))}
              {detail.changes.length === 0 && <div className="text-text-muted">无</div>}
              <div className="mt-1 text-text-muted">git（{detail.commits.length}）</div>
              {detail.commits.slice(0, 3).map((c) => (
                <div key={c.sha} className="truncate">
                  <span className="text-text-muted">●</span> {c.message}
                </div>
              ))}
            </div>

            {/* 部署：实例 test/prod + URL + 发布版本 + 部署需求 needs★
                （deploy_history 时间线已按 env 过滤进环境卡，T6） */}
            <div>
              <div className="mb-1 font-medium text-text-muted">部署</div>
              {(detail.instances ?? []).map((ins) => (
                <div key={ins.env} className="truncate">
                  <span className={ins.status === "running" ? "text-success" : "text-text-muted"}>
                    ●
                  </span>{" "}
                  {ins.env} · v{ins.version}
                  {ins.url && (
                    <a href={ins.url} target="_blank" rel="noreferrer" className="ml-1 text-accent">
                      ↗
                    </a>
                  )}
                </div>
              ))}
              {(detail.instances ?? []).length === 0 && (
                <div className="text-text-muted">无实例</div>
              )}
              <div className="mt-1 text-text-muted">发布（{detail.releases.length}）</div>
              {detail.releases.slice(0, 3).map((r) => (
                <div key={r.id}>
                  <span className="text-accent">●</span> {r.version} · {r.status}
                </div>
              ))}
              {detail.deploy_needs &&
                (detail.deploy_needs.ports.length ||
                  detail.deploy_needs.mounts.length ||
                  detail.deploy_needs.env_keys.length ||
                  detail.deploy_needs.command) && (
                  <div className="mt-1 rounded border border-warn/30 bg-warn/5 p-1">
                    <div className="font-medium text-warn">★ 部署需求 needs</div>
                    {detail.deploy_needs.ports.length > 0 && (
                      <div>ports: {detail.deploy_needs.ports.join(",")}</div>
                    )}
                    {detail.deploy_needs.command && (
                      <div className="truncate">cmd: {detail.deploy_needs.command}</div>
                    )}
                    {detail.deploy_needs.mounts.length > 0 && (
                      <div>mounts: {detail.deploy_needs.mounts.length}条</div>
                    )}
                    {detail.deploy_needs.env_keys.length > 0 && (
                      <div>env_keys: {detail.deploy_needs.env_keys.join(",")}</div>
                    )}
                  </div>
                )}
            </div>

            {/* 运行：资源当前 + 健康徽标（复用已 30s 轮询的 appStats；健康词表为 up/down） */}
            <div>
              <div className="mb-1 font-medium text-text-muted">运行</div>
              {(() => {
                const st = appStats;
                if (!st) return <div className="text-text-muted">无数据</div>;
                return (
                  <>
                    <div>
                      <span
                        className={
                          st.health === "up"
                            ? "text-success"
                            : st.health
                              ? "text-danger"
                              : "text-text-muted"
                        }
                      >
                        ●
                      </span>{" "}
                      {st.health || "未采集"}
                    </div>
                    {st.cpu && <div className="text-text-muted">CPU {st.cpu}</div>}
                    {st.mem && <div className="text-text-muted">内存 {st.mem}</div>}
                    {st.url && (
                      <a href={st.url} target="_blank" rel="noreferrer" className="text-accent">
                        访问 ↗
                      </a>
                    )}
                  </>
                );
              })()}
            </div>

            {/* 依赖：中间件绑定（mwReconciler.ListDeps 经 handler 填） */}
            <div>
              <div className="mb-1 font-medium text-text-muted">依赖（{detail.deps.length}）</div>
              {detail.deps.map((d, i) => (
                <div key={i} className="truncate">
                  <span
                    className={
                      d.status === "bound"
                        ? "text-success"
                        : d.status === "failed"
                          ? "text-danger"
                          : "text-warn"
                    }
                  >
                    ●
                  </span>{" "}
                  {d.kind} · {d.strategy}
                </div>
              ))}
              {detail.deps.length === 0 && <div className="text-text-muted">无</div>}
            </div>
          </div>
          <div className="mt-2">
            <button
              onClick={() => {
                setRegFor(regFor === app.id ? "" : app.id);
                setRegReq("");
                setRegNote("");
              }}
              className="rounded bg-bg px-2 py-1 text-xs text-text hover:bg-surface-2"
            >
              📝 登记变更（自由编码产出 / 关联需求）
            </button>
            {regFor === app.id && (
              <div className="mt-1 space-y-1 rounded border border-border p-2 text-xs">
                <div className="flex items-center gap-1">
                  <span>关联需求：</span>
                  <select
                    value={regReq}
                    onChange={(e) => setRegReq(e.target.value)}
                    className="flex-1 rounded border border-border bg-bg px-1 py-0.5"
                  >
                    <option value="">（无，纯自由变更）</option>
                    {detail.requirements.map((q) => (
                      <option key={q.id} value={q.id}>
                        {q.title}
                      </option>
                    ))}
                  </select>
                </div>
                <input
                  value={regNote}
                  onChange={(e) => setRegNote(e.target.value)}
                  placeholder="补充说明（可选）"
                  className="w-full rounded border border-border bg-bg px-1.5 py-0.5"
                />
                <div className="flex gap-1">
                  <button
                    onClick={() => onRegisterChange(app.id, regReq, regNote)}
                    disabled={regBusy}
                    className="rounded bg-accent px-2 py-0.5 text-white disabled:opacity-50"
                  >
                    {regBusy ? "登记中（AI 总结）…" : "提交登记"}
                  </button>
                  <button
                    onClick={() => setRegFor("")}
                    className="rounded border border-border px-2 py-0.5"
                  >
                    取消
                  </button>
                </div>
              </div>
            )}
          </div>
          {detail.commits.length > 0 && (
            <div className="mt-2 border-t border-border pt-2">
              <div className="mb-1 font-medium text-text-muted">
                版本历史（{detail.commits.length}，可部署/回滚任意版本）
              </div>
              <div className="space-y-1">
                {detail.commits.map((c) => (
                  <div key={c.sha} className="flex items-center gap-2">
                    <code className="text-xs text-text-muted">{c.sha.slice(0, 7)}</code>
                    <span className="truncate text-text">{c.message}</span>
                    <button
                      onClick={() => onDeployCommit(app.id, c.sha)}
                      className="ml-auto rounded bg-warn/10 px-2 py-0.5 text-xs text-warn"
                    >
                      部署此版本
                    </button>
                  </div>
                ))}
              </div>
            </div>
          )}
        </>
      )}
    </>
  );
}

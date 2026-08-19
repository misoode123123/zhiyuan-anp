"use client";

// logs/reqs/env 三个展开面板（自 page.tsx 等价平移，T7 自 detail-panels 拆出以守 ≤400 行
// 预算；渲染结构/className/文案零改动，仅 a.id→app.id、动作→props 回调、setter→props）。
// 受控组件：三面板均由 openPanel+openFor 归属判断（与原 per-app 判断语义一致），
// 无命中面板时返回 null。
import type { Dispatch, SetStateAction } from "react";
import type { App, EnvForm, EnvVar, Req } from "../_lib/types";
import type { ExpandedKey } from "../_lib/use-app-actions";

export function ExpandPanels({
  app,
  envList,
  envForm,
  setEnvForm,
  logs,
  appReqs,
  openPanel,
  openFor,
  onSaveEnv,
  onRemoveEnv,
}: {
  app: App;
  envList: EnvVar[];
  envForm: EnvForm;
  setEnvForm: Dispatch<SetStateAction<EnvForm>>;
  logs: string;
  appReqs: Req[];
  openPanel: ExpandedKey | "";
  openFor: string;
  onSaveEnv: (id: string) => Promise<void>;
  onRemoveEnv: (id: string, key: string) => Promise<void>;
}) {
  if (openFor !== app.id) return null;
  return (
    <>
      {openPanel === "logs" && (
        <pre className="mt-2 max-h-48 overflow-auto rounded bg-neutral-900 p-2 text-xs text-green-300">
          {logs}
        </pre>
      )}
      {openPanel === "reqs" && (
        <div className="mt-2 rounded bg-bg p-2 text-xs">
          <div className="mb-1 text-text-muted">归属此应用的需求（{appReqs.length}）</div>
          {appReqs.map((q) => (
            <div key={q.id} className="flex items-center gap-2 py-0.5">
              <span
                className={`rounded px-1.5 py-0.5 ${q.status === "delivered" ? "bg-success/10 text-success" : "bg-surface-2 text-text-muted"}`}
              >
                {q.status}
              </span>
              <span className="truncate">{q.title}</span>
            </div>
          ))}
          {appReqs.length === 0 && (
            <div className="text-text-muted">暂无（发布此应用的需求后会自动归属到此）</div>
          )}
        </div>
      )}
      {openPanel === "env" && (
        <div className="mt-2 rounded bg-bg p-2 text-xs">
          <div className="mb-1 text-text-muted">
            运行时环境变量（部署时 -e 注入容器；🔒=密钥已隐藏明文；平台托管=由部署供给，不可改）
          </div>
          <div className="space-y-1">
            {envList.map((e) => (
              <div key={e.id} className="flex items-center gap-2">
                <code className="text-text">{e.key}</code>
                <span className="text-text-muted">=</span>
                <span className={e.is_secret ? "text-warn" : "text-text-muted"}>
                  {e.is_secret ? "🔒 已隐藏" : e.value || "(空)"}
                </span>
                {e.source === "platform" ? (
                  <span className="ml-auto rounded bg-accent/15 px-1.5 py-0.5 text-accent">
                    平台托管
                  </span>
                ) : (
                  <button
                    onClick={() => onRemoveEnv(app.id, e.key)}
                    className="ml-auto rounded bg-danger/10 px-1.5 py-0.5 text-danger"
                  >
                    删
                  </button>
                )}
              </div>
            ))}
            {envList.length === 0 && <div className="text-text-muted">暂无</div>}
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-1 border-t border-border pt-2">
            <input
              value={envForm.key}
              onChange={(ev) => setEnvForm({ ...envForm, key: ev.target.value })}
              placeholder="KEY"
              className="w-28 rounded border border-border px-1 py-0.5"
            />
            <input
              value={envForm.value}
              onChange={(ev) => setEnvForm({ ...envForm, value: ev.target.value })}
              placeholder="value"
              type={envForm.is_secret ? "password" : "text"}
              className="flex-1 rounded border border-border px-1 py-0.5"
            />
            <label className="flex items-center gap-1">
              <input
                type="checkbox"
                checked={envForm.is_secret}
                onChange={(ev) => setEnvForm({ ...envForm, is_secret: ev.target.checked })}
              />
              密钥
            </label>
            <button
              onClick={() => onSaveEnv(app.id)}
              className="rounded bg-accent px-2 py-0.5 text-white"
            >
              保存
            </button>
          </div>
        </div>
      )}
    </>
  );
}

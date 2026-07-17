"use client";

import { useState, type ReactNode } from "react";
import { ProjectDocs } from "./project-docs";

// 项目上下文抽屉:展示该应用的需求/变更/发布(数据来自 /detail)。
// 让开发者在编码时一眼看到"这个项目要做什么、改了什么、发布了什么"——上下文不再缺失。
// 纯展示组件,数据与状态全由 WorkspaceFrame 注入。

type Req = { id: string; title: string; status: string; priority?: string; fixed_version?: string; description?: string; user_story?: string; acceptance_criteria?: string };
type Chg = { id: string; kind: string; status: string; source_id: string; created_at: string; output?: string };
type Rel = { id: string; version: string; status: string; created_at: string };
export type WorkspaceDetail = { requirements?: Req[]; changes?: Chg[]; releases?: Rel[] };

// 状态→圆点映射(需求/变更/发布共用,缺失则普通圆点)
const STATUS_DOT: Record<string, string> = {
  delivered: "✅", approved: "✅", pending: "⏳", draft: "📝", rejected: "❌", reviewed: "👍",
  specified: "📋", developing: "🔨",
};

export function ContextDrawer({
  detail,
  loading,
  err,
  onClose,
  onApprove,
  onReject,
  psID,
  appID,
  selectedReq,
  onStartReq,
}: {
  detail: WorkspaceDetail | null;
  loading: boolean;
  err: string;
  onClose: () => void;
  onApprove: (id: string) => void;
  onReject: (id: string) => void;
  psID: string;
  appID: string;
  selectedReq: string;
  onStartReq: (id: string) => void;
}) {
  const [openReq, setOpenReq] = useState<string | null>(null);
  const [openChg, setOpenChg] = useState<string | null>(null);
  return (
    <aside className="w-64 shrink-0 overflow-y-auto border-r border-neutral-200 bg-neutral-50 p-2 text-xs">
      <div className="mb-2 flex items-center justify-between">
        <span className="font-medium text-neutral-600">📋 项目上下文</span>
        <button onClick={onClose} className="text-neutral-400 hover:text-neutral-700" title="折叠">◀</button>
      </div>
      {err && <div className="text-red-600">{err}</div>}
      {!err && loading && <div className="text-neutral-400">加载中…</div>}
      <Section title="📁 文件结构">
        <ProjectDocs psID={psID} appID={appID} />
      </Section>
      {detail && !err && (
        <>
          <Section title={`需求(${detail.requirements?.length ?? 0})`}>
            {detail.requirements?.length ? detail.requirements!.map((q) => (
              <div key={q.id}>
                <button
                  onClick={() => setOpenReq(openReq === q.id ? null : q.id)}
                  className="flex min-w-0 flex-1 gap-1 py-0.5 text-left"
                >
                  <span>{STATUS_DOT[q.status] ?? "•"}</span>
                  {q.priority && (
                    <span className={`shrink-0 rounded px-1 text-[10px] ${q.priority === "P0" ? "bg-red-100 text-red-700" : q.priority === "P2" ? "bg-neutral-200 text-neutral-600" : "bg-blue-100 text-blue-700"}`} title="需求等级">{q.priority}</span>
                  )}
                  <span className={`truncate ${selectedReq === q.id ? "font-semibold text-blue-700" : "text-neutral-700"}`}>{q.title || "(无标题)"}</span>
                </button>
                {selectedReq !== q.id && (
                  <button
                    onClick={() => onStartReq(q.id)}
                    className="shrink-0 rounded bg-blue-100 px-1.5 text-[10px] text-blue-700"
                    title="以此需求驱动开发(AI 按需求编码)"
                  >
                    开发
                  </button>
                )}
                {openReq === q.id && (
                  <div className="mb-1 ml-3 space-y-0.5 border-l border-neutral-300 pl-2 text-[11px] text-neutral-600">
                    {q.fixed_version && <div className="text-neutral-500">📦 计划版本:{q.fixed_version}</div>}
                    {q.description && <div>{q.description}</div>}
                    {q.user_story && <div>📝 {q.user_story}</div>}
                    {q.acceptance_criteria && <div>✅ {q.acceptance_criteria}</div>}
                    {!q.description && !q.user_story && !q.acceptance_criteria && (
                      <div className="text-neutral-400">(无详情)</div>
                    )}
                  </div>
                )}
              </div>
            )) : <Empty />}
          </Section>
          <Section title={`变更(${detail.changes?.length ?? 0}) · 审批后可上线`}>
            {detail.changes?.length ? detail.changes!.map((c) => (
              <div key={c.id}>
                <div className="flex py-0.5">
                  <button onClick={() => setOpenChg(openChg === c.id ? null : c.id)} className="flex gap-1 text-left">
                    <span>{STATUS_DOT[c.status] ?? "•"}</span>
                    <span>{c.kind} · <span className="text-neutral-500">{c.status}</span></span>
                  </button>
                  {c.status === "pending" && (
                    <span className="ml-auto shrink-0">
                      <button onClick={() => onApprove(c.id)} className="text-emerald-600" title="批准上线">✅</button>
                      <button onClick={() => onReject(c.id)} className="ml-1 text-red-600" title="拒绝">❌</button>
                    </span>
                  )}
                </div>
                {openChg === c.id && (
                  <pre className="mb-1 ml-3 max-h-32 overflow-auto whitespace-pre-wrap border-l border-neutral-300 pl-2 text-[11px] text-neutral-600">{c.output || "(无内容)"}</pre>
                )}
              </div>
            )) : <Empty />}
          </Section>
          <Section title={`发布(${detail.releases?.length ?? 0})`}>
            {detail.releases?.length ? detail.releases!.map((r) => (
              <div key={r.id} className="py-0.5">
                <span>{STATUS_DOT[r.status] ?? "•"}</span> v{r.version} · <span className="text-neutral-500">{r.status}</span>
              </div>
            )) : <Empty />}
          </Section>
        </>
      )}
    </aside>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="mb-2">
      <div className="mb-1 font-medium text-neutral-500">{title}</div>
      <div>{children}</div>
    </div>
  );
}
function Empty() {
  return <div className="text-neutral-400">暂无</div>;
}

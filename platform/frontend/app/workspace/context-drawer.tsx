"use client";

import type { ReactNode } from "react";

// 项目上下文抽屉:展示该应用的需求/变更/发布(数据来自 /detail)。
// 让开发者在编码时一眼看到"这个项目要做什么、改了什么、发布了什么"——上下文不再缺失。
// 纯展示组件,数据与状态全由 WorkspaceFrame 注入。

type Req = { id: string; title: string; status: string };
type Chg = { id: string; kind: string; status: string; source_id: string; created_at: string };
type Rel = { id: string; version: string; status: string; created_at: string };
export type WorkspaceDetail = { requirements?: Req[]; changes?: Chg[]; releases?: Rel[] };

// 状态→圆点映射(需求/变更/发布共用,缺失则普通圆点)
const STATUS_DOT: Record<string, string> = {
  delivered: "✅", approved: "✅", pending: "⏳", draft: "📝", rejected: "❌", reviewed: "👍",
};

export function ContextDrawer({
  detail,
  loading,
  err,
  onClose,
  onApprove,
  onReject,
}: {
  detail: WorkspaceDetail | null;
  loading: boolean;
  err: string;
  onClose: () => void;
  onApprove: (id: string) => void;
  onReject: (id: string) => void;
}) {
  return (
    <aside className="w-64 shrink-0 overflow-y-auto border-r border-neutral-200 bg-neutral-50 p-2 text-xs">
      <div className="mb-2 flex items-center justify-between">
        <span className="font-medium text-neutral-600">📋 项目上下文</span>
        <button onClick={onClose} className="text-neutral-400 hover:text-neutral-700" title="折叠">◀</button>
      </div>
      {err && <div className="text-red-600">{err}</div>}
      {!err && loading && <div className="text-neutral-400">加载中…</div>}
      {detail && !err && (
        <>
          <Section title={`需求(${detail.requirements?.length ?? 0})`}>
            {detail.requirements?.length ? detail.requirements!.map((q) => (
              <div key={q.id} className="flex gap-1 py-0.5">
                <span>{STATUS_DOT[q.status] ?? "•"}</span>
                <span className="truncate text-neutral-700" title={q.title}>{q.title}</span>
              </div>
            )) : <Empty />}
          </Section>
          <Section title={`变更(${detail.changes?.length ?? 0})`}>
            {detail.changes?.length ? detail.changes!.map((c) => (
              <div key={c.id} className="py-0.5">
                <span>{STATUS_DOT[c.status] ?? "•"}</span> {c.kind} · <span className="text-neutral-500">{c.status}</span>
                {c.status === "pending" && (
                  <span className="ml-1">
                    <button onClick={() => onApprove(c.id)} className="text-emerald-600" title="批准">✅</button>
                    <button onClick={() => onReject(c.id)} className="ml-1 text-red-600" title="拒绝">❌</button>
                  </span>
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

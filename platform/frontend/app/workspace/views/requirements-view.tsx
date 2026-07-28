"use client";

import { useState } from "react";
import type { WorkspaceDetail, Req, ReqState, ReqActions } from "../types";
import { statusColor, statusLabel } from "@/lib/workspace";

// 需求视图：IDE 风格需求列表（状态色条 + 优先级标签 + 标题），展开详情 + 操作按钮。
export function RequirementsView({
  detail,
  selectedReq,
  onStartReq,
  reqState,
  reqActions,
}: {
  detail: WorkspaceDetail | null;
  selectedReq: string;
  onStartReq: (id: string) => void;
  reqState: ReqState;
  reqActions: ReqActions;
}) {
  const [openId, setOpenId] = useState<string | null>(selectedReq || null);
  const reqs = detail?.requirements ?? [];

  const priClass = (p?: string) =>
    !p || p === "P2"
      ? "bg-[#eaeaea] text-[#636c76]"
      : p === "P0"
        ? "bg-[#ffe0e0] text-[#cf222e]"
        : "bg-[#ddf4ff] text-[#0969da]";

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center justify-between border-b border-[#e6e6e6] px-3 py-2 text-[11px] uppercase tracking-wide text-[#636363]">
        <span>需求</span>
        <span className="text-[#9a9a9a]">{reqs.length} 项 · 按等级</span>
      </div>
      <div className="flex-1 overflow-auto py-0.5">
        {reqs.length === 0 ? (
          <div className="p-3 text-[#9a9a9a]">暂无需求</div>
        ) : (
          reqs.map((q) => {
            const sel = selectedReq === q.id;
            const open = openId === q.id;
            return (
              <div key={q.id}>
                <div
                  className={`flex items-center gap-2 px-3 py-1.5 leading-relaxed ${
                    sel ? "bg-[#d6ebff]" : "hover:bg-[#e8e8e8]"
                  }`}
                >
                  <span
                    className="w-[3px] shrink-0 rounded"
                    style={{ height: 15, background: statusColor(q.status) }}
                  />
                  {q.priority && (
                    <span
                      className={`shrink-0 rounded px-1 text-[10px] font-semibold ${priClass(q.priority)}`}
                    >
                      {q.priority}
                    </span>
                  )}
                  <button
                    onClick={() => setOpenId(open ? null : q.id)}
                    className="flex min-w-0 flex-1 text-left"
                  >
                    <span
                      className={`truncate ${sel ? "font-semibold text-[#007acc]" : "text-[#3c3c3c]"}`}
                    >
                      {q.title || "(无标题)"}
                    </span>
                  </button>
                  {sel ? null : (
                    <button
                      onClick={() => onStartReq(q.id)}
                      className="shrink-0 rounded border border-[#d0d0d0] bg-white px-1.5 text-[10px] text-[#57606a] hover:bg-[#f0f0f0]"
                      title={q.assignee ? "继续开发" : "认领并开发"}
                    >
                      {q.assignee ? "继续" : "认领"}
                    </button>
                  )}
                </div>
                {open && <ReqDetail q={q} reqState={reqState} reqActions={reqActions} />}
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}

function ReqDetail({
  q,
  reqState,
  reqActions,
}: {
  q: Req;
  reqState: ReqState;
  reqActions: ReqActions;
}) {
  let ac: string[] = [];
  try {
    ac = JSON.parse(q.acceptance_criteria || "[]");
  } catch {
    ac = q.acceptance_criteria ? [q.acceptance_criteria] : [];
  }
  return (
    <div className="ml-3 border-l-2 border-[#007acc] bg-white px-0 py-2">
      <div className="px-3">
        <span className="text-[11px] text-[#8b949e]">状态</span>{" "}
        <span className="text-[11px] text-[#57606a]">{statusLabel(q.status)}</span>
      </div>
      {q.user_story && (
        <div className="px-3 pt-1 text-[11px] text-[#57606a]">📝 {q.user_story}</div>
      )}
      {ac.length > 0 && (
        <div className="px-3 pt-1 text-[11px] text-[#57606a]">
          <div className="text-[#8b949e]">✅ 验收标准</div>
          {ac.map((c, i) => (
            <div key={i}>· {c}</div>
          ))}
        </div>
      )}
      <div className="flex flex-wrap gap-1.5 px-3 pt-2">
        <button
          onClick={() => reqActions.dispatch()}
          disabled={reqState.dispatching}
          className="rounded bg-[#2da44e] px-2 py-0.5 text-[11px] text-white disabled:opacity-50"
        >
          {reqState.dispatching ? "编码中…" : "🤖 AI 编码"}
        </button>
        <button
          onClick={reqActions.runAutoTest}
          disabled={reqState.testing}
          className="rounded border border-[#d0d0d0] bg-white px-2 py-0.5 text-[11px] text-[#1a7f37] disabled:opacity-50"
        >
          {reqState.testing ? "测试中…" : "🧪 自动测试"}
        </button>
        <button
          onClick={reqActions.breakdown}
          disabled={reqState.breaking}
          className="rounded border border-[#d0d0d0] bg-white px-2 py-0.5 text-[11px] text-[#6f42c1] disabled:opacity-50"
        >
          {reqState.breaking ? "拆解中…" : "📋 拆子任务"}
        </button>
        <button
          onClick={reqActions.submit}
          disabled={reqState.submitting}
          className="rounded bg-[#bf8700] px-2 py-0.5 text-[11px] text-white disabled:opacity-50"
        >
          {reqState.submitting ? "核对中…" : "🔒 提交核对"}
        </button>
        <button
          onClick={reqActions.merge}
          disabled={reqState.merging}
          className="rounded bg-[#1a7f37] px-2 py-0.5 text-[11px] text-white disabled:opacity-50"
        >
          {reqState.merging ? "合并中…" : "🔀 合并主线"}
        </button>
      </div>
      {reqState.taskMsg && (
        <div className="whitespace-pre-wrap px-3 pt-1 text-[11px] text-[#0969da]">
          {reqState.taskMsg}
        </div>
      )}
      {reqState.submitMsg && (
        <div className="whitespace-pre-wrap px-3 pt-1 text-[11px] text-[#bf8700]">
          {reqState.submitMsg}
        </div>
      )}
      {reqState.testMsg && (
        <div className="px-3 pt-1 text-[11px] text-[#1a7f37]">{reqState.testMsg}</div>
      )}
      {reqState.testResults && reqState.testResults.length > 0 && (
        <div className="px-3 pt-1 text-[11px]">
          {reqState.testResults.map((tc, i) => (
            <div
              key={i}
              className={
                tc.actual_status === tc.expected_status ? "text-[#1a7f37]" : "text-[#cf222e]"
              }
            >
              {tc.actual_status === tc.expected_status ? "✅" : "❌"} {tc.method} {tc.path} →{" "}
              {tc.actual_status || "(未跑)"}
            </div>
          ))}
        </div>
      )}
      {reqState.subtasks.length > 0 && (
        <div className="px-3 pt-1 text-[11px]">
          {reqState.subtasks.map((t, i) => (
            <div key={i} className="flex items-center gap-1">
              <input
                type="checkbox"
                checked={t.done}
                onChange={() => reqActions.toggleSubtask(i)}
              />
              <span
                className={`flex-1 ${t.done ? "text-[#9a9a9a] line-through" : "text-[#57606a]"}`}
              >
                {t.text}
              </span>
              {!t.done && (
                <button
                  onClick={() => reqActions.dispatch(i)}
                  className="text-[#0969da]"
                  title="让 AI 做这一步"
                >
                  ▶
                </button>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

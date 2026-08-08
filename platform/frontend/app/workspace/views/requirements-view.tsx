"use client";

import { useState } from "react";
import type { WorkspaceDetail, Req, ReqState, ReqActions } from "../types";
import { statusColor, statusLabel } from "@/lib/workspace";
import { currentUser } from "@/lib/api";

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
  // 当前登录用户名（与 requirement.assignee 同为 username），用于区分"我认领的" vs "别人认领的"。
  const me = currentUser();

  const priClass = (p?: string) =>
    !p || p === "P2"
      ? "bg-surface-2 text-text-muted"
      : p === "P0"
        ? "bg-danger/10 text-danger"
        : "bg-accent/10 text-accent";

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center justify-between border-b border-border px-3 py-2 text-[11px] uppercase tracking-wide text-text-muted">
        <span>需求</span>
        <span className="text-text-muted">{reqs.length} 项 · 按等级</span>
      </div>
      <div className="flex-1 overflow-auto py-0.5">
        {reqs.length === 0 ? (
          <div className="p-3 text-text-muted">暂无需求</div>
        ) : (
          reqs.map((q) => {
            const sel = selectedReq === q.id;
            const open = openId === q.id;
            const mine = !!q.assignee && q.assignee === me;
            return (
              <div key={q.id}>
                <div
                  className={`flex items-center gap-2 px-3 py-1.5 leading-relaxed ${
                    sel ? "bg-accent/10" : "hover:bg-surface-2"
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
                    <span className={`truncate ${sel ? "font-semibold text-accent" : "text-text"}`}>
                      {q.title || "(无标题)"}
                    </span>
                  </button>
                  {sel ? null : q.assignee && !mine ? (
                    <span
                      className="shrink-0 cursor-not-allowed rounded border border-border bg-surface px-1.5 text-[10px] text-text-muted opacity-50"
                      title={`已被 ${q.assignee} 认领`}
                    >
                      已被认领
                    </span>
                  ) : (
                    <button
                      onClick={() => onStartReq(q.id)}
                      className="shrink-0 rounded border border-border bg-surface px-1.5 text-[10px] text-text-muted hover:bg-surface-2"
                      title={mine ? "继续开发" : "认领并启动编码工作台"}
                    >
                      {mine ? "↩ 继续" : "🚀 启动"}
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
    <div className="ml-3 border-l-2 border-accent bg-surface px-0 py-2">
      <div className="px-3">
        <span className="text-[11px] text-text-muted">状态</span>{" "}
        <span className="text-[11px] text-text">{statusLabel(q.status)}</span>
      </div>
      {q.user_story && <div className="px-3 pt-1 text-[11px] text-text">📝 {q.user_story}</div>}
      {ac.length > 0 && (
        <div className="px-3 pt-1 text-[11px] text-text">
          <div className="text-text-muted">✅ 验收标准</div>
          {ac.map((c, i) => (
            <div key={i}>· {c}</div>
          ))}
        </div>
      )}
      <div className="flex flex-wrap gap-1.5 px-3 pt-2">
        <button
          onClick={() => reqActions.dispatch()}
          disabled={reqState.dispatching}
          className="rounded bg-success px-2 py-0.5 text-[11px] text-accent-fg disabled:opacity-50"
        >
          {reqState.dispatching ? "编码中…" : "🤖 AI 编码"}
        </button>
        <button
          onClick={reqActions.runAutoTest}
          disabled={reqState.testing}
          className="rounded border border-border bg-surface px-2 py-0.5 text-[11px] text-success disabled:opacity-50"
        >
          {reqState.testing ? "测试中…" : "🧪 自动测试"}
        </button>
        <button
          onClick={reqActions.breakdown}
          disabled={reqState.breaking}
          className="rounded border border-border bg-surface px-2 py-0.5 text-[11px] text-accent disabled:opacity-50"
        >
          {reqState.breaking ? "拆解中…" : "📋 拆子任务"}
        </button>
        <button
          onClick={reqActions.submit}
          disabled={reqState.submitting}
          className="rounded bg-warn px-2 py-0.5 text-[11px] text-accent-fg disabled:opacity-50"
        >
          {reqState.submitting ? "核对中…" : "🔒 提交核对"}
        </button>
        <button
          onClick={reqActions.merge}
          disabled={reqState.merging}
          className="rounded bg-success px-2 py-0.5 text-[11px] text-accent-fg disabled:opacity-50"
        >
          {reqState.merging ? "合并中…" : "🔀 合并主线"}
        </button>
      </div>
      {reqState.taskMsg && (
        <div className="whitespace-pre-wrap px-3 pt-1 text-[11px] text-accent">
          {reqState.taskMsg}
        </div>
      )}
      {reqState.submitMsg && (
        <div className="whitespace-pre-wrap px-3 pt-1 text-[11px] text-warn">
          {reqState.submitMsg}
        </div>
      )}
      {reqState.testMsg && (
        <div className="px-3 pt-1 text-[11px] text-success">{reqState.testMsg}</div>
      )}
      {reqState.testResults && reqState.testResults.length > 0 && (
        <div className="px-3 pt-1 text-[11px]">
          {reqState.testResults.map((tc, i) => (
            <div
              key={i}
              className={tc.actual_status === tc.expected_status ? "text-success" : "text-danger"}
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
              <span className={`flex-1 ${t.done ? "text-text-muted line-through" : "text-text"}`}>
                {t.text}
              </span>
              {!t.done && (
                <button
                  onClick={() => reqActions.dispatch(i)}
                  className="text-accent"
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

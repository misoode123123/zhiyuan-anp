"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL, currentProjectSpace, setCurrentProjectSpace } from "@/lib/api";

type Envelope<T> = { code: number; data: T };
type PS = { id: string; name: string; slug: string };

type Metrics = {
  req_claimed: number;
  req_completed: number;
  code_task_done: number;
  code_task_failed: number;
  change_submitted: number;
  change_approved: number;
  change_rejected: number;
  releases: number;
  conversations: number;
  ws_sessions: number;
  ws_prompts: number;
  ws_seconds: number;
};
type SessionSummary = {
  id: string;
  tool: string;
  repo_dir: string;
  started_at: string;
  ended_at?: string;
  prompt_count: number;
};
type Profile = {
  user_id: string;
  user_name: string;
  is_unassigned?: boolean;
  metrics: Metrics;
  sessions?: SessionSummary[];
};

type SessionMsgs =
  | { tool: string; transcript: string }
  | { tool: string; messages: { role: string; content: string; created_at?: string }[] };

function fmtSecs(s: number): string {
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.round(s / 60)}m`;
  return `${(s / 3600).toFixed(1)}h`;
}

function MetricCard({
  label,
  value,
  hint,
}: {
  label: string;
  value: number | string;
  hint?: string;
}) {
  return (
    <div className="rounded-lg border bg-white p-3 shadow-sm">
      <div className="text-xs text-gray-500">{label}</div>
      <div className="text-2xl font-bold text-gray-800">{value}</div>
      {hint && <div className="text-[10px] text-gray-400">{hint}</div>}
    </div>
  );
}

function ProfileView({ p, onOpenSession }: { p: Profile; onOpenSession: (id: string) => void }) {
  const m = p.metrics;
  return (
    <div className="space-y-4">
      {/* 编码工作台互动（置顶重点） */}
      <div className="rounded-lg border-2 border-emerald-200 bg-emerald-50 p-4">
        <div className="mb-2 text-sm font-bold text-emerald-800">
          🟢 编码工作台互动（开发者↔AI 工具）
        </div>
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
          <MetricCard label="会话数" value={m.ws_sessions} />
          <MetricCard label="发起 prompt" value={m.ws_prompts} />
          <MetricCard label="活跃时长" value={fmtSecs(m.ws_seconds)} />
          <MetricCard label="需求梳理" value={m.conversations} />
        </div>
        {p.sessions && p.sessions.length > 0 && (
          <div className="mt-3">
            <div className="mb-1 text-xs font-semibold text-emerald-700">
              会话记录（点击查看完整聊天）
            </div>
            <div className="space-y-1">
              {p.sessions.map((s) => (
                <button
                  key={s.id}
                  onClick={() => onOpenSession(s.id)}
                  className="block w-full rounded border border-emerald-200 bg-white px-2 py-1 text-left text-xs hover:bg-emerald-100"
                >
                  <span className="font-mono text-[10px] text-gray-400">{s.tool}</span>{" "}
                  <span className="text-gray-700">{s.repo_dir}</span>{" "}
                  <span className="text-gray-400">{new Date(s.started_at).toLocaleString()}</span>{" "}
                  <span className="text-emerald-700">{s.prompt_count} prompts</span>
                </button>
              ))}
            </div>
          </div>
        )}
      </div>

      {/* 产出指标 */}
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-4 lg:grid-cols-6">
        <MetricCard label="认领需求" value={m.req_claimed} />
        <MetricCard label="需求已完成" value={m.req_completed} />
        <MetricCard label="编码完成" value={m.code_task_done} />
        <MetricCard label="编码失败" value={m.code_task_failed} />
        <MetricCard label="变更提交" value={m.change_submitted} />
        <MetricCard label="变更通过/驳回" value={`${m.change_approved}/${m.change_rejected}`} />
        <MetricCard label="发布次数" value={m.releases} />
      </div>
    </div>
  );
}

export default function PerformancePage() {
  const [spaces, setSpaces] = useState<PS[]>([]);
  const [psID, setPsID] = useState("");
  const [me, setMe] = useState<Profile | null>(null);
  const [members, setMembers] = useState<Profile[] | null>(null);
  const [selected, setSelected] = useState<Profile | null>(null);
  const [chat, setChat] = useState<{ id: string; data: SessionMsgs } | null>(null);
  const [chatBusy, setChatBusy] = useState(false);
  const [chatErr, setChatErr] = useState("");

  // 加载项目空间列表
  useEffect(() => {
    fetch(`${API_BASE_URL}/project-spaces`)
      .then((r) => r.json())
      .then((r: Envelope<PS[]>) => {
        setSpaces(r.data || []);
        const cur = currentProjectSpace();
        if (cur) setPsID(cur);
        else if (r.data && r.data.length > 0) setPsID(r.data[0].id);
      })
      .catch(() => {});
  }, []);

  // psID 变化 → 拉 me + members（admin）
  useEffect(() => {
    if (!psID) return;
    setCurrentProjectSpace(psID);
    setMe(null);
    setMembers(null);
    setSelected(null);
    fetch(`${API_BASE_URL}/project-spaces/${psID}/performance/me`)
      .then((r) => r.json())
      .then((r: Envelope<Profile>) => setMe(r.data))
      .catch(() => {});
    fetch(`${API_BASE_URL}/project-spaces/${psID}/performance/members`).then((r) => {
      if (r.ok) return r.json().then((r: Envelope<Profile[]>) => setMembers(r.data || []));
      setMembers(null); // 403 → 非 admin，只看自己
      return null;
    });
  }, [psID]);

  function openSession(id: string) {
    setChatBusy(true);
    setChatErr("");
    setChat({ id, data: { tool: "", messages: [] } });
    fetch(`${API_BASE_URL}/project-spaces/${psID}/performance/sessions/${id}/messages`)
      .then((r) => r.json())
      .then((r: Envelope<SessionMsgs>) => setChat({ id, data: r.data }))
      .catch((e) => setChatErr(String(e)))
      .finally(() => setChatBusy(false));
  }

  return (
    <div className="mx-auto max-w-6xl space-y-4 p-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-bold">📊 绩效记录</h1>
        <select
          value={psID}
          onChange={(e) => setPsID(e.target.value)}
          className="rounded border px-2 py-1 text-sm"
        >
          {spaces.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </select>
      </div>

      {!psID && <div className="text-gray-500">请先选择项目空间。</div>}

      {/* admin：全员列表 */}
      {members && (
        <div className="rounded-lg border bg-white p-3">
          <div className="mb-2 text-sm font-semibold text-gray-700">
            全员绩效（管理员视图，点击查看明细）
          </div>
          <div className="flex flex-wrap gap-2">
            {members.map((p) => (
              <button
                key={p.user_id || "unassigned"}
                onClick={() => setSelected(p)}
                className={`rounded border px-2 py-1 text-xs ${
                  selected?.user_id === p.user_id
                    ? "border-blue-400 bg-blue-50"
                    : "bg-gray-50 hover:bg-gray-100"
                }`}
              >
                {p.is_unassigned ? "🪣 未归属" : p.user_name || p.user_id}{" "}
                <span className="text-gray-400">
                  · {p.metrics.code_task_done}编码/{p.metrics.ws_prompts}prompt
                </span>
              </button>
            ))}
          </div>
        </div>
      )}

      {/* 明细：admin 选中某人，否则显示自己 */}
      {(selected ?? me) && <ProfileView p={selected ?? me!} onOpenSession={openSession} />}
      {members && !selected && me && (
        <div className="text-xs text-gray-400">上方为本人的绩效；点击成员名查看他人明细。</div>
      )}

      {/* 聊天记录抽屉 */}
      {chat && (
        <div className="fixed inset-0 z-50 flex bg-black/30" onClick={() => setChat(null)}>
          <div
            className="ml-auto h-full w-full max-w-2xl overflow-auto bg-white p-4 shadow-xl"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="mb-3 flex items-center justify-between">
              <div className="text-sm font-bold">💬 互动聊天记录（{chat.data.tool || "…"}）</div>
              <button onClick={() => setChat(null)} className="text-gray-400 hover:text-gray-700">
                ✕
              </button>
            </div>
            {chatBusy && <div className="text-gray-500">加载中…</div>}
            {chatErr && <div className="text-red-600">{chatErr}</div>}
            {"transcript" in chat.data && chat.data.transcript && (
              <pre className="whitespace-pre-wrap text-xs text-gray-700">
                {chat.data.transcript}
              </pre>
            )}
            {"messages" in chat.data &&
              chat.data.messages.map((mm, i) => (
                <div
                  key={i}
                  className={`mb-2 rounded p-2 text-xs ${
                    mm.role === "user" ? "bg-blue-50" : "bg-gray-50"
                  }`}
                >
                  <div className="mb-1 font-semibold text-gray-600">
                    {mm.role === "user" ? "👤 开发者" : "🤖 工具"}
                  </div>
                  <div className="whitespace-pre-wrap text-gray-800">{mm.content}</div>
                </div>
              ))}
            {!chatBusy &&
              !chatErr &&
              (("transcript" in chat.data && !chat.data.transcript) ||
                ("messages" in chat.data && chat.data.messages.length === 0)) && (
                <div className="text-gray-500">
                  无聊天记录（会话可能仍在进行中，或该工具仅会话级记录）。
                </div>
              )}
          </div>
        </div>
      )}
    </div>
  );
}

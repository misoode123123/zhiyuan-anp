"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL, currentUser } from "@/lib/api";

type Envelope<T> = { code: number; data: T; message?: string };
type PS = { id: string; name: string; slug: string };
type Rec = {
  id: string; user_id: string; status: string; start_time: string; end_time: string;
  reason: string; supervisor_id: string; approval_status: string; approver: string; created_at: string;
};

const STATUS_LABEL: Record<string, string> = { rest: "休息", overtime: "加班", leave: "请假" };
const APPROVAL_COLOR: Record<string, string> = {
  pending: "bg-amber-100 text-amber-700",
  approved: "bg-emerald-100 text-emerald-700",
  rejected: "bg-red-100 text-red-700",
};

export default function AttendancePage() {
  const [spaces, setSpaces] = useState<PS[]>([]);
  const [psID, setPsID] = useState("");
  const [mine, setMine] = useState<Rec[]>([]);
  const [inbox, setInbox] = useState<Rec[]>([]);
  const [form, setForm] = useState({ status: "leave", start: "", end: "", reason: "", supervisor: "admin" });
  const [me, setMe] = useState("admin");

  useEffect(() => {
    fetch(`${API_BASE_URL}/project-spaces`)
      .then((r) => r.json())
      .then((r: Envelope<PS[]>) => {
        setSpaces(r.data ?? []);
        const def = (r.data ?? []).find((s) => s.id === "ps_default") ?? (r.data ?? [])[0];
        if (def) setPsID(def.id);
      });
  }, []);
  useEffect(() => { setMe(currentUser()); }, [psID]);

  const load = (id: string) => {
    if (!id) return;
    fetch(`${API_BASE_URL}/project-spaces/${id}/attendance/mine`).then((r) => r.json()).then((r: Envelope<Rec[]>) => setMine(r.data ?? []));
    fetch(`${API_BASE_URL}/attendance/inbox`).then((r) => r.json()).then((r: Envelope<Rec[]>) => setInbox(r.data ?? []));
  };
  useEffect(() => { load(psID); }, [psID]);

  async function submit() {
    if (!form.start || !form.end || !form.supervisor.trim()) return;
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/attendance`, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        status: form.status,
        start_time: form.start.length === 16 ? form.start + ":00Z" : form.start,
        end_time: form.end.length === 16 ? form.end + ":00Z" : form.end,
        reason: form.reason, supervisor_id: form.supervisor,
      }),
    });
    const r = await res.json();
    if (r.code !== 0) { alert(r.message); return; }
    setForm({ status: "leave", start: "", end: "", reason: "", supervisor: form.supervisor });
    load(psID);
  }
  async function decide(id: string, ok: boolean) {
    const res = await fetch(`${API_BASE_URL}/attendance/${id}/${ok ? "approve" : "reject"}`, { method: "POST" });
    const r = await res.json();
    if (r.code !== 0) alert(r.message);
    load(psID);
  }

  return (
    <div>
      <div className="mb-4 flex items-center gap-3">
        <h1 className="text-xl font-bold">🗓️ 考勤管理</h1>
        <select value={psID} onChange={(e) => setPsID(e.target.value)} className="rounded-md border border-neutral-300 px-2 py-1 text-sm">
          {spaces.map((s) => (<option key={s.id} value={s.id}>{s.name} ({s.slug})</option>))}
        </select>
        <span className="ml-auto text-xs text-neutral-500">当前用户：{me}（侧栏可切换，模拟员工/上级）</span>
      </div>
      <p className="mb-4 text-sm text-neutral-600">
        员工提交考勤（休息/加班/请假）→ 转直接上级审批收件箱 → 批准/驳回。本模块由平台自身流水线（opencode）产出。
      </p>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        {/* 提交 + 我的记录 */}
        <div>
          <div className="mb-2 text-sm font-semibold">提交考勤（作为 {me}）</div>
          <div className="mb-3 space-y-2 rounded-lg border border-neutral-200 bg-white p-3">
            <div className="flex flex-wrap gap-2 text-sm">
              <select value={form.status} onChange={(e) => setForm({ ...form, status: e.target.value })} className="rounded border border-neutral-300 px-2 py-1">
                <option value="leave">请假</option><option value="rest">休息</option><option value="overtime">加班</option>
              </select>
              <input type="datetime-local" value={form.start} onChange={(e) => setForm({ ...form, start: e.target.value })} className="rounded border border-neutral-300 px-2 py-1 text-sm" />
              <input type="datetime-local" value={form.end} onChange={(e) => setForm({ ...form, end: e.target.value })} className="rounded border border-neutral-300 px-2 py-1 text-sm" />
            </div>
            <input placeholder="直接上级用户名（如 admin）" value={form.supervisor} onChange={(e) => setForm({ ...form, supervisor: e.target.value })} className="w-full rounded border border-neutral-300 px-2 py-1 text-sm" />
            <input placeholder="事由（可选）" value={form.reason} onChange={(e) => setForm({ ...form, reason: e.target.value })} className="w-full rounded border border-neutral-300 px-2 py-1 text-sm" />
            <button onClick={submit} className="rounded bg-blue-600 px-3 py-1 text-sm text-white">提交</button>
          </div>

          <div className="mb-2 text-sm font-semibold">我的考勤（{mine.length}）</div>
          <div className="space-y-2">
            {mine.map((r) => (
              <div key={r.id} className="rounded-md border border-neutral-200 bg-white p-2 text-sm">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="rounded bg-blue-100 px-1.5 py-0.5 text-xs">{STATUS_LABEL[r.status] ?? r.status}</span>
                  <span className={`rounded px-1.5 py-0.5 text-xs ${APPROVAL_COLOR[r.approval_status] ?? "bg-neutral-100"}`}>{r.approval_status}</span>
                  <span className="text-xs text-neutral-400">{fmt(r.start_time)} → {fmt(r.end_time)}</span>
                </div>
                {r.reason && <div className="mt-1 text-xs text-neutral-500">{r.reason}</div>}
                {r.approver && <div className="text-xs text-neutral-400">审批人：{r.approver}</div>}
              </div>
            ))}
            {mine.length === 0 && <div className="text-sm text-neutral-400">暂无记录</div>}
          </div>
        </div>

        {/* 审批收件箱 */}
        <div>
          <div className="mb-2 text-sm font-semibold">审批收件箱（待 {me} 审批 · {inbox.length}）</div>
          <div className="space-y-2">
            {inbox.map((r) => (
              <div key={r.id} className="rounded-md border border-neutral-200 bg-white p-2 text-sm">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium">{r.user_id}</span>
                  <span className="rounded bg-blue-100 px-1.5 py-0.5 text-xs">{STATUS_LABEL[r.status] ?? r.status}</span>
                  <span className="text-xs text-neutral-400">{fmt(r.start_time)} → {fmt(r.end_time)}</span>
                  <button onClick={() => decide(r.id, true)} className="ml-auto rounded bg-emerald-100 px-2 py-0.5 text-xs text-emerald-700">批准</button>
                  <button onClick={() => decide(r.id, false)} className="rounded bg-red-100 px-2 py-0.5 text-xs text-red-700">驳回</button>
                </div>
                {r.reason && <div className="mt-1 text-xs text-neutral-500">事由：{r.reason}</div>}
              </div>
            ))}
            {inbox.length === 0 && <div className="text-sm text-neutral-400">收件箱为空（切换到上级用户即可看到待审批）</div>}
          </div>
          <div className="mt-3 rounded-md bg-neutral-50 p-2 text-xs text-neutral-500">
            演示：左侧切「当前用户」为 dev1 提交（上级填 admin），再切回 admin 即在此看到并审批。
          </div>
        </div>
      </div>
    </div>
  );
}

function fmt(s: string) {
  if (!s) return "";
  try { return new Date(s).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }); } catch { return s; }
}

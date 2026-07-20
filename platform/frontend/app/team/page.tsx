"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL } from "@/lib/api";

type Envelope<T> = { code: number; data: T; message?: string };
type PS = { id: string; name: string; slug: string };
type Req = {
  id: string;
  title: string;
  status: string;
  assignee?: string;
  application_id?: string;
  priority?: string;
  created_at?: string;
};
type Chg = {
  id: string;
  app_name?: string;
  status: string;
  created_at?: string;
  reviewer?: string;
};
type Team = {
  toClaim: Req[];
  inDev: Req[];
  toApprove: Chg[];
  toRelease: Chg[];
  delivered: Req[];
};

const fmt = (d?: string) =>
  d
    ? new Date(d).toLocaleString("zh-CN", {
        hour12: false,
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
      })
    : "";

export default function TeamPage() {
  const [spaces, setSpaces] = useState<PS[]>([]);
  const [psID, setPsID] = useState("");
  const [team, setTeam] = useState<Team>({
    toClaim: [],
    inDev: [],
    toApprove: [],
    toRelease: [],
    delivered: [],
  });
  const [users, setUsers] = useState<{ name: string }[]>([]);

  useEffect(() => {
    fetch(`${API_BASE_URL}/project-spaces`)
      .then((r) => r.json())
      .then((r: Envelope<PS[]>) => {
        setSpaces(r.data ?? []);
        const def = (r.data ?? []).find((s) => s.id === "ps_default") ?? (r.data ?? [])[0];
        if (def) setPsID(def.id);
      });
    fetch(`${API_BASE_URL}/users`)
      .then((r) => r.json())
      .then((r: Envelope<{ name: string }[]>) => setUsers(r.data ?? []));
  }, []);

  const load = (id: string) => {
    if (!id) return;
    fetch(`${API_BASE_URL}/project-spaces/${id}/team-tasks`)
      .then((r) => r.json())
      .then((r: Envelope<Team>) =>
        setTeam(r.data ?? { toClaim: [], inDev: [], toApprove: [], toRelease: [], delivered: [] })
      );
  };
  useEffect(() => {
    load(psID);
  }, [psID]);

  async function assign(rid: string, name: string) {
    if (!name) return;
    const r = await fetch(`${API_BASE_URL}/project-spaces/${psID}/requirements/${rid}/assign`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ assignee: name }),
    }).then((rr) => rr.json());
    if (r.code !== 0) alert(r.message);
    else load(psID);
  }

  const reqCard = (q: Req, claimable: boolean) => (
    <div key={q.id} className="mb-2 rounded border border-neutral-200 bg-white p-2 text-xs">
      <div className="truncate font-medium text-neutral-800">{q.title || q.id.slice(0, 12)}</div>
      <div className="mt-0.5 text-neutral-500">
        👤 {q.assignee || "未认领"}
        {q.priority && <span className="ml-1">{q.priority}</span>}
      </div>
      {q.created_at && <div className="text-neutral-400">📅 {fmt(q.created_at)}</div>}
      {claimable && !q.assignee && (
        <select
          className="mt-1 w-full rounded border border-neutral-300 px-1 py-0.5"
          defaultValue=""
          onChange={(e) => assign(q.id, e.target.value)}
        >
          <option value="">指派给…</option>
          {users.map((u) => (
            <option key={u.name} value={u.name}>
              {u.name}
            </option>
          ))}
        </select>
      )}
    </div>
  );
  const chgCard = (c: Chg) => (
    <div key={c.id} className="mb-2 rounded border border-neutral-200 bg-white p-2 text-xs">
      <div className="truncate font-medium text-neutral-800">{c.app_name || c.id.slice(0, 12)}</div>
      <div className="text-neutral-500">
        👤 {c.reviewer || "?"}
        {c.created_at && <span className="ml-1">· {fmt(c.created_at)}</span>}
      </div>
    </div>
  );

  const COLS = [
    { title: `📥 待认领(${team.toClaim.length})`, body: team.toClaim.map((q) => reqCard(q, true)) },
    { title: `🔨 开发中(${team.inDev.length})`, body: team.inDev.map((q) => reqCard(q, false)) },
    { title: `⏳ 待审批(${team.toApprove.length})`, body: team.toApprove.map(chgCard) },
    { title: `🚀 待上线(${team.toRelease.length})`, body: team.toRelease.map(chgCard) },
    {
      title: `✅ 已交付(${team.delivered.length})`,
      body: team.delivered.map((q) => reqCard(q, false)),
    },
  ];

  return (
    <div>
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <h1 className="text-xl font-bold">📋 团队看板</h1>
        <select
          value={psID}
          onChange={(e) => setPsID(e.target.value)}
          className="rounded-md border border-neutral-300 px-2 py-1 text-sm"
        >
          {spaces.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name} ({s.slug})
            </option>
          ))}
        </select>
        <span className="text-xs text-neutral-400">全队任务全景 · 待认领可直接指派给成员</span>
      </div>
      <div className="grid grid-cols-2 gap-3 md:grid-cols-3 lg:grid-cols-5">
        {COLS.map((col, i) => (
          <div key={i} className="rounded-lg bg-neutral-50 p-2">
            <div className="mb-2 text-xs font-semibold text-neutral-600">{col.title}</div>
            {col.body}
            {col.body.length === 0 && <div className="text-xs text-neutral-300">—</div>}
          </div>
        ))}
      </div>
    </div>
  );
}

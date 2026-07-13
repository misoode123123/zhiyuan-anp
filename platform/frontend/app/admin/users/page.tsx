"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL } from "@/lib/api";

type Envelope<T> = { code: number; data: T; message?: string };
type PS = { id: string; name: string; slug: string };
type Member = { user_id: string; role: string };

const ROLES = [
  { v: "business", l: "业务部门（提需求/验收）" },
  { v: "dev", l: "研发（派编码/评审）" },
  { v: "rule_architect", l: "规则架构师（规则管理）" },
  { v: "gatekeeper", l: "闸门负责人（审批）" },
  { v: "admin", l: "管理员（全部）" },
];

const ROLE_COLOR: Record<string, string> = {
  business: "bg-blue-100 text-blue-700",
  dev: "bg-purple-100 text-purple-700",
  rule_architect: "bg-amber-100 text-amber-700",
  gatekeeper: "bg-emerald-100 text-emerald-700",
  admin: "bg-red-100 text-red-700",
};

export default function UsersPage() {
  const [spaces, setSpaces] = useState<PS[]>([]);
  const [psID, setPsID] = useState("");
  const [members, setMembers] = useState<Member[]>([]);
  const [userID, setUserID] = useState("");
  const [role, setRole] = useState("business");
  const [msg, setMsg] = useState("");

  useEffect(() => {
    fetch(`${API_BASE_URL}/project-spaces`)
      .then((r) => r.json())
      .then((r: Envelope<PS[]>) => {
        setSpaces(r.data ?? []);
        if (r.data?.[0]) setPsID(r.data[0].id);
      });
  }, []);

  const load = (id: string) => {
    if (!id) return;
    fetch(`${API_BASE_URL}/project-spaces/${id}/members`)
      .then((r) => r.json())
      .then((r: Envelope<Member[]>) => setMembers(r.data ?? []));
  };
  useEffect(() => {
    load(psID);
  }, [psID]);

  async function add() {
    if (!userID.trim()) return;
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/members`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ user_id: userID, role }),
    });
    const r = await res.json();
    setMsg(r.code === 0 ? `✓ 已把 ${userID} 加入（${role}）` : `✗ ${r.message}`);
    if (r.code === 0) {
      setUserID("");
      load(psID);
    }
  }

  return (
    <div>
      <h1 className="mb-1 text-xl font-bold">🔐 用户权限</h1>
      <p className="mb-4 text-sm text-neutral-600">
        RBAC + ABAC：把用户加入项目空间并分配角色。请求带 <code className="rounded bg-neutral-100 px-1">X-User</code> 头标识用户（M1 模拟登录，后续接 OIDC/SSO）。
      </p>

      <div className="mb-4">
        <label className="text-xs text-neutral-500">项目空间</label>
        <select value={psID} onChange={(e) => setPsID(e.target.value)} className="ml-2 rounded-md border border-neutral-300 px-2 py-1 text-sm">
          {spaces.map((s) => (
            <option key={s.id} value={s.id}>{s.name} ({s.slug})</option>
          ))}
        </select>
      </div>

      {msg && <div className="mb-3 text-sm text-blue-700">{msg}</div>}

      <div className="mb-4 rounded-lg border border-neutral-200 bg-white p-3">
        <div className="mb-2 text-sm font-semibold">添加成员</div>
        <div className="flex gap-2">
          <input value={userID} onChange={(e) => setUserID(e.target.value)} placeholder="用户标识（如 alice）" className="flex-1 rounded-md border border-neutral-300 px-2 py-1 text-sm" />
          <select value={role} onChange={(e) => setRole(e.target.value)} className="rounded-md border border-neutral-300 px-2 py-1 text-sm">
            {ROLES.map((r) => (
              <option key={r.v} value={r.v}>{r.l}</option>
            ))}
          </select>
          <button onClick={add} className="rounded-md bg-blue-600 px-3 py-1 text-sm text-white">加入</button>
        </div>
      </div>

      <div>
        <div className="mb-2 text-sm font-semibold">成员（{members.length}）</div>
        <div className="space-y-1">
          {members.map((m, i) => (
            <div key={i} className="flex items-center gap-2 rounded-md border border-neutral-200 bg-white p-2 text-sm">
              <span className="font-mono text-xs">{m.user_id}</span>
              <span className={`rounded px-1.5 py-0.5 text-xs ${ROLE_COLOR[m.role] ?? "bg-neutral-100"}`}>{m.role}</span>
            </div>
          ))}
          {members.length === 0 && <div className="text-sm text-neutral-400">暂无成员</div>}
        </div>
      </div>
    </div>
  );
}

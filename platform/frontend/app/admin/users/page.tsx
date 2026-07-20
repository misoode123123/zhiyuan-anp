"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL } from "@/lib/api";

type Envelope<T> = { code: number; data: T; message?: string };
type PS = { id: string; name: string; slug: string };
type Member = { user_id: string; project_space_id: string; role: string };
type User = { id: string; name: string; email: string; status: string; spaces?: Member[] };

const ROLES = [
  { v: "business", l: "业务（提需求/验收）" },
  { v: "dev", l: "研发（派编码/评审）" },
  { v: "rule_architect", l: "规则架构师（规则）" },
  { v: "gatekeeper", l: "闸门（审批）" },
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
  const [users, setUsers] = useState<User[]>([]);
  const [members, setMembers] = useState<Member[]>([]);
  const [newUser, setNewUser] = useState({ name: "", email: "", password: "" });
  const [selUser, setSelUser] = useState("");
  const [role, setRole] = useState("business");
  const [msg, setMsg] = useState("");

  useEffect(() => {
    fetch(`${API_BASE_URL}/project-spaces`)
      .then((r) => r.json())
      .then((r: Envelope<PS[]>) => {
        setSpaces(r.data ?? []);
        const def = (r.data ?? []).find((s) => s.id === "ps_default") ?? (r.data ?? [])[0];
        if (def) setPsID(def.id);
      });
    loadUsers();
  }, []);

  const loadUsers = () =>
    fetch(`${API_BASE_URL}/users`)
      .then((r) => r.json())
      .then((r: Envelope<User[]>) => setUsers(r.data ?? []));
  const load = (id: string) => {
    if (!id) return;
    fetch(`${API_BASE_URL}/project-spaces/${id}/members`)
      .then((r) => r.json())
      .then((r: Envelope<Member[]>) => setMembers(r.data ?? []));
  };
  useEffect(() => {
    load(psID);
  }, [psID]);

  async function createUser() {
    if (!newUser.name.trim()) return;
    const res = await fetch(`${API_BASE_URL}/users`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(newUser),
    });
    const r = await res.json();
    setMsg(r.code === 0 ? `✓ 已创建用户 ${newUser.name}` : `✗ ${r.message}`);
    if (r.code === 0) {
      setNewUser({ name: "", email: "", password: "" });
      loadUsers();
    }
  }
  async function addMember() {
    if (!selUser) return;
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/members`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ user_id: selUser, role }),
    });
    const r = await res.json();
    setMsg(r.code === 0 ? `✓ ${selUser} 已加入（${role}）` : `✗ ${r.message}`);
    if (r.code === 0) {
      load(psID);
      loadUsers();
    }
  }

  return (
    <div>
      <h1 className="mb-1 text-xl font-bold">🔐 用户与权限</h1>
      <p className="mb-4 text-sm text-neutral-600">
        用户是一等实体（可管理的目录）。用户在某空间的角色由 membership
        决定（用户×空间×角色）。请求带 <code className="rounded bg-neutral-100 px-1">X-User</code>{" "}
        头（M1 模拟登录，后续接 OIDC）。
      </p>
      {msg && <div className="mb-3 text-sm text-blue-700">{msg}</div>}

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        {/* 用户目录 */}
        <div>
          <div className="mb-2 text-sm font-semibold">用户目录（{users.length}）</div>
          <div className="mb-3 flex flex-wrap gap-2 rounded-lg border border-neutral-200 bg-white p-2">
            <input
              value={newUser.name}
              onChange={(e) => setNewUser({ ...newUser, name: e.target.value })}
              placeholder="用户名（如 alice）"
              className="flex-1 rounded border border-neutral-300 px-2 py-1 text-sm"
            />
            <input
              value={newUser.email}
              onChange={(e) => setNewUser({ ...newUser, email: e.target.value })}
              placeholder="邮箱（可选）"
              className="flex-1 rounded border border-neutral-300 px-2 py-1 text-sm"
            />
            <input
              type="password"
              value={newUser.password}
              onChange={(e) => setNewUser({ ...newUser, password: e.target.value })}
              placeholder="初始密码"
              className="flex-1 rounded border border-neutral-300 px-2 py-1 text-sm"
            />
            <button
              onClick={createUser}
              className="rounded bg-blue-600 px-3 py-1 text-xs text-white"
            >
              新建用户
            </button>
          </div>
          <div className="space-y-1">
            {users.map((u) => (
              <div key={u.id} className="rounded-md border border-neutral-200 bg-white p-2 text-sm">
                <div className="flex items-center gap-2">
                  <span className="font-medium">{u.name}</span>
                  {u.email && <span className="text-xs text-neutral-400">{u.email}</span>}
                  <span
                    className={`rounded px-1.5 py-0.5 text-xs ${u.status === "active" ? "bg-emerald-100 text-emerald-700" : "bg-neutral-100"}`}
                  >
                    {u.status}
                  </span>
                </div>
                {u.spaces && u.spaces.length > 0 && (
                  <div className="mt-1 flex flex-wrap gap-1">
                    {u.spaces.map((s, i) => (
                      <span key={i} className="text-xs text-neutral-500">
                        {spaceName(s.project_space_id)}：
                        <span className={`rounded px-1 ${ROLE_COLOR[s.role] ?? ""}`}>{s.role}</span>
                      </span>
                    ))}
                  </div>
                )}
              </div>
            ))}
            {users.length === 0 && <div className="text-sm text-neutral-400">暂无用户</div>}
          </div>
        </div>

        {/* 空间成员管理 */}
        <div>
          <div className="mb-2 flex items-center gap-2">
            <span className="text-sm font-semibold">空间成员</span>
            <select
              value={psID}
              onChange={(e) => setPsID(e.target.value)}
              className="rounded-md border border-neutral-300 px-2 py-1 text-sm"
            >
              {spaces.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          </div>
          <div className="mb-3 flex gap-2 rounded-lg border border-neutral-200 bg-white p-2">
            <select
              value={selUser}
              onChange={(e) => setSelUser(e.target.value)}
              className="flex-1 rounded border border-neutral-300 px-2 py-1 text-sm"
            >
              <option value="">— 选用户 —</option>
              {users.map((u) => (
                <option key={u.id} value={u.name}>
                  {u.name}
                </option>
              ))}
            </select>
            <select
              value={role}
              onChange={(e) => setRole(e.target.value)}
              className="rounded border border-neutral-300 px-2 py-1 text-sm"
            >
              {ROLES.map((r) => (
                <option key={r.v} value={r.v}>
                  {r.l}
                </option>
              ))}
            </select>
            <button
              onClick={addMember}
              className="rounded bg-blue-600 px-3 py-1 text-xs text-white"
            >
              加入空间
            </button>
          </div>
          <div className="space-y-1">
            {members.map((m, i) => (
              <div
                key={i}
                className="flex items-center gap-2 rounded-md border border-neutral-200 bg-white p-2 text-sm"
              >
                <span className="font-mono text-xs">{m.user_id}</span>
                <span
                  className={`rounded px-1.5 py-0.5 text-xs ${ROLE_COLOR[m.role] ?? "bg-neutral-100"}`}
                >
                  {m.role}
                </span>
              </div>
            ))}
            {members.length === 0 && <div className="text-sm text-neutral-400">该空间暂无成员</div>}
          </div>
        </div>
      </div>
    </div>
  );

  function spaceName(id: string) {
    return spaces.find((s) => s.id === id)?.name ?? id.slice(0, 10);
  }
}

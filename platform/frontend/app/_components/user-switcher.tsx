"use client";

import { useEffect, useState } from "react";
import { currentUser, setCurrentUser } from "@/lib/api";

// 演示用户（与后端 SeedBootstrapMembers 对齐）。
const PRESETS = [
  { id: "admin", label: "管理员 (admin)", role: "全部权限" },
  { id: "dev1", label: "研发 (dev1)", role: "派编码/审批" },
  { id: "biz1", label: "业务 (biz1)", role: "提需求" },
];

export function UserSwitcher() {
  const [user, setUser] = useState("admin");

  useEffect(() => {
    setUser(currentUser());
    const onChange = () => setUser(currentUser());
    window.addEventListener("anp:user-changed", onChange);
    return () => window.removeEventListener("anp:user-changed", onChange);
  }, []);

  function pick(u: string) {
    setCurrentUser(u);
    setUser(u);
  }

  return (
    <div>
      <div className="mb-1 text-xs text-neutral-500">当前用户（M1 模拟登录）</div>
      <select
        value={user}
        onChange={(e) => pick(e.target.value)}
        className="w-full rounded-md border border-neutral-300 px-2 py-1.5 text-sm"
        title="切换模拟登录用户，演示 RBAC 权限差异"
      >
        {PRESETS.map((p) => (
          <option key={p.id} value={p.id}>
            {p.label}
          </option>
        ))}
        {!PRESETS.some((p) => p.id === user) && <option value={user}>{user}</option>}
      </select>
      <div className="mt-1 text-[11px] text-neutral-400">
        {PRESETS.find((p) => p.id === user)?.role ?? "自定义用户"}
      </div>
    </div>
  );
}

"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { API_BASE_URL, setAuthToken } from "@/lib/api";

export default function LoginPage() {
  const router = useRouter();
  const [name, setName] = useState("admin");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState("");
  const [loading, setLoading] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setLoading(true);
    setErr("");
    try {
      const res = await fetch(`${API_BASE_URL}/auth/login`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name, password }),
      });
      const r = await res.json();
      if (r.code === 0 && r.data?.token) {
        setAuthToken(r.data.token, r.data.user?.name || name);
        router.push("/");
      } else {
        setErr(r.message || "登录失败");
      }
    } catch (ex) {
      setErr(String(ex));
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="flex min-h-[70vh] items-center justify-center">
      <form onSubmit={submit} className="w-80 rounded-lg border border-neutral-200 bg-white p-6 shadow-sm">
        <h1 className="mb-1 text-xl font-bold">登录 智源 ANP</h1>
        <p className="mb-4 text-xs text-neutral-500">企业 AI 原生研发平台 · 真实账号登录</p>
        <label className="text-xs text-neutral-500">用户名</label>
        <input value={name} onChange={(e) => setName(e.target.value)} className="mt-1 mb-3 w-full rounded border border-neutral-300 px-2 py-1.5 text-sm" />
        <label className="text-xs text-neutral-500">密码</label>
        <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} className="mt-1 mb-3 w-full rounded border border-neutral-300 px-2 py-1.5 text-sm" placeholder="初始 admin / admin123" />
        {err && <div className="mb-2 text-sm text-red-500">{err}</div>}
        <button disabled={loading} className="w-full rounded bg-blue-600 py-2 text-sm text-white disabled:opacity-50">
          {loading ? "登录中…" : "登录"}
        </button>
        <div className="mt-3 text-center text-xs text-neutral-400">
          <a href="/" className="hover:underline">跳过，以游客继续（X-User 模拟）</a>
        </div>
      </form>
    </div>
  );
}

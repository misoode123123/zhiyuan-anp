"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL } from "@/lib/api";

type Envelope<T> = { code: number; data: T; message?: string };
type PS = { id: string; name: string; slug: string };
type App = {
  id: string; name: string; repo_dir: string; internal_port: number;
  image: string; container_name: string; host_port: number; url: string;
  version: number; status: string; last_error: string; build_log: string;
};
type Req = { id: string; title: string; status: string; application_id: string };

const STATUS_COLOR: Record<string, string> = {
  running: "bg-emerald-100 text-emerald-700",
  building: "bg-amber-100 text-amber-700",
  registered: "bg-neutral-100 text-neutral-500",
  stopped: "bg-blue-100 text-blue-700",
  failed: "bg-red-100 text-red-700",
};

export default function ApplicationsPage() {
  const [spaces, setSpaces] = useState<PS[]>([]);
  const [psID, setPsID] = useState("");
  const [apps, setApps] = useState<App[]>([]);
  const [form, setForm] = useState({ name: "", repo_dir: "/data/repos/myapp", internal_port: 8080 });
  const [logsFor, setLogsFor] = useState<string>("");
  const [logs, setLogs] = useState("");
  const [reqsFor, setReqsFor] = useState<string>("");
  const [appReqs, setAppReqs] = useState<Req[]>([]);

  useEffect(() => {
    fetch(`${API_BASE_URL}/project-spaces`)
      .then((r) => r.json())
      .then((r: Envelope<PS[]>) => {
        setSpaces(r.data ?? []);
        const def = (r.data ?? []).find((s) => s.id === "ps_default") ?? (r.data ?? [])[0];
        if (def) setPsID(def.id);
      });
  }, []);

  const load = (id: string) => {
    if (!id) return;
    fetch(`${API_BASE_URL}/project-spaces/${id}/apps`).then((r) => r.json()).then((r: Envelope<App[]>) => setApps(r.data ?? []));
  };
  useEffect(() => {
    load(psID);
    // 有 building 中的应用时轮询
    const t = setInterval(() => load(psID), 3000);
    return () => clearInterval(t);
  }, [psID]);

  async function register() {
    if (!form.name.trim() || !form.repo_dir.trim()) return;
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps`, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify(form),
    });
    const r = await res.json();
    if (r.code !== 0) { alert(r.message); return; }
    setForm({ name: "", repo_dir: form.repo_dir, internal_port: form.internal_port });
    load(psID);
  }
  async function act(id: string, action: "deploy" | "stop" | "start") {
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${id}/${action}`, { method: "POST" });
    const r = await res.json();
    if (r.code !== 0) alert(r.message);
    load(psID);
  }
  async function remove(id: string) {
    if (!confirm("删除应用（含容器）？")) return;
    await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${id}`, { method: "DELETE" });
    load(psID);
  }
  async function showLogs(id: string) {
    setLogsFor(id);
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${id}/logs`);
    const r = await res.json();
    setLogs(r.data?.logs ?? "(无)");
  }
  async function showReqs(id: string) {
    if (reqsFor === id) { setReqsFor(""); return; }
    setReqsFor(id);
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${id}/requirements`);
    const r = await res.json();
    setAppReqs(r.data ?? []);
  }

  return (
    <div>
      <div className="mb-4 flex items-center gap-3">
        <h1 className="text-xl font-bold">📦 应用部署</h1>
        <select value={psID} onChange={(e) => setPsID(e.target.value)} className="rounded-md border border-neutral-300 px-2 py-1 text-sm">
          {spaces.map((s) => (<option key={s.id} value={s.id}>{s.name} ({s.slug})</option>))}
        </select>
      </div>
      <p className="mb-4 text-sm text-neutral-600">
        把研发产出的应用（含 Dockerfile 的源码目录）自动 <b>docker build → docker run → 分配端口 → 暴露 URL</b>。
        repo_dir 填 <b>docker 守护进程可见的路径</b>（生产 .28 上形如 <code>/data/repos/myapp</code>）。
      </p>

      {/* 注册 */}
      <div className="mb-4 flex flex-wrap items-end gap-2 rounded-lg border border-neutral-200 bg-white p-3 text-sm">
        <div>
          <label className="block text-xs text-neutral-500">应用名</label>
          <input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="如 hello-go" className="rounded border border-neutral-300 px-2 py-1" />
        </div>
        <div className="min-w-[260px] flex-1">
          <label className="block text-xs text-neutral-500">源码路径 repo_dir（含 Dockerfile）</label>
          <input value={form.repo_dir} onChange={(e) => setForm({ ...form, repo_dir: e.target.value })} className="w-full rounded border border-neutral-300 px-2 py-1 font-mono text-xs" />
        </div>
        <div>
          <label className="block text-xs text-neutral-500">容器内端口</label>
          <input type="number" value={form.internal_port} onChange={(e) => setForm({ ...form, internal_port: Number(e.target.value) })} className="w-24 rounded border border-neutral-300 px-2 py-1" />
        </div>
        <button onClick={register} className="rounded bg-blue-600 px-3 py-1.5 text-white">注册</button>
      </div>

      {/* 应用列表 */}
      <div className="space-y-3">
        {apps.map((a) => (
          <div key={a.id} className="rounded-lg border border-neutral-200 bg-white p-3">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-mono font-medium">{a.name}</span>
              <span className={`rounded px-1.5 py-0.5 text-xs ${STATUS_COLOR[a.status] ?? "bg-neutral-100"}`}>{a.status}</span>
              {a.image && <span className="text-xs text-neutral-400">v{a.version} · {a.image}</span>}
              <div className="ml-auto flex gap-1">
                <button onClick={() => act(a.id, "deploy")} className="rounded bg-blue-100 px-2 py-0.5 text-xs text-blue-700">构建部署</button>
                {a.status === "running" && <button onClick={() => act(a.id, "stop")} className="rounded bg-neutral-100 px-2 py-0.5 text-xs">停止</button>}
                {a.status === "stopped" && <button onClick={() => act(a.id, "start")} className="rounded bg-emerald-100 px-2 py-0.5 text-xs text-emerald-700">启动</button>}
                <button onClick={() => showReqs(a.id)} className="rounded bg-neutral-100 px-2 py-0.5 text-xs">需求</button>
                <button onClick={() => showLogs(a.id)} className="rounded bg-neutral-100 px-2 py-0.5 text-xs">日志</button>
                <button onClick={() => remove(a.id)} className="rounded bg-red-100 px-2 py-0.5 text-xs text-red-700">删除</button>
              </div>
            </div>
            <div className="mt-1 text-xs text-neutral-500">
              repo: <code>{a.repo_dir}</code> · 内部端口 {a.internal_port}
              {a.host_port ? ` · 宿主端口 ${a.host_port}` : ""}
            </div>
            {a.url && (
              <div className="mt-1 text-sm">
                访问入口：<a href={a.url} target="_blank" rel="noreferrer" className="text-blue-600 hover:underline">{a.url}</a>
              </div>
            )}
            {a.last_error && <div className="mt-1 rounded bg-red-50 p-1 text-xs text-red-700">{a.last_error}</div>}
            {logsFor === a.id && (
              <pre className="mt-2 max-h-48 overflow-auto rounded bg-neutral-900 p-2 text-xs text-green-300">{logs}</pre>
            )}
            {reqsFor === a.id && (
              <div className="mt-2 rounded bg-neutral-50 p-2 text-xs">
                <div className="mb-1 text-neutral-500">归属此应用的需求（{appReqs.length}）</div>
                {appReqs.map((q) => (
                  <div key={q.id} className="flex items-center gap-2 py-0.5">
                    <span className={`rounded px-1.5 py-0.5 ${q.status === "delivered" ? "bg-emerald-100 text-emerald-700" : "bg-neutral-100 text-neutral-500"}`}>{q.status}</span>
                    <span className="truncate">{q.title}</span>
                  </div>
                ))}
                {appReqs.length === 0 && <div className="text-neutral-400">暂无（发布此应用的需求后会自动归属到此）</div>}
              </div>
            )}
          </div>
        ))}
        {apps.length === 0 && <div className="text-sm text-neutral-400">暂无应用。注册一个（源码目录需含 Dockerfile）后点「构建部署」。</div>}
      </div>

      <div className="mt-4 rounded-md bg-amber-50 p-2 text-xs text-amber-700">
        说明：构建部署在 ANP 后端容器内经宿主 docker socket 执行。repo_dir 必须是<b>后端容器内可见</b>的路径（产出应用默认在 <code>/data/repos/&lt;应用名&gt;</code>，对应宿主 <code>/opt/anp/data/repos/...</code>）。端口自动从 9100-9300 分配。
      </div>
    </div>
  );
}

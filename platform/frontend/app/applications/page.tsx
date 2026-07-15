"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL } from "@/lib/api";

type Envelope<T> = { code: number; data: T; message?: string };
type PS = { id: string; name: string; slug: string };
type Instance = {
  env: string; status: string; url: string; version: number;
  host_port: number; image: string; updated_at: string;
};
type EnvVar = { id: string; key: string; value: string; is_secret: boolean };
type App = {
  id: string; name: string; repo_dir: string; internal_port: number;
  image: string; container_name: string; host_port: number; url: string;
  version: number; status: string; last_error: string; build_log: string;
  updated_at: string;
  instances?: Instance[]; // 各环境部署实例（test/prod）
};
type Req = { id: string; title: string; status: string; application_id: string };
type Detail = {
  application: App;
  requirements: Req[];
  changes: { id: string; status: string; kind: string; source_id: string; created_at: string }[];
  releases: { id: string; version: string; status: string; change_id: string; created_at: string }[];
  commits: { sha: string; message: string; date: string }[];
};

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
  const [form, setForm] = useState({ name: "", internal_port: 8080 });
  const [wsTool, setWsTool] = useState("opencode"); // 交互编码工具（开发者可选，不同人选不同）
  const [logsFor, setLogsFor] = useState<string>("");
  const [logs, setLogs] = useState("");
  const [reqsFor, setReqsFor] = useState<string>("");
  const [appReqs, setAppReqs] = useState<Req[]>([]);
  const [detailFor, setDetailFor] = useState<string>("");
  const [detail, setDetail] = useState<Detail | null>(null);
  const [envFor, setEnvFor] = useState<string>("");
  const [appEnvs, setAppEnvs] = useState<EnvVar[]>([]);
  const [envForm, setEnvForm] = useState({ key: "", value: "", is_secret: false });

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
    if (!form.name.trim()) return;
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps`, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify(form),
    });
    const r = await res.json();
    if (r.code !== 0) { alert(r.message); return; }
    setForm({ name: "", internal_port: form.internal_port });
    load(psID);
  }
  async function act(id: string, action: "deploy" | "stop" | "start", env?: string) {
    const body = action === "deploy" && env ? { env } : {};
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${id}/${action}`, {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body),
    });
    const r = await res.json();
    if (r.code !== 0) alert(r.message);
    load(psID);
  }
  async function promote(id: string) {
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${id}/promote`, { method: "POST" });
    const r = await res.json();
    if (r.code !== 0) alert(r.message);
    load(psID);
  }
  async function openWorkspace(id: string, tool: string) {
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${id}/workspace`, {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ tool }),
    });
    const r = await res.json();
    if (r.code === 0 && r.data?.url) {
      window.open(r.data.url, "_blank"); // 打开 opencode/工具官方 web UI
    } else {
      alert(r.message || "启动编码工作台失败");
    }
  }
  async function reloadEnv(id: string) {
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${id}/env`);
    const r = await res.json();
    setAppEnvs(r.data ?? []);
  }
  async function showEnv(id: string) {
    if (envFor === id) { setEnvFor(""); return; }
    setEnvFor(id);
    await reloadEnv(id);
  }
  async function saveEnv(id: string) {
    if (!envForm.key.trim()) return;
    await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${id}/env`, {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(envForm),
    });
    setEnvForm({ key: "", value: "", is_secret: false });
    reloadEnv(id);
  }
  async function removeEnv(id: string, key: string) {
    await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${id}/env/${encodeURIComponent(key)}`, { method: "DELETE" });
    reloadEnv(id);
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
  async function showDetail(id: string) {
    if (detailFor === id) { setDetailFor(""); setDetail(null); return; }
    setDetailFor(id);
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${id}/detail`);
    const r = await res.json();
    setDetail(r.data ?? null);
  }
  async function deployCommit(appID: string, sha: string) {
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/deploy-commit`, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ sha }),
    });
    const r = await res.json();
    if (r.code !== 0) alert(r.message);
    load(psID);
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
        <div>
          <label className="block text-xs text-neutral-500">容器内端口（可选）</label>
          <input type="number" value={form.internal_port} onChange={(e) => setForm({ ...form, internal_port: Number(e.target.value) })} className="w-24 rounded border border-neutral-300 px-2 py-1" />
        </div>
        <button onClick={register} className="rounded bg-blue-600 px-3 py-1.5 text-white">创建应用</button>
        <span className="text-xs text-neutral-400">仓库自动托管到 /data/repos/&lt;应用名&gt;（git），opencode 编码即提交到此</span>
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
                <button onClick={() => act(a.id, "deploy")} className="rounded bg-blue-100 px-2 py-0.5 text-xs text-blue-700">构建部署(test)</button>
                <button onClick={() => promote(a.id)} className="rounded bg-emerald-100 px-2 py-0.5 text-xs text-emerald-700">🚀 上线(prod)</button>
                <select value={wsTool} onChange={(e) => setWsTool(e.target.value)} className="rounded border border-neutral-300 px-1 py-0.5 text-xs" title="选择交互编码工具">
                  <option value="opencode">opencode</option>
                  <option value="claude">claude*</option>
                  <option value="codex">codex*</option>
                </select>
                <button onClick={() => openWorkspace(a.id, wsTool)} className="rounded bg-purple-100 px-2 py-0.5 text-xs text-purple-700" title="打开该工具的官方交互编码界面（*为预留）">🧑‍💻 编码</button>
                <button onClick={() => showEnv(a.id)} className="rounded bg-neutral-100 px-2 py-0.5 text-xs">⚙️变量</button>
                {a.status === "running" && <button onClick={() => act(a.id, "stop")} className="rounded bg-neutral-100 px-2 py-0.5 text-xs">停止</button>}
                {a.status === "stopped" && <button onClick={() => act(a.id, "start")} className="rounded bg-emerald-100 px-2 py-0.5 text-xs text-emerald-700">启动</button>}
                <button onClick={() => showReqs(a.id)} className="rounded bg-neutral-100 px-2 py-0.5 text-xs">需求</button>
                <button onClick={() => showDetail(a.id)} className="rounded bg-neutral-100 px-2 py-0.5 text-xs">详情</button>
                <button onClick={() => showLogs(a.id)} className="rounded bg-neutral-100 px-2 py-0.5 text-xs">日志</button>
                <button onClick={() => remove(a.id)} className="rounded bg-red-100 px-2 py-0.5 text-xs text-red-700">删除</button>
              </div>
            </div>
            <div className="mt-1 text-xs text-neutral-500">
              repo: <code>{a.repo_dir}</code> · 内部端口 {a.internal_port}
              {a.host_port ? ` · 宿主端口 ${a.host_port}` : ""}
            </div>
            {a.updated_at && (
              <div className="text-xs text-neutral-400">
                {a.status === "running" ? "部署于" : "更新于"}：{new Date(a.updated_at).toLocaleString("zh-CN", { hour12: false })}
              </div>
            )}
            <div className="mt-2 grid grid-cols-1 gap-2 sm:grid-cols-2">
              {(["test", "prod"] as const).map((env) => {
                const ins = a.instances?.find((i) => i.env === env);
                const label = env === "prod" ? "🚀 生产 prod" : "🧪 测试 test";
                return (
                  <div key={env} className="rounded bg-neutral-50 p-2 text-xs">
                    <div className="flex items-center gap-2">
                      <span className={`rounded px-1.5 py-0.5 font-medium ${env === "prod" ? "bg-blue-100 text-blue-700" : "bg-purple-100 text-purple-700"}`}>{label}</span>
                      {ins && <span className={`rounded px-1.5 py-0.5 ${STATUS_COLOR[ins.status] ?? "bg-neutral-100"}`}>{ins.status}</span>}
                      {ins && ins.version > 0 && <span className="text-neutral-400">v{ins.version}</span>}
                    </div>
                    {ins?.url ? (
                      <a href={ins.url} target="_blank" rel="noreferrer" className="mt-1 block truncate text-blue-600 hover:underline">{ins.url}</a>
                    ) : (
                      <div className="mt-1 text-neutral-400">{env === "prod" ? "未上线（点「上线」部署）" : "未部署（发布或「构建部署」）"}</div>
                    )}
                  </div>
                );
              })}
            </div>
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
            {envFor === a.id && (
              <div className="mt-2 rounded bg-neutral-50 p-2 text-xs">
                <div className="mb-1 text-neutral-500">运行时环境变量（部署时 -e 注入容器；🔒=密钥已隐藏明文）</div>
                <div className="space-y-1">
                  {appEnvs.map((e) => (
                    <div key={e.id} className="flex items-center gap-2">
                      <code className="text-neutral-700">{e.key}</code>
                      <span className="text-neutral-400">=</span>
                      <span className={e.is_secret ? "text-amber-600" : "text-neutral-600"}>{e.is_secret ? "🔒 已隐藏" : (e.value || "(空)")}</span>
                      <button onClick={() => removeEnv(a.id, e.key)} className="ml-auto rounded bg-red-100 px-1.5 py-0.5 text-red-700">删</button>
                    </div>
                  ))}
                  {appEnvs.length === 0 && <div className="text-neutral-400">暂无</div>}
                </div>
                <div className="mt-2 flex flex-wrap items-center gap-1 border-t border-neutral-200 pt-2">
                  <input value={envForm.key} onChange={(ev) => setEnvForm({ ...envForm, key: ev.target.value })} placeholder="KEY" className="w-28 rounded border border-neutral-300 px-1 py-0.5" />
                  <input value={envForm.value} onChange={(ev) => setEnvForm({ ...envForm, value: ev.target.value })} placeholder="value" type={envForm.is_secret ? "password" : "text"} className="flex-1 rounded border border-neutral-300 px-1 py-0.5" />
                  <label className="flex items-center gap-1"><input type="checkbox" checked={envForm.is_secret} onChange={(ev) => setEnvForm({ ...envForm, is_secret: ev.target.checked })} />密钥</label>
                  <button onClick={() => saveEnv(a.id)} className="rounded bg-blue-600 px-2 py-0.5 text-white">保存</button>
                </div>
              </div>
            )}
            {detailFor === a.id && detail && (
              <>
              <div className="mt-2 grid grid-cols-1 gap-2 rounded bg-neutral-50 p-2 text-xs md:grid-cols-3">
                <div>
                  <div className="mb-1 font-medium text-neutral-500">需求（{detail.requirements.length}）</div>
                  {detail.requirements.map((q) => (<div key={q.id} className="truncate"><span className={q.status === "delivered" ? "text-emerald-600" : "text-neutral-500"}>●</span> {q.title}</div>))}
                  {detail.requirements.length === 0 && <div className="text-neutral-400">无</div>}
                </div>
                <div>
                  <div className="mb-1 font-medium text-neutral-500">变更（{detail.changes.length}）</div>
                  {detail.changes.map((c) => (<div key={c.id}><span className={c.status === "approved" ? "text-emerald-600" : "text-amber-600"}>●</span> {c.kind} · {c.status}</div>))}
                  {detail.changes.length === 0 && <div className="text-neutral-400">无</div>}
                </div>
                <div>
                  <div className="mb-1 font-medium text-neutral-500">发布（{detail.releases.length}）</div>
                  {detail.releases.map((r) => (<div key={r.id}><span className="text-blue-600">●</span> {r.version} · {r.status}</div>))}
                  {detail.releases.length === 0 && <div className="text-neutral-400">无</div>}
                </div>
              </div>
              {detail.commits.length > 0 && (
                <div className="mt-2 border-t border-neutral-200 pt-2">
                  <div className="mb-1 font-medium text-neutral-500">版本历史（{detail.commits.length}，可部署/回滚任意版本）</div>
                  <div className="space-y-1">
                    {detail.commits.map((c) => (
                      <div key={c.sha} className="flex items-center gap-2">
                        <code className="text-xs text-neutral-400">{c.sha.slice(0, 7)}</code>
                        <span className="truncate text-neutral-700">{c.message}</span>
                        <button onClick={() => deployCommit(a.id, c.sha)} className="ml-auto rounded bg-amber-100 px-2 py-0.5 text-xs text-amber-700">部署此版本</button>
                      </div>
                    ))}
                  </div>
                </div>
              )}
              </>
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

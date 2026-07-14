"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL } from "@/lib/api";
import { FlowStepper } from "../_components/stepper";

type Envelope<T> = { code: number; data: T; message?: string };
type ProjectSpace = { id: string; name: string; slug: string };
type Task = {
  id: string;
  kind: string; // code（手动）/ dispatch（需求派发）
  source_id: string;
  repo_dir: string;
  prompt: string;
  model: string;
  status: string; // running/completed/failed
  output: string | null;
  change_id: string | null;
  created_at: string;
};

const STATUS_COLOR: Record<string, string> = {
  running: "bg-amber-100 text-amber-700",
  completed: "bg-emerald-100 text-emerald-700",
  failed: "bg-red-100 text-red-700",
};

export default function DevPage() {
  const [spaces, setSpaces] = useState<ProjectSpace[]>([]);
  const [psID, setPsID] = useState("");
  const [repoDir, setRepoDir] = useState("D:/Projects/智源-ANP平台/pilots/oc-pilot");
  const [prompt, setPrompt] = useState("");
  const [model, setModel] = useState("zai-coding/glm-5.1");
  const [list, setList] = useState<Task[]>([]);
  const [loading, setLoading] = useState(false);
  const [msg, setMsg] = useState("");

  // 加载项目空间列表
  useEffect(() => {
    fetch(`${API_BASE_URL}/project-spaces`)
      .then((r) => r.json())
      .then((r: Envelope<ProjectSpace[]>) => {
        setSpaces(r.data ?? []);
        if (r.data?.[0]) setPsID(r.data[0].id);
      });
  }, []);

  // 加载当前项目空间的编码任务（含需求派发 + 手动）
  const loadList = (id: string) => {
    if (!id) return;
    fetch(`${API_BASE_URL}/project-spaces/${id}/code-tasks`)
      .then((r) => r.json())
      .then((r: Envelope<Task[]>) => setList(r.data ?? []))
      .catch(() => {});
  };
  useEffect(() => {
    loadList(psID);
  }, [psID]);

  // 存在 running 任务时自动轮询刷新（任务完成自动登记变更，列表会更新）
  useEffect(() => {
    if (!psID || !list.some((t) => t.status === "running")) return;
    const iv = setInterval(() => loadList(psID), 3000);
    return () => clearInterval(iv);
  }, [list, psID]);

  async function run() {
    if (!prompt.trim() || loading || !psID) return;
    setLoading(true);
    setMsg("");
    try {
      const res = await fetch(`${API_BASE_URL}/code`, {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-Project-Space-Id": psID },
        body: JSON.stringify({ repo_dir: repoDir, prompt, model }),
      });
      const r = await res.json();
      if (r.data?.task_id) {
        setMsg(`✅ 已派发编码任务 ${r.data.task_id}，后台执行中，完成后自动登记变更待🚪G3 审批`);
        setPrompt("");
        loadList(psID); // 立即刷新，轮询接管 running 进度
      } else {
        setMsg(`✗ ${r.message ?? "提交失败"}`);
      }
    } catch (e) {
      setMsg(`✗ ${e}`);
    } finally {
      setLoading(false);
    }
  }

  const runningCount = list.filter((t) => t.status === "running").length;

  return (
    <div>
      <h1 className="mb-1 text-xl font-bold">研发工作台</h1>
      <FlowStepper current={1} />
      <p className="mb-4 text-sm text-neutral-600">
        异步编码引擎：opencode + 智谱 GLM-5.1。需求工作台点「⚡ 派发编码」的任务会自动出现在下方看板，<b>无需在此重复录入</b>；下方输入框用于无需求规格的独立编码。
      </p>

      {/* 项目空间 */}
      <div className="mb-3">
        <label className="text-xs text-neutral-500">项目空间</label>
        <select value={psID} onChange={(e) => setPsID(e.target.value)} className="ml-2 rounded-md border border-neutral-300 px-2 py-1 text-sm">
          {spaces.map((s) => (<option key={s.id} value={s.id}>{s.name} ({s.slug})</option>))}
        </select>
      </div>

      {/* 手动派发（独立编码入口） */}
      <div className="mb-4 rounded-lg border border-neutral-200 bg-white p-4">
        <div className="mb-2 text-sm font-semibold">✏️ 手动派发编码任务</div>
        <div className="mb-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label className="text-xs text-neutral-500">目标仓库路径</label>
            <input value={repoDir} onChange={(e) => setRepoDir(e.target.value)} className="w-full rounded-md border border-neutral-300 px-2 py-1.5 text-sm" />
          </div>
          <div>
            <label className="text-xs text-neutral-500">模型</label>
            <input value={model} onChange={(e) => setModel(e.target.value)} className="w-full rounded-md border border-neutral-300 px-2 py-1.5 text-sm" />
          </div>
        </div>
        <label className="text-xs text-neutral-500">编码任务</label>
        <textarea value={prompt} onChange={(e) => setPrompt(e.target.value)} rows={2} placeholder="例：创建 hello.py 打印 hello world" className="mt-1 w-full rounded-md border border-neutral-300 px-2 py-1.5 text-sm" />
        <button onClick={run} disabled={loading || !psID} className="mt-2 rounded-md bg-blue-600 px-4 py-2 text-sm text-white disabled:opacity-50">
          {loading ? "提交中…" : "派发编码任务"}
        </button>
        {msg && <div className="mt-2 rounded-md bg-blue-50 p-2 text-sm text-blue-800">{msg}</div>}
      </div>

      {/* 编码任务看板 */}
      <div className="mb-2 flex items-center justify-between">
        <div className="text-sm font-semibold">编码任务看板（{list.length}）</div>
        {runningCount > 0 && <div className="text-xs text-amber-700">⏳ {runningCount} 个执行中，每 3s 自动刷新</div>}
      </div>
      <div className="space-y-2">
        {list.map((t) => {
          const fromReq = t.kind === "dispatch";
          return (
            <div key={t.id} className="rounded-md border border-neutral-200 bg-white p-3 text-sm">
              <div className="flex flex-wrap items-center gap-2">
                <span className={`rounded px-1.5 py-0.5 text-xs ${fromReq ? "bg-emerald-100 text-emerald-700" : "bg-blue-100 text-blue-700"}`}>
                  {fromReq ? "⚡ 需求派发" : "✏️ 手动派发"}
                </span>
                <span className={`rounded px-1.5 py-0.5 text-xs ${STATUS_COLOR[t.status] ?? "bg-neutral-100 text-neutral-600"}`}>{t.status}</span>
                {fromReq && t.source_id && <span className="text-xs text-neutral-400">来自需求 {t.source_id.slice(0, 8)}</span>}
                {t.change_id && <span className="text-xs text-neutral-400">→ 变更 {t.change_id.slice(0, 8)}（待🚪G3 审批）</span>}
                {t.status === "completed" && t.change_id && (
                  <span className="text-xs text-emerald-700">✓ 去「🚪 变更审批」批准</span>
                )}
                <span className="ml-auto font-mono text-xs text-neutral-400">{t.id.slice(0, 12)}</span>
              </div>
              <div className="mt-1 line-clamp-2 text-neutral-700">{t.prompt}</div>
              <div className="mt-1 text-xs text-neutral-400">{t.model} · {t.repo_dir}</div>
              {t.output && (
                <details className="mt-2">
                  <summary className="cursor-pointer text-xs text-neutral-500">产出/日志</summary>
                  <pre className="mt-1 max-h-48 overflow-auto rounded bg-neutral-900 p-2 text-xs text-green-300">{t.output}</pre>
                </details>
              )}
            </div>
          );
        })}
        {list.length === 0 && <div className="text-sm text-neutral-400">暂无编码任务。去需求工作台「⚡ 派发编码」，或在此手动派发。</div>}
      </div>
    </div>
  );
}

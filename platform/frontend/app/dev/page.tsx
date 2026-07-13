"use client";

import { useState } from "react";
import { API_BASE_URL } from "@/lib/api";
import { FlowStepper } from "../_components/stepper";

type Task = {
  id: string;
  status: string;
  output: string;
  change_id: string;
  prompt: string;
  model: string;
};

const STATUS_COLOR: Record<string, string> = {
  running: "bg-amber-100 text-amber-700",
  completed: "bg-emerald-100 text-emerald-700",
  failed: "bg-red-100 text-red-700",
};

export default function DevPage() {
  const [repoDir, setRepoDir] = useState("D:/Projects/智源-ANP平台/pilots/oc-pilot");
  const [prompt, setPrompt] = useState("");
  const [model, setModel] = useState("zai-coding/glm-5.1");
  const [task, setTask] = useState<Task | null>(null);
  const [loading, setLoading] = useState(false);

  async function run() {
    if (!prompt.trim() || loading) return;
    setLoading(true);
    setTask(null);
    try {
      const res = await fetch(`${API_BASE_URL}/code`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ repo_dir: repoDir, prompt, model }),
      });
      const r = await res.json();
      const tid = r.data?.task_id;
      if (!tid) {
        setTask({ id: "", status: "failed", output: r.message ?? "提交失败", change_id: "", prompt, model });
        setLoading(false);
        return;
      }
      setTask({ id: tid, status: "running", output: "", change_id: "", prompt, model });
      const iv = setInterval(async () => {
        const rr = await fetch(`${API_BASE_URL}/code-tasks/${tid}`).then((x) => x.json());
        setTask(rr.data);
        if (rr.data?.status === "completed" || rr.data?.status === "failed") {
          clearInterval(iv);
          setLoading(false);
        }
      }, 3000);
    } catch (e) {
      setTask({ id: "", status: "failed", output: String(e), change_id: "", prompt, model });
      setLoading(false);
    }
  }

  return (
    <div>
      <h1 className="mb-1 text-xl font-bold">研发工作台</h1>
      <FlowStepper current={1} />
      <p className="mb-4 text-sm text-neutral-600">
        异步编码引擎：opencode + 智谱 GLM-5.1（提交即返回，后台执行，自动登记变更待🚪G3 审批）
      </p>

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
      <textarea value={prompt} onChange={(e) => setPrompt(e.target.value)} rows={3} placeholder="例：创建 hello.py 打印 hello world" className="mt-1 w-full rounded-md border border-neutral-300 px-2 py-1.5 text-sm" />
      <button onClick={run} disabled={loading} className="mt-2 rounded-md bg-blue-600 px-4 py-2 text-sm text-white disabled:opacity-50">
        {loading ? "编码中（轮询进度）…" : "派发编码任务"}
      </button>

      {task && (
        <div className="mt-4 rounded-lg border border-neutral-200 bg-white p-4">
          <div className="flex items-center gap-2">
            <span className={`rounded px-2 py-0.5 text-xs ${STATUS_COLOR[task.status] ?? "bg-neutral-100"}`}>{task.status}</span>
            {task.id && <span className="font-mono text-xs text-neutral-500">{task.id}</span>}
            {task.change_id && <span className="text-xs text-neutral-400">→ 变更 {task.change_id}（待🚪G3 审批）</span>}
            {task.status === "completed" && task.change_id && (
              <span className="text-xs text-emerald-700">✓ 下一步：去「🚪 变更审批」批准变更 {task.change_id}</span>
            )}
          </div>
          {task.output && (
            <pre className="mt-2 max-h-64 overflow-auto rounded bg-neutral-900 p-3 text-xs text-green-300">{task.output}</pre>
          )}
        </div>
      )}
    </div>
  );
}

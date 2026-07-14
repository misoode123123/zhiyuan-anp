"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { API_BASE_URL } from "@/lib/api";

type Envelope<T> = { code: number; data: T; message?: string };
type ProjectSpace = { id: string; name: string; slug: string };
type Requirement = {
  id: string;
  title: string;
  description: string;
  user_story: string;
  acceptance_criteria: string;
  status: string;
};

const STEPS = ["需求", "编码", "审批", "发布"];

export default function RequirementsPage() {
  const [spaces, setSpaces] = useState<ProjectSpace[]>([]);
  const [psID, setPsID] = useState("");
  const [desc, setDesc] = useState("");
  const [images, setImages] = useState<string[]>([]);
  const [last, setLast] = useState<Requirement | null>(null);
  const [list, setList] = useState<Requirement[]>([]);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState("");
  const [msg, setMsg] = useState("");
  const [repoDir, setRepoDir] = useState("D:/Projects/智源-ANP平台/pilots/oc-pilot");
  const [dispatching, setDispatching] = useState("");

  useEffect(() => {
    fetch(`${API_BASE_URL}/project-spaces`)
      .then((r) => r.json())
      .then((r: Envelope<ProjectSpace[]>) => {
        setSpaces(r.data ?? []);
        if (r.data?.[0]) setPsID(r.data[0].id);
      });
  }, []);

  const loadList = (id: string) => {
    if (!id) return;
    fetch(`${API_BASE_URL}/project-spaces/${id}/requirements`)
      .then((r) => r.json())
      .then((r: Envelope<Requirement[]>) => setList(r.data ?? []))
      .catch(() => {});
  };
  useEffect(() => {
    loadList(psID);
  }, [psID]);

  function onFiles(files: FileList | null) {
    if (!files) return;
    Array.from(files).slice(0, 4).forEach((f) => {
      const reader = new FileReader();
      reader.onload = () => setImages((p) => [...p, reader.result as string]);
      reader.readAsDataURL(f);
    });
  }

  async function generate() {
    if (!desc.trim() || !psID || loading) return;
    setLoading(true);
    setErr("");
    try {
      const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/requirements`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ description: desc, images }),
      });
      const r = await res.json();
      if (r.data) {
        setLast(r.data);
        setDesc("");
        setImages([]);
        loadList(psID);
        setMsg("✅ 需求已生成。下一步：点「⚡ 派发编码」让 AI 实现");
      } else setErr(r.message ?? "生成失败");
    } catch (e) {
      setErr(String(e));
    } finally {
      setLoading(false);
    }
  }

  async function dispatch(rid: string) {
    if (!psID || !repoDir || dispatching) return;
    setDispatching(rid);
    setMsg("");
    try {
      const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/requirements/${rid}/dispatch-code`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ repo_dir: repoDir }),
      });
      const r = await res.json();
      if (r.data?.task_id) {
        setMsg(`⚡ 已派发编码（任务 ${r.data.task_id}）。AI 后台实现中 → 完成后请去「🚪 变更审批」查看产出并审批`);
      } else {
        setMsg(`✗ ${r.message ?? "派发失败"}`);
      }
    } catch (e) {
      setMsg(`✗ ${e}`);
    } finally {
      setDispatching("");
    }
  }

  let ac: string[] = [];
  try {
    ac = last ? JSON.parse(last.acceptance_criteria) : [];
  } catch {
    ac = [];
  }

  return (
    <div>
      <h1 className="mb-1 text-xl font-bold">需求工作台</h1>
      <p className="mb-3 text-sm text-neutral-600">业务描述 + 截图（可选）→ AI 生成规格 → 派发编码 → 审批 → 发布</p>

      <div className="mb-4">
        <Link href="/requirements/chat" className="rounded-md bg-blue-600 px-3 py-1.5 text-sm text-white">💬 对话式梳理需求（AI 引导，推荐）</Link>
      </div>

      {/* 流程引导 */}
      <div className="mb-4 flex items-center gap-1 text-xs">
        {STEPS.map((s, i) => (
          <div key={s} className="flex items-center gap-1">
            <span className={`rounded-full px-2 py-1 ${i === 0 ? "bg-blue-600 text-white" : "bg-neutral-200 text-neutral-600"}`}>{i + 1}. {s}</span>
            {i < STEPS.length - 1 && <span className="text-neutral-400">→</span>}
          </div>
        ))}
        <span className="ml-2 text-neutral-400">您在此</span>
      </div>

      <div className="mb-3">
        <label className="text-xs text-neutral-500">项目空间</label>
        <select value={psID} onChange={(e) => setPsID(e.target.value)} className="ml-2 rounded-md border border-neutral-300 px-2 py-1 text-sm">
          {spaces.map((s) => (<option key={s.id} value={s.id}>{s.name} ({s.slug})</option>))}
        </select>
      </div>

      <label className="text-xs text-neutral-500">业务描述</label>
      <textarea value={desc} onChange={(e) => setDesc(e.target.value)} rows={3} placeholder="例：客服系统登录界面，支持账号密码和短信验证码登录" className="mt-1 w-full rounded-md border border-neutral-300 px-2 py-1.5 text-sm" />

      <div className="mt-2">
        <label className="text-xs text-neutral-500">附件截图（可选，多模态，最多 4 张）</label>
        <input type="file" accept="image/*" multiple onChange={(e) => onFiles(e.target.files)} className="mt-1 block text-sm" />
        {images.length > 0 && (
          <div className="mt-2 flex gap-2">{images.map((img, i) => (<img key={i} src={img} alt="" className="h-16 rounded border" />))}</div>
        )}
      </div>

      <button onClick={generate} disabled={loading || !psID} className="mt-2 rounded-md bg-blue-600 px-4 py-2 text-sm text-white disabled:opacity-50">
        {loading ? "AI 生成规格中…" : "① 生成需求规格"}
      </button>
      {err && <div className="mt-2 text-sm text-red-500">{err}</div>}
      {msg && <div className="mt-2 rounded-md bg-blue-50 p-2 text-sm text-blue-800">{msg}</div>}

      {last && (
        <div className="mt-4 rounded-lg border border-blue-200 bg-blue-50 p-4">
          <div className="text-xs text-neutral-500">最新生成 · {last.id}</div>
          <div className="text-base font-semibold">{last.title}</div>
          <div className="mt-2 text-sm"><b>用户故事：</b>{last.user_story}</div>
          <div className="mt-2 text-sm"><b>验收标准：</b><ul className="ml-5 list-disc">{ac.map((c, i) => (<li key={i}>{c}</li>))}</ul></div>
          <div className="mt-3 border-t border-blue-200 pt-3">
            <div className="mb-1 text-xs text-neutral-500">下一步：派发给 AI 编码</div>
            <input value={repoDir} onChange={(e) => setRepoDir(e.target.value)} placeholder="目标仓库路径" className="mb-2 w-full rounded border border-neutral-300 px-2 py-1 text-sm" />
            <button onClick={() => dispatch(last.id)} disabled={!!dispatching} className="rounded-md bg-emerald-600 px-3 py-1.5 text-sm text-white disabled:opacity-50">
              {dispatching === last.id ? "派发中…" : "⚡ ② 派发编码"}
            </button>
          </div>
        </div>
      )}

      <div className="mt-6">
        <div className="mb-2 text-sm font-semibold">需求列表（{list.length}）— 每项可派发编码</div>
        <div className="space-y-2">
          {list.map((r) => (
            <div key={r.id} className="rounded-md border border-neutral-200 bg-white p-3 text-sm">
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                  <span className="font-medium">{r.title}</span>
                  <span className={`rounded px-1.5 py-0.5 text-xs ${r.status === "delivered" ? "bg-emerald-100 text-emerald-700" : r.status === "specified" ? "bg-blue-100 text-blue-700" : "bg-neutral-100 text-neutral-600"}`}>
                    {r.status === "delivered" ? "✅ 已交付" : r.status === "specified" ? "已生成" : r.status}
                  </span>
                </div>
                <button onClick={() => dispatch(r.id)} disabled={!!dispatching} className="rounded bg-emerald-600 px-2 py-1 text-xs text-white disabled:opacity-50">
                  {dispatching === r.id ? "编码中…" : "⚡ 派发编码"}
                </button>
              </div>
              <div className="mt-1 text-xs text-neutral-500">{r.user_story}</div>
            </div>
          ))}
          {list.length === 0 && <div className="text-sm text-neutral-400">暂无需求</div>}
        </div>
      </div>
    </div>
  );
}

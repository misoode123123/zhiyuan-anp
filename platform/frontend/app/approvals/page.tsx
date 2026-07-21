"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL } from "@/lib/api";
import { FlowStepper } from "../_components/stepper";
import { ChangeOutput } from "../_components/change-output";

type Envelope<T> = { code: number; data: T; message?: string };
type Change = {
  id: string;
  kind: string;
  source_id: string;
  repo_dir: string;
  prompt: string;
  model: string;
  output: string;
  status: string;
  reviewer: string;
  created_at: string;
  reviewed_at: string;
  app_name: string;
};

const STATUS_COLOR: Record<string, string> = {
  pending: "bg-amber-100 text-amber-700",
  approved: "bg-emerald-100 text-emerald-700",
  rejected: "bg-red-100 text-red-700",
};

export default function ApprovalsPage() {
  const [list, setList] = useState<Change[]>([]);
  const [filter, setFilter] = useState("pending");
  const [msg, setMsg] = useState("");

  const load = () =>
    fetch(`${API_BASE_URL}/changes?status=${filter}`)
      .then((r) => r.json())
      .then((r: Envelope<Change[]>) => setList(r.data ?? []));
  useEffect(() => {
    load();
  }, [filter]);

  async function decide(id: string, d: "approve" | "reject") {
    const res = await fetch(`${API_BASE_URL}/changes/${id}/${d}`, { method: "POST" });
    const r = await res.json();
    setMsg(
      (r.message ?? "") +
        (d === "approve" ? "  → 下一步：去「🚀 发布中心」发布上线" : "  → 需回滚或重做")
    );
    load();
  }

  return (
    <div>
      <h1 className="mb-1 text-xl font-bold">🚪 变更审批（G3 代码闸门）</h1>
      <FlowStepper current={2} />
      <p className="mb-4 text-sm text-neutral-600">
        AI 编码产出登记为待审批变更，人工批准/拒绝后才算合入（关键节点人决策）。
      </p>

      <div className="mb-3 flex gap-2">
        {["pending", "approved", "rejected", ""].map((s) => (
          <button
            key={s || "all"}
            onClick={() => setFilter(s)}
            className={`rounded-md px-3 py-1 text-sm ${filter === s ? "bg-blue-600 text-white" : "bg-neutral-100"}`}
          >
            {s === "" ? "全部" : s === "pending" ? "待审" : s === "approved" ? "已批准" : "已拒绝"}
          </button>
        ))}
      </div>
      {msg && <div className="mb-3 text-sm text-blue-700">{msg}</div>}

      <div className="space-y-2">
        {list.map((c) => (
          <div key={c.id} className="rounded-md border border-neutral-200 bg-white p-3 text-sm">
            <div className="flex items-center justify-between">
              <div className="flex min-w-0 items-center gap-2">
                <span className="truncate font-medium text-neutral-800">
                  {c.app_name || c.id.slice(0, 12)}
                </span>
                <span className="font-mono text-[10px] text-neutral-400">{c.id.slice(0, 12)}</span>
                <span
                  className={`rounded px-1.5 py-0.5 text-xs ${STATUS_COLOR[c.status] ?? "bg-neutral-100"}`}
                >
                  {c.status}
                </span>
                <span className="text-xs text-neutral-400">
                  {c.kind} · {c.model}
                </span>
              </div>
              {c.status === "pending" && (
                <div className="flex gap-2">
                  <button
                    onClick={() => decide(c.id, "approve")}
                    className="rounded bg-emerald-600 px-2 py-1 text-xs text-white"
                  >
                    批准
                  </button>
                  <button
                    onClick={() => decide(c.id, "reject")}
                    className="rounded bg-red-600 px-2 py-1 text-xs text-white"
                  >
                    拒绝
                  </button>
                </div>
              )}
            </div>
            <div className="mt-1 text-neutral-700">{c.prompt}</div>
            <div className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-xs text-neutral-400">
              {c.created_at && (
                <span>
                  📅 创建 {new Date(c.created_at).toLocaleString("zh-CN", { hour12: false })}
                </span>
              )}
              {c.reviewed_at && (
                <span>
                  ✅ 审批 {new Date(c.reviewed_at).toLocaleString("zh-CN", { hour12: false })}
                </span>
              )}
              <span className="truncate">📁 {c.repo_dir}</span>
            </div>
            {c.output && <ChangeOutput output={c.output} />}
          </div>
        ))}
        {list.length === 0 && <div className="text-sm text-neutral-400">暂无变更</div>}
      </div>
    </div>
  );
}

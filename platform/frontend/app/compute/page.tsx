"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL } from "@/lib/api";

type Envelope<T> = { code: number; data: T };
type PS = { id: string; name: string; slug: string };
type Stats = {
  total_tokens: number;
  total_calls: number;
  by_model: { model: string; tokens: number; calls: number }[];
};
type Usage = {
  id: string;
  model: string;
  kind: string;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  created_at: string;
};

export default function ComputePage() {
  const [spaces, setSpaces] = useState<PS[]>([]);
  const [psID, setPsID] = useState("");
  const [stats, setStats] = useState<Stats | null>(null);
  const [list, setList] = useState<Usage[]>([]);

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
    fetch(`${API_BASE_URL}/project-spaces/${id}/usage/stats`)
      .then((r) => r.json())
      .then((r: Envelope<Stats>) => setStats(r.data ?? null));
    fetch(`${API_BASE_URL}/project-spaces/${id}/usage`)
      .then((r) => r.json())
      .then((r: Envelope<Usage[]>) => setList(r.data ?? []));
  };
  useEffect(() => {
    load(psID);
  }, [psID]);

  const maxTokens = Math.max(1, ...(stats?.by_model ?? []).map((m) => m.tokens));

  return (
    <div>
      <h1 className="mb-1 text-xl font-bold">⚡ 算力资源中心</h1>
      <p className="mb-4 text-sm text-neutral-600">
        AI 用量 / Token / 成本看板（企业"只投入算力"的落点）。
      </p>

      <div className="mb-4">
        <label className="text-xs text-neutral-500">项目空间</label>
        <select
          value={psID}
          onChange={(e) => setPsID(e.target.value)}
          className="ml-2 rounded-md border border-neutral-300 px-2 py-1 text-sm"
        >
          {spaces.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name} ({s.slug})
            </option>
          ))}
        </select>
      </div>

      <div className="mb-6 grid grid-cols-2 gap-4">
        <div className="rounded-lg border border-neutral-200 bg-white p-4">
          <div className="text-sm text-neutral-500">总 Token</div>
          <div className="text-2xl font-bold">{stats?.total_tokens ?? 0}</div>
        </div>
        <div className="rounded-lg border border-neutral-200 bg-white p-4">
          <div className="text-sm text-neutral-500">AI 调用次数</div>
          <div className="text-2xl font-bold">{stats?.total_calls ?? 0}</div>
        </div>
      </div>

      <div className="mb-6">
        <div className="mb-2 text-sm font-semibold">按模型分布</div>
        <div className="space-y-2">
          {(stats?.by_model ?? []).map((m) => (
            <div key={m.model} className="flex items-center gap-2 text-sm">
              <div className="w-40 font-mono text-xs">{m.model}</div>
              <div className="flex-1">
                <div className="h-4 rounded bg-neutral-100">
                  <div
                    className="h-4 rounded bg-blue-500"
                    style={{ width: `${(m.tokens / maxTokens) * 100}%` }}
                  />
                </div>
              </div>
              <div className="w-32 text-right text-xs text-neutral-500">
                {m.tokens} tok / {m.calls} 次
              </div>
            </div>
          ))}
          {(stats?.by_model ?? []).length === 0 && (
            <div className="text-sm text-neutral-400">暂无用量（生成需求/测试用例后产生）</div>
          )}
        </div>
      </div>

      <div>
        <div className="mb-2 text-sm font-semibold">用量明细（最近）</div>
        <div className="space-y-1">
          {list.map((u) => (
            <div
              key={u.id}
              className="flex items-center gap-3 rounded-md border border-neutral-200 bg-white p-2 text-xs"
            >
              <span className="font-mono text-neutral-500">{u.kind}</span>
              <span className="text-neutral-700">{u.model}</span>
              <span className="text-neutral-400">
                prompt {u.prompt_tokens} / completion {u.completion_tokens}
              </span>
              <span className="ml-auto font-medium">{u.total_tokens} tok</span>
            </div>
          ))}
          {list.length === 0 && <div className="text-sm text-neutral-400">暂无明细</div>}
        </div>
      </div>
    </div>
  );
}

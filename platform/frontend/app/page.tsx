"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL, currentProjectSpace } from "@/lib/api";

type Envelope<T> = { code: number; data: T };
type PS = { id: string; name: string; slug: string };
type Overview = {
  space: PS;
  members: number; apps: number; deployed_apps: number;
  requirements: number; changes: number; releases: number;
};

export default function Home() {
  const [spaces, setSpaces] = useState<PS[]>([]);
  const [psID, setPsID] = useState("");
  const [ov, setOv] = useState<Overview | null>(null);

  useEffect(() => {
    fetch(`${API_BASE_URL}/project-spaces`).then((r) => r.json()).then((r: Envelope<PS[]>) => {
      setSpaces(r.data ?? []);
      const cur = currentProjectSpace();
      const def = (r.data ?? []).find((s) => s.id === cur) ?? (r.data ?? []).find((s) => s.id === "ps_default") ?? (r.data ?? [])[0];
      if (def) setPsID(def.id);
    });
  }, []);

  useEffect(() => {
    if (!psID) return;
    fetch(`${API_BASE_URL}/project-spaces/${psID}/overview`).then((r) => r.json()).then((r: Envelope<Overview>) => setOv(r.data ?? null));
  }, [psID]);

  return (
    <div>
      <h1 className="mb-1 text-2xl font-bold">智源 ANP 平台概览</h1>
      <p className="mb-4 text-neutral-600">企业 AI 原生研发平台 · 空间即工作上下文</p>

      <div className="mb-4 flex items-center gap-2">
        <label className="text-xs text-neutral-500">项目空间</label>
        <select value={psID} onChange={(e) => setPsID(e.target.value)} className="rounded-md border border-neutral-300 px-2 py-1 text-sm">
          {spaces.map((s) => (<option key={s.id} value={s.id}>{s.name} ({s.slug})</option>))}
        </select>
        {ov && <span className="text-sm text-neutral-500">· {ov.space.name}</span>}
      </div>

      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
        <Stat label="成员" value={ov?.members} />
        <Stat label="应用" value={ov?.apps} hint={ov ? `运行中 ${ov.deployed_apps}` : undefined} />
        <Stat label="需求" value={ov?.requirements} />
        <Stat label="变更" value={ov?.changes} />
        <Stat label="发布" value={ov?.releases} />
        <Stat label="项目空间" value={spaces.length} />
      </div>

      <div className="mt-6 grid grid-cols-1 gap-3 sm:grid-cols-3">
        <a href="/requirements" className="rounded-lg border border-neutral-200 bg-white p-4 hover:border-blue-300">
          <div className="text-sm text-neutral-500">需求 → 上线</div>
          <div className="mt-1 text-lg font-semibold">AI 驱动全流程</div>
        </a>
        <a href="/applications" className="rounded-lg border border-neutral-200 bg-white p-4 hover:border-blue-300">
          <div className="text-sm text-neutral-500">应用 = 托管仓库</div>
          <div className="mt-1 text-lg font-semibold">构建部署自动</div>
        </a>
        <a href="/admin/users" className="rounded-lg border border-neutral-200 bg-white p-4 hover:border-blue-300">
          <div className="text-sm text-neutral-500">用户 × 空间 × 角色</div>
          <div className="mt-1 text-lg font-semibold">RBAC 多租户</div>
        </a>
      </div>
    </div>
  );
}

function Stat({ label, value, hint }: { label: string; value?: number; hint?: string }) {
  return (
    <div className="rounded-lg border border-neutral-200 bg-white p-3">
      <div className="text-xs text-neutral-500">{label}</div>
      <div className="text-2xl font-bold">{value ?? "—"}</div>
      {hint && <div className="text-[11px] text-neutral-400">{hint}</div>}
    </div>
  );
}

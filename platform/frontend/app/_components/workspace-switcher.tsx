"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL, apiGet, currentProjectSpace, setCurrentProjectSpace } from "@/lib/api";

type ProjectSpace = { id: string; name: string; slug: string };
type Envelope<T> = { code: number; message: string; data: T };

export function WorkspaceSwitcher() {
  const [spaces, setSpaces] = useState<ProjectSpace[]>([]);
  const [current, setCurrent] = useState("");
  const [error, setError] = useState(false);
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState("");
  const [newSlug, setNewSlug] = useState("");

  const load = () =>
    apiGet<Envelope<ProjectSpace[]>>("/project-spaces")
      .then((r) => setSpaces(r.data ?? []))
      .catch(() => setError(true));

  useEffect(() => {
    setCurrent(currentProjectSpace());
    load();
  }, []);

  function pick(id: string) {
    setCurrent(id);
    setCurrentProjectSpace(id); // 持久化，供全局 fetch 拦截器带 X-Project-Space-Id
  }

  async function create() {
    if (!newName.trim() || !newSlug.trim()) return;
    const res = await fetch(`${API_BASE_URL}/project-spaces`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: newName, slug: newSlug }),
    });
    const r = await res.json();
    if (r.code === 0) {
      setCreating(false);
      setNewName("");
      setNewSlug("");
      load();
    }
  }

  return (
    <div>
      <div className="mb-1 flex items-center justify-between">
        <span className="text-xs text-neutral-500">项目空间</span>
        <button
          onClick={() => setCreating(!creating)}
          className="text-xs text-blue-600 hover:underline"
          title="新建项目空间（浏览器创建为 UTF-8，避免乱码）"
        >
          {creating ? "取消" : "＋ 新建"}
        </button>
      </div>

      {creating && (
        <div className="mb-2 space-y-1 rounded-md bg-neutral-50 p-2">
          <input
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            placeholder="名称（如：客服系统）"
            className="w-full rounded border border-neutral-300 px-2 py-1 text-sm"
          />
          <input
            value={newSlug}
            onChange={(e) => setNewSlug(e.target.value)}
            placeholder="英文标识（如：cs）"
            className="w-full rounded border border-neutral-300 px-2 py-1 text-sm"
          />
          <button
            onClick={create}
            className="w-full rounded bg-blue-600 py-1 text-xs text-white"
          >
            创建
          </button>
        </div>
      )}

      <select
        value={current}
        onChange={(e) => pick(e.target.value)}
        className="w-full rounded-md border border-neutral-300 px-2 py-1.5 text-sm"
      >
        <option value="">— 选择 —</option>
        {spaces.map((s) => (
          <option key={s.id} value={s.id}>
            {s.name} ({s.slug})
          </option>
        ))}
      </select>
      {error && <div className="mt-1 text-xs text-red-500">后端未连接 (:8080)</div>}
    </div>
  );
}

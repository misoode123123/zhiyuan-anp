"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL } from "@/lib/api";

type Envelope<T> = { code: number; data: T };
type ConfigItem = {
  key: string;
  value: string;
  category: string;
  description: string;
};

export default function ConfigPage() {
  const [items, setItems] = useState<ConfigItem[]>([]);
  const [draft, setDraft] = useState<Record<string, string>>({});
  const [msg, setMsg] = useState("");

  const load = () => {
    fetch(`${API_BASE_URL}/config`)
      .then((r) => r.json())
      .then((r: Envelope<ConfigItem[]>) => setItems(r.data ?? []));
  };
  useEffect(() => {
    load();
  }, []);

  async function save(key: string) {
    const value = draft[key];
    if (value === undefined) return;
    const res = await fetch(`${API_BASE_URL}/config/${key}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ value }),
    });
    const r = await res.json();
    setMsg(r.code === 0 ? `✓ ${key} 已保存（热生效，无需重启）` : `✗ ${r.message}`);
    setDraft((d) => ({ ...d, [key]: undefined }));
    load();
  }

  const grouped = items.reduce((acc, it) => {
    (acc[it.category] ||= []).push(it);
    return acc;
  }, {} as Record<string, ConfigItem[]>);

  const isSecret = (k: string) => k.includes("key") || k.includes("secret");

  return (
    <div>
      <h1 className="mb-1 text-xl font-bold">⚙️ 系统配置</h1>
      <p className="mb-4 text-sm text-neutral-600">
        业务配置入库管理（基础运行配置仍用 .env）。修改<strong>热生效</strong>，无需改文件/重启。
      </p>
      {msg && <div className="mb-3 text-sm text-blue-700">{msg}</div>}

      {Object.entries(grouped).map(([cat, list]) => (
        <div key={cat} className="mb-6">
          <div className="mb-2 text-sm font-semibold text-neutral-700">{cat}</div>
          <div className="space-y-2">
            {list.map((it) => (
              <div key={it.key} className="flex items-center gap-2 rounded-md border border-neutral-200 bg-white p-2">
                <div className="w-56 shrink-0">
                  <div className="font-mono text-xs text-neutral-700">{it.key}</div>
                  <div className="text-xs text-neutral-400">{it.description || "—"}</div>
                </div>
                <input
                  type={isSecret(it.key) ? "password" : "text"}
                  defaultValue={it.value}
                  onChange={(e) => setDraft((d) => ({ ...d, [it.key]: e.target.value }))}
                  className="flex-1 rounded-md border border-neutral-300 px-2 py-1 text-sm"
                />
                <button
                  onClick={() => save(it.key)}
                  disabled={draft[it.key] === undefined}
                  className="rounded bg-blue-600 px-3 py-1 text-xs text-white disabled:opacity-40"
                >
                  保存
                </button>
              </div>
            ))}
          </div>
        </div>
      ))}
      {items.length === 0 && <div className="text-sm text-neutral-400">暂无配置</div>}
    </div>
  );
}

"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL, currentProjectSpace } from "@/lib/api";

type Envelope<T> = { code: number; message?: string; data: T };
type ProjectSpace = { id: string; name: string; slug: string };
type Quota = {
  project_space_id: string;
  max_apps: number;
  max_databases: number;
  max_total_db_mb: number;
  max_capability_calls_per_day: number;
  updated_at?: string;
};
type Usage = {
  quota: Quota;
  used_apps: number;
  used_databases: number;
  used_db_size_mb: number;
  used_capability_today: number;
};

// 维度元数据（label / 单位 / 颜色阈值）
type Dim = {
  key: keyof Pick<
    Quota,
    "max_apps" | "max_databases" | "max_total_db_mb" | "max_capability_calls_per_day"
  >;
  label: string;
  unit: string;
  icon: string;
  usedKey: keyof Pick<
    Usage,
    "used_apps" | "used_databases" | "used_db_size_mb" | "used_capability_today"
  >;
};

const DIMENSIONS: Dim[] = [
  { key: "max_apps", label: "应用数", unit: "", icon: "📦", usedKey: "used_apps" },
  { key: "max_databases", label: "数据库数", unit: "", icon: "🗄️", usedKey: "used_databases" },
  { key: "max_total_db_mb", label: "库总大小", unit: "MB", icon: "💾", usedKey: "used_db_size_mb" },
  {
    key: "max_capability_calls_per_day",
    label: "今日 AI 调用",
    unit: "次",
    icon: "🤖",
    usedKey: "used_capability_today",
  },
];

export default function QuotaPage() {
  const [spaces, setSpaces] = useState<ProjectSpace[]>([]);
  const [psID, setPsID] = useState("");
  const [usage, setUsage] = useState<Usage | null>(null);
  const [form, setForm] = useState({
    max_apps: 0,
    max_databases: 0,
    max_total_db_mb: 0,
    max_capability_calls_per_day: 0,
  });
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");

  // 1. 拉项目空间列表，默认选 currentProjectSpace()
  useEffect(() => {
    fetch(`${API_BASE_URL}/project-spaces`)
      .then((r) => r.json())
      .then((r: Envelope<ProjectSpace[]>) => {
        const list = r.data ?? [];
        setSpaces(list);
        const saved = currentProjectSpace();
        const def =
          list.find((s) => s.id === saved) ?? list.find((s) => s.id === "ps_default") ?? list[0];
        if (def) setPsID(def.id);
      })
      .catch(() => setErr("拉项目空间失败（后端 :8080 未连接？）"));
  }, []);

  // 2. 拉配额 + 用量
  const load = (id: string) => {
    if (!id) return;
    fetch(`${API_BASE_URL}/project-spaces/${id}/quota`)
      .then((r) => r.json())
      .then((r: Envelope<Usage>) => {
        if (r.code !== 0 || !r.data) {
          setErr(r.message ?? "拉配额失败");
          return;
        }
        setUsage(r.data);
        setForm({
          max_apps: r.data.quota.max_apps,
          max_databases: r.data.quota.max_databases,
          max_total_db_mb: r.data.quota.max_total_db_mb,
          max_capability_calls_per_day: r.data.quota.max_capability_calls_per_day,
        });
        setErr("");
      })
      .catch(() => setErr("拉配额失败"));
  };
  useEffect(() => {
    if (psID) load(psID);
  }, [psID]);

  // 3. 保存（PUT 只传改过的字段——这里全传，后端用 *int 区分）
  async function save() {
    if (!psID) return;
    setMsg("");
    setErr("");
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/quota`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(form),
    });
    const r = (await res.json()) as Envelope<Quota>;
    if (r.code === 0) {
      setMsg("✓ 配额已更新");
      load(psID);
    } else {
      setErr(`✗ ${r.message ?? "保存失败"}`);
    }
  }

  // 进度条颜色：>= 100 红，>= 80% 橙，否则蓝
  function barClass(ratio: number): string {
    if (ratio >= 1) return "bg-red-500";
    if (ratio >= 0.8) return "bg-amber-500";
    return "bg-blue-500";
  }

  return (
    <div>
      <h1 className="mb-1 text-xl font-bold">📊 项目配额</h1>
      <p className="mb-4 text-sm text-neutral-600">
        4 维度强制：<b>应用数 / 库数 / 库总大小 / 每日 AI 调用</b>。建资源前
        check，超限返回友好错误。
      </p>

      {/* 项目空间选择器 */}
      <div className="mb-4 flex items-center gap-2">
        <span className="text-sm text-neutral-500">项目空间：</span>
        <select
          value={psID}
          onChange={(e) => setPsID(e.target.value)}
          className="rounded-md border border-neutral-300 px-2 py-1.5 text-sm"
        >
          <option value="">— 选择 —</option>
          {spaces.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name} ({s.slug})
            </option>
          ))}
        </select>
        {usage?.quota.updated_at && (
          <span className="ml-auto text-xs text-neutral-400">
            更新于 {new Date(usage.quota.updated_at).toLocaleString()}
          </span>
        )}
      </div>

      {err && (
        <div className="mb-3 rounded-md border border-red-200 bg-red-50 p-2 text-sm text-red-700">
          {err}
        </div>
      )}
      {msg && (
        <div className="mb-3 rounded-md border border-emerald-200 bg-emerald-50 p-2 text-sm text-emerald-700">
          {msg}
        </div>
      )}

      {!psID && <div className="text-sm text-neutral-400">请先选择项目空间</div>}

      {psID && usage && (
        <>
          {/* 4 个用量卡片 */}
          <div className="mb-6 grid grid-cols-1 gap-3 md:grid-cols-2">
            {DIMENSIONS.map((d) => {
              const limit = usage.quota[d.key];
              const used = usage[d.usedKey];
              const ratio = limit > 0 ? used / limit : used > 0 ? 1 : 0;
              return (
                <div key={d.key} className="rounded-lg border border-neutral-200 bg-white p-4">
                  <div className="mb-2 flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      <span className="text-lg">{d.icon}</span>
                      <span className="font-medium">{d.label}</span>
                    </div>
                    <span
                      className={`rounded px-2 py-0.5 text-xs ${
                        ratio >= 1
                          ? "bg-red-100 text-red-700"
                          : ratio >= 0.8
                            ? "bg-amber-100 text-amber-700"
                            : "bg-blue-100 text-blue-700"
                      }`}
                    >
                      {used}
                      {d.unit} / {limit}
                      {d.unit}
                    </span>
                  </div>
                  <div className="h-2 w-full overflow-hidden rounded-full bg-neutral-100">
                    <div
                      className={`h-full ${barClass(ratio)}`}
                      style={{ width: `${Math.min(ratio * 100, 100)}%` }}
                    />
                  </div>
                  <div className="mt-1 text-xs text-neutral-400">
                    {ratio >= 1
                      ? "已满：建新资源会被拦截"
                      : ratio >= 0.8
                        ? "接近上限"
                        : `剩余 ${limit - used}${d.unit}`}
                  </div>
                </div>
              );
            })}
          </div>

          {/* 编辑表单 */}
          <div className="rounded-lg border border-neutral-200 bg-white p-4">
            <div className="mb-3 text-sm font-semibold text-neutral-700">调整上限（admin）</div>
            <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
              {DIMENSIONS.map((d) => (
                <label key={d.key} className="flex items-center gap-2 text-sm">
                  <span className="w-32 shrink-0 text-neutral-600">
                    {d.icon} {d.label}上限
                  </span>
                  <input
                    type="number"
                    min={0}
                    value={form[d.key]}
                    onChange={(e) => setForm({ ...form, [d.key]: Number(e.target.value) })}
                    className="w-28 rounded-md border border-neutral-300 px-2 py-1"
                  />
                  <span className="text-xs text-neutral-400">{d.unit || "个"}</span>
                </label>
              ))}
            </div>
            <div className="mt-3 flex items-center gap-2">
              <button
                onClick={save}
                className="rounded-md bg-blue-600 px-4 py-1.5 text-sm text-white hover:bg-blue-700"
              >
                保存
              </button>
              <button
                onClick={() => psID && load(psID)}
                className="rounded-md bg-neutral-200 px-3 py-1.5 text-sm text-neutral-700 hover:bg-neutral-300"
              >
                重载
              </button>
              <span className="text-xs text-neutral-400">
                设为 0 表示完全禁用（任何已用都超限）
              </span>
            </div>
          </div>
        </>
      )}
    </div>
  );
}

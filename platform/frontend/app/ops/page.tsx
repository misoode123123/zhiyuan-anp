"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL } from "@/lib/api";

type Envelope<T> = { code: number; data: T };
type PS = { id: string; name: string; slug: string };

type ComponentHealth = { name: string; status: string; detail: string; latency_ms?: number };
type Stats = {
  requirements: Record<string, number>;
  code_tasks: Record<string, number>;
  changes: Record<string, number>;
  releases: number;
  active_alerts: number;
  active_sops: number;
};
type Dashboard = {
  overall_health: string;
  components: ComponentHealth[];
  stats: Stats;
  usage: { total_tokens: number; total_calls: number };
  activity: { time: string; kind: string; action: string; title: string; ref_id: string }[];
  open_alerts: number;
};
type Alert = {
  id: string;
  source: string;
  severity: string;
  status: string;
  title: string;
  description: string;
  fired_at: string;
};
type SOP = {
  id: string;
  code: string;
  name: string;
  category: string;
  risk_level: string;
  status: string;
  steps: string;
  requires_approval: boolean;
};

const HEALTH_COLOR: Record<string, string> = {
  healthy: "bg-emerald-100 text-emerald-700",
  degraded: "bg-amber-100 text-amber-700",
  down: "bg-red-100 text-red-700",
};
const SEV_COLOR: Record<string, string> = {
  critical: "bg-red-100 text-red-700",
  warning: "bg-amber-100 text-amber-700",
  info: "bg-blue-100 text-blue-700",
};
const HEALTH_ICON: Record<string, string> = { healthy: "✅", degraded: "⚠️", down: "❌" };

export default function OpsPage() {
  const [spaces, setSpaces] = useState<PS[]>([]);
  const [psID, setPsID] = useState("");
  const [dash, setDash] = useState<Dashboard | null>(null);
  const [alerts, setAlerts] = useState<Alert[]>([]);
  const [sops, setSops] = useState<SOP[]>([]);
  const [busy, setBusy] = useState(false);
  const [showAlertForm, setShowAlertForm] = useState(false);
  const [showSopForm, setShowSopForm] = useState(false);
  const [alertForm, setAlertForm] = useState({ severity: "warning", title: "", description: "" });
  const [sopForm, setSopForm] = useState({
    code: "",
    name: "",
    category: "restart",
    risk_level: "low",
    steps: "",
    status: "draft",
  });

  useEffect(() => {
    fetch(`${API_BASE_URL}/project-spaces`)
      .then((r) => r.json())
      .then((r: Envelope<PS[]>) => {
        setSpaces(r.data ?? []);
        const list = r.data ?? [];
        const def = list.find((s) => s.id === "ps_default") ?? list[0];
        if (def) setPsID(def.id);
      });
  }, []);

  const load = (id: string) => {
    if (!id) return;
    fetch(`${API_BASE_URL}/project-spaces/${id}/ops/dashboard`)
      .then((r) => r.json())
      .then((r: Envelope<Dashboard>) => setDash(r.data ?? null));
    fetch(`${API_BASE_URL}/project-spaces/${id}/ops/alerts`)
      .then((r) => r.json())
      .then((r: Envelope<Alert[]>) => setAlerts(r.data ?? []));
    fetch(`${API_BASE_URL}/project-spaces/${id}/ops/sops`)
      .then((r) => r.json())
      .then((r: Envelope<SOP[]>) => setSops(r.data ?? []));
  };
  useEffect(() => {
    load(psID);
  }, [psID]);

  async function inspect() {
    if (!psID) return;
    setBusy(true);
    await fetch(`${API_BASE_URL}/project-spaces/${psID}/ops/inspect`, { method: "POST" });
    setBusy(false);
    load(psID);
  }
  async function resolveAlert(id: string) {
    await fetch(`${API_BASE_URL}/project-spaces/${psID}/ops/alerts/${id}/resolve`, {
      method: "POST",
    });
    load(psID);
  }
  async function submitAlert() {
    if (!alertForm.title.trim()) return;
    await fetch(`${API_BASE_URL}/project-spaces/${psID}/ops/alerts`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ source: "manual", ...alertForm }),
    });
    setAlertForm({ severity: "warning", title: "", description: "" });
    setShowAlertForm(false);
    load(psID);
  }
  async function submitSop() {
    if (!sopForm.code.trim() || !sopForm.name.trim()) return;
    await fetch(`${API_BASE_URL}/project-spaces/${psID}/ops/sops`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(sopForm),
    });
    setSopForm({
      code: "",
      name: "",
      category: "restart",
      risk_level: "low",
      steps: "",
      status: "draft",
    });
    setShowSopForm(false);
    load(psID);
  }
  async function deleteSop(id: string) {
    await fetch(`${API_BASE_URL}/project-spaces/${psID}/ops/sops/${id}`, { method: "DELETE" });
    load(psID);
  }

  const sum = (m: Record<string, number> | undefined) =>
    Object.values(m ?? {}).reduce((a, b) => a + b, 0);

  return (
    <div>
      <div className="mb-4 flex items-center gap-3">
        <h1 className="text-xl font-bold">🛠️ 运维中心</h1>
        <select
          value={psID}
          onChange={(e) => setPsID(e.target.value)}
          className="rounded-md border border-neutral-300 px-2 py-1 text-sm"
        >
          {spaces.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name} ({s.slug})
            </option>
          ))}
        </select>
        <button
          onClick={inspect}
          disabled={busy}
          className="ml-auto rounded-md bg-blue-600 px-3 py-1.5 text-sm text-white disabled:opacity-50"
        >
          {busy ? "巡检中…" : "🔍 巡检"}
        </button>
      </div>
      <p className="mb-4 text-sm text-neutral-600">
        平台健康看板 + 告警 + SOP
        预案库。点击「巡检」触发健康检查，异常组件自动产生告警。高风险自愈经 🚪G6 人工确认。
      </p>

      {/* 总览健康 */}
      <div className="mb-4 flex items-center gap-2">
        <span className="text-sm text-neutral-500">总体健康：</span>
        <span
          className={`rounded-md px-2 py-0.5 text-sm font-medium ${HEALTH_COLOR[dash?.overall_health ?? "healthy"]}`}
        >
          {HEALTH_ICON[dash?.overall_health ?? "healthy"]} {dash?.overall_health ?? "—"}
        </span>
        {dash && dash.open_alerts > 0 && (
          <span className="rounded-md bg-red-100 px-2 py-0.5 text-sm text-red-700">
            {dash.open_alerts} 个未恢复告警
          </span>
        )}
      </div>

      {/* 组件健康 */}
      <div className="mb-6 grid grid-cols-1 gap-3 md:grid-cols-3">
        {(dash?.components ?? []).map((c) => (
          <div key={c.name} className="rounded-lg border border-neutral-200 bg-white p-3">
            <div className="flex items-center justify-between">
              <span className="font-mono text-sm font-medium">{c.name}</span>
              <span
                className={`rounded px-1.5 py-0.5 text-xs ${HEALTH_COLOR[c.status] ?? "bg-neutral-100"}`}
              >
                {HEALTH_ICON[c.status]} {c.status}
              </span>
            </div>
            <div className="mt-1 text-xs text-neutral-500">{c.detail}</div>
            {c.latency_ms ? (
              <div className="mt-1 text-[11px] text-neutral-400">{c.latency_ms} ms</div>
            ) : null}
          </div>
        ))}
      </div>

      {/* 统计 + 用量 */}
      <div className="mb-6 grid grid-cols-2 gap-3 md:grid-cols-4">
        <Stat
          label="需求"
          value={sum(dash?.stats.requirements)}
          sub={fmtMap(dash?.stats.requirements)}
        />
        <Stat
          label="编码任务"
          value={sum(dash?.stats.code_tasks)}
          sub={fmtMap(dash?.stats.code_tasks)}
        />
        <Stat label="变更" value={sum(dash?.stats.changes)} sub={fmtMap(dash?.stats.changes)} />
        <Stat
          label="发布版本"
          value={dash?.stats.releases ?? 0}
          sub={`激活 SOP ${dash?.stats.active_sops ?? 0}`}
        />
        <Stat label="Token 总量" value={dash?.usage.total_tokens ?? 0} />
        <Stat label="AI 调用" value={dash?.usage.total_calls ?? 0} />
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        {/* 告警 */}
        <div>
          <div className="mb-2 flex items-center gap-2">
            <span className="text-sm font-semibold">告警（{alerts.length}）</span>
            <button
              onClick={() => setShowAlertForm(!showAlertForm)}
              className="text-xs text-blue-600 hover:underline"
            >
              ＋ 新建
            </button>
          </div>
          {showAlertForm && (
            <div className="mb-2 space-y-1 rounded-md border border-neutral-200 bg-white p-2">
              <select
                value={alertForm.severity}
                onChange={(e) => setAlertForm({ ...alertForm, severity: e.target.value })}
                className="rounded border border-neutral-300 px-2 py-1 text-sm"
              >
                <option value="critical">critical</option>
                <option value="warning">warning</option>
                <option value="info">info</option>
              </select>
              <input
                placeholder="标题"
                value={alertForm.title}
                onChange={(e) => setAlertForm({ ...alertForm, title: e.target.value })}
                className="w-full rounded border border-neutral-300 px-2 py-1 text-sm"
              />
              <input
                placeholder="描述（可选）"
                value={alertForm.description}
                onChange={(e) => setAlertForm({ ...alertForm, description: e.target.value })}
                className="w-full rounded border border-neutral-300 px-2 py-1 text-sm"
              />
              <button
                onClick={submitAlert}
                className="rounded bg-blue-600 px-3 py-1 text-xs text-white"
              >
                创建告警
              </button>
            </div>
          )}
          <div className="space-y-2">
            {alerts.map((a) => (
              <div key={a.id} className="rounded-md border border-neutral-200 bg-white p-2 text-sm">
                <div className="flex items-center gap-2">
                  <span
                    className={`rounded px-1.5 py-0.5 text-xs ${SEV_COLOR[a.severity] ?? "bg-neutral-100"}`}
                  >
                    {a.severity}
                  </span>
                  <span
                    className={`rounded px-1.5 py-0.5 text-xs ${a.status === "firing" ? "bg-red-50 text-red-600" : "bg-neutral-100 text-neutral-500"}`}
                  >
                    {a.status}
                  </span>
                  <span className="font-mono text-xs text-neutral-400">{a.source}</span>
                  {a.status === "firing" && (
                    <button
                      onClick={() => resolveAlert(a.id)}
                      className="ml-auto text-xs text-blue-600 hover:underline"
                    >
                      恢复
                    </button>
                  )}
                </div>
                <div className="mt-1 font-medium">{a.title}</div>
                {a.description && <div className="text-xs text-neutral-500">{a.description}</div>}
              </div>
            ))}
            {alerts.length === 0 && <div className="text-sm text-neutral-400">暂无告警</div>}
          </div>
        </div>

        {/* 活动 + SOP */}
        <div className="space-y-6">
          <div>
            <div className="mb-2 text-sm font-semibold">最近活动</div>
            <div className="space-y-1">
              {(dash?.activity ?? []).map((it, i) => (
                <div
                  key={i}
                  className="flex items-center gap-2 rounded-md border border-neutral-200 bg-white p-2 text-xs"
                >
                  <span className="rounded bg-neutral-100 px-1.5 py-0.5 font-mono">{it.kind}</span>
                  <span className="text-neutral-500">{it.action}</span>
                  <span className="truncate text-neutral-700">{it.title}</span>
                  <span className="ml-auto shrink-0 text-neutral-400">
                    {new Date(it.time).toLocaleString()}
                  </span>
                </div>
              ))}
              {(dash?.activity ?? []).length === 0 && (
                <div className="text-sm text-neutral-400">暂无活动</div>
              )}
            </div>
          </div>

          <div>
            <div className="mb-2 flex items-center gap-2">
              <span className="text-sm font-semibold">SOP 预案（{sops.length}）</span>
              <button
                onClick={() => setShowSopForm(!showSopForm)}
                className="text-xs text-blue-600 hover:underline"
              >
                ＋ 新建
              </button>
            </div>
            {showSopForm && (
              <div className="mb-2 space-y-1 rounded-md border border-neutral-200 bg-white p-2">
                <div className="flex gap-2">
                  <input
                    placeholder="编码（如 RESTART-POD）"
                    value={sopForm.code}
                    onChange={(e) => setSopForm({ ...sopForm, code: e.target.value })}
                    className="flex-1 rounded border border-neutral-300 px-2 py-1 text-sm"
                  />
                  <input
                    placeholder="名称"
                    value={sopForm.name}
                    onChange={(e) => setSopForm({ ...sopForm, name: e.target.value })}
                    className="flex-1 rounded border border-neutral-300 px-2 py-1 text-sm"
                  />
                </div>
                <div className="flex gap-2 text-sm">
                  <select
                    value={sopForm.category}
                    onChange={(e) => setSopForm({ ...sopForm, category: e.target.value })}
                    className="rounded border border-neutral-300 px-2 py-1"
                  >
                    {["restart", "scale", "cache", "traffic", "data"].map((c) => (
                      <option key={c} value={c}>
                        {c}
                      </option>
                    ))}
                  </select>
                  <select
                    value={sopForm.risk_level}
                    onChange={(e) => setSopForm({ ...sopForm, risk_level: e.target.value })}
                    className="rounded border border-neutral-300 px-2 py-1"
                  >
                    {["low", "medium", "high"].map((c) => (
                      <option key={c} value={c}>
                        {c}
                      </option>
                    ))}
                  </select>
                  <select
                    value={sopForm.status}
                    onChange={(e) => setSopForm({ ...sopForm, status: e.target.value })}
                    className="rounded border border-neutral-300 px-2 py-1"
                  >
                    {["draft", "active", "deprecated"].map((c) => (
                      <option key={c} value={c}>
                        {c}
                      </option>
                    ))}
                  </select>
                </div>
                <textarea
                  placeholder="执行步骤（Markdown）"
                  value={sopForm.steps}
                  onChange={(e) => setSopForm({ ...sopForm, steps: e.target.value })}
                  rows={2}
                  className="w-full rounded border border-neutral-300 px-2 py-1 text-sm"
                />
                <button
                  onClick={submitSop}
                  className="rounded bg-blue-600 px-3 py-1 text-xs text-white"
                >
                  创建 SOP
                </button>
              </div>
            )}
            <div className="space-y-2">
              {sops.map((s) => (
                <div
                  key={s.id}
                  className="rounded-md border border-neutral-200 bg-white p-2 text-sm"
                >
                  <div className="flex items-center gap-2">
                    <span className="font-mono text-xs text-neutral-400">{s.code}</span>
                    <span className="font-medium">{s.name}</span>
                    <span className="rounded bg-neutral-100 px-1.5 py-0.5 text-xs">
                      {s.category}
                    </span>
                    <span
                      className={`rounded px-1.5 py-0.5 text-xs ${s.risk_level === "high" ? "bg-red-100 text-red-700" : s.risk_level === "medium" ? "bg-amber-100 text-amber-700" : "bg-emerald-100 text-emerald-700"}`}
                    >
                      {s.risk_level}
                    </span>
                    <span
                      className={`rounded px-1.5 py-0.5 text-xs ${s.status === "active" ? "bg-blue-100 text-blue-700" : "bg-neutral-100 text-neutral-500"}`}
                    >
                      {s.status}
                    </span>
                    {s.requires_approval && <span className="text-xs text-amber-600">需审批</span>}
                    <button
                      onClick={() => deleteSop(s.id)}
                      className="ml-auto text-xs text-red-600 hover:underline"
                    >
                      删除
                    </button>
                  </div>
                  {s.steps && (
                    <div className="mt-1 whitespace-pre-wrap text-xs text-neutral-600">
                      {s.steps}
                    </div>
                  )}
                </div>
              ))}
              {sops.length === 0 && <div className="text-sm text-neutral-400">暂无 SOP</div>}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

function Stat({ label, value, sub }: { label: string; value: number; sub?: string }) {
  return (
    <div className="rounded-lg border border-neutral-200 bg-white p-3">
      <div className="text-xs text-neutral-500">{label}</div>
      <div className="text-xl font-bold">{value}</div>
      {sub && <div className="mt-0.5 text-[11px] text-neutral-400">{sub}</div>}
    </div>
  );
}

function fmtMap(m: Record<string, number> | undefined): string {
  if (!m) return "";
  return Object.entries(m)
    .map(([k, v]) => `${k}:${v}`)
    .join(" ");
}

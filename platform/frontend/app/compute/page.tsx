"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL } from "@/lib/api";

type Envelope<T> = { code: number; data: T; message?: string };
type Provider = {
  id: string;
  name: string;
  type: string;
  base_url: string;
  api_key?: string;
  enabled: boolean;
  description?: string;
};
type Model = {
  id: string;
  provider_id: string;
  name: string;
  display_name?: string;
  modality: string;
  context_window?: number;
  max_output?: number;
  cost_input: number;
  cost_output: number;
  enabled: boolean;
};
type Route = {
  id: string;
  task_type: string;
  primary_model_id: string;
  fallback_model_id?: string;
  enabled: boolean;
};
const TASK_LABELS: Record<string, string> = {
  spec: "需求规格生成",
  code: "AI编码",
  test: "测试用例",
  review: "代码评审",
  chat: "对话",
  general: "通用",
};

const MODALITY_LABEL: Record<string, string> = {
  text: "文本",
  vision: "视觉",
  code: "编码",
  voice: "语音",
};

export default function ComputePage() {
  const [tab, setTab] = useState<"providers" | "models" | "routes" | "usage">("providers");
  const [providers, setProviders] = useState<Provider[]>([]);
  const [models, setModels] = useState<Model[]>([]);
  const [routes, setRoutes] = useState<Route[]>([]);
  const [msg, setMsg] = useState("");
  const [showPForm, setShowPForm] = useState(false);
  const [pForm, setPForm] = useState({
    name: "",
    type: "api",
    base_url: "",
    api_key: "",
    description: "",
  });
  const [showMForm, setShowMForm] = useState(false);
  const [mForm, setMForm] = useState({
    provider_id: "",
    name: "",
    display_name: "",
    modality: "text",
    context_window: 0,
    max_output: 0,
    cost_input: 0,
    cost_output: 0,
  });

  const loadProviders = () =>
    fetch(`${API_BASE_URL}/compute/providers`)
      .then((r) => r.json())
      .then((r: Envelope<Provider[]>) => setProviders(r.data ?? []));
  const loadModels = () =>
    fetch(`${API_BASE_URL}/compute/models`)
      .then((r) => r.json())
      .then((r: Envelope<Model[]>) => setModels(r.data ?? []));
  const loadRoutes = () =>
    fetch(`${API_BASE_URL}/compute/routes`)
      .then((r) => r.json())
      .then((r: Envelope<Route[]>) => setRoutes(r.data ?? []));
  useEffect(() => {
    loadProviders();
    loadModels();
    loadRoutes();
  }, []);

  async function saveProvider() {
    const res = await fetch(`${API_BASE_URL}/compute/providers`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(pForm),
    });
    const r = await res.json();
    setMsg(r.code === 0 ? `✓ 供应商已添加` : `✗ ${r.message}`);
    if (r.code === 0) {
      setShowPForm(false);
      setPForm({ name: "", type: "api", base_url: "", api_key: "", description: "" });
      loadProviders();
    }
  }

  async function saveModel() {
    const res = await fetch(`${API_BASE_URL}/compute/models`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(mForm),
    });
    const r = await res.json();
    setMsg(r.code === 0 ? `✓ 模型已添加` : `✗ ${r.message}`);
    if (r.code === 0) {
      setShowMForm(false);
      setMForm({
        provider_id: "",
        name: "",
        display_name: "",
        modality: "text",
        context_window: 0,
        max_output: 0,
        cost_input: 0,
        cost_output: 0,
      });
      loadModels();
    }
  }

  async function toggleProvider(p: Provider) {
    const res = await fetch(`${API_BASE_URL}/compute/providers/${p.id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ...p, enabled: !p.enabled }),
    });
    if ((await res.json()).code === 0) loadProviders();
  }

  async function toggleModel(m: Model) {
    const res = await fetch(`${API_BASE_URL}/compute/models/${m.id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ...m, enabled: !m.enabled }),
    });
    if ((await res.json()).code === 0) loadModels();
  }

  // 按 provider 分组 models
  const modelsByProvider = models.reduce(
    (acc, m) => {
      (acc[m.provider_id] ||= []).push(m);
      return acc;
    },
    {} as Record<string, Model[]>
  );

  async function saveRoute(taskType: string, modelId: string, fallbackId: string) {
    const body: { primary_model_id: string; enabled: boolean; fallback_model_id?: string } = {
      primary_model_id: modelId,
      enabled: true,
    };
    if (fallbackId) body.fallback_model_id = fallbackId;
    const res = await fetch(`${API_BASE_URL}/compute/routes/${taskType}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const r = await res.json();
    setMsg(r.code === 0 ? `✓ 路由 ${TASK_LABELS[taskType] || taskType} 已保存` : `✗ ${r.message}`);
    if (r.code === 0) loadRoutes();
  }

  const modelLabel = (id: string) => {
    const m = models.find((x) => x.id === id);
    return m ? `${m.display_name || m.name}` : "—";
  };
  const allTaskTypes = ["spec", "code", "test", "review", "chat", "general"];

  return (
    <div>
      <h1 className="mb-1 text-xl font-bold">⚡ 算力中心</h1>
      <p className="mb-4 text-sm text-text-muted">多平台多模型统一管理 · 智能路由 · 成本核算</p>

      <div className="mb-4 flex gap-2">
        {(
          [
            { k: "providers", l: "🔌 供应商" },
            { k: "models", l: "🤖 模型" },
            { k: "routes", l: "🔀 路由" },
            { k: "usage", l: "📊 看板" },
          ] as const
        ).map((t) => (
          <button
            key={t.k}
            onClick={() => setTab(t.k)}
            className={`rounded-md px-3 py-1.5 text-sm ${tab === t.k ? "bg-accent text-white" : "bg-surface-2"}`}
          >
            {t.l}
          </button>
        ))}
      </div>
      {msg && <div className="mb-3 text-sm text-accent">{msg}</div>}

      {/* 供应商 Tab */}
      {tab === "providers" && (
        <div>
          <div className="mb-3 flex justify-between items-center">
            <span className="text-sm font-semibold">供应商（{providers.length}）</span>
            <button
              onClick={() => setShowPForm(!showPForm)}
              className="rounded bg-accent px-3 py-1 text-xs text-white"
            >
              {showPForm ? "取消" : "＋ 添加供应商"}
            </button>
          </div>

          {showPForm && (
            <div className="mb-3 grid grid-cols-1 gap-2 rounded-lg border bg-surface p-3 sm:grid-cols-2">
              <input
                placeholder="名称（如：智谱 GLM）"
                value={pForm.name}
                onChange={(e) => setPForm({ ...pForm, name: e.target.value })}
                className="rounded border px-2 py-1 text-sm"
              />
              <select
                value={pForm.type}
                onChange={(e) => setPForm({ ...pForm, type: e.target.value })}
                className="rounded border px-2 py-1 text-sm"
              >
                <option value="api">API（云端）</option>
                <option value="local">本地（Ollama/vLLM）</option>
              </select>
              <input
                placeholder="base_url（如 https://open.bigmodel.cn/api/paas/v4）"
                value={pForm.base_url}
                onChange={(e) => setPForm({ ...pForm, base_url: e.target.value })}
                className="rounded border px-2 py-1 text-sm col-span-2"
              />
              <input
                type="password"
                placeholder="API Key"
                value={pForm.api_key}
                onChange={(e) => setPForm({ ...pForm, api_key: e.target.value })}
                className="rounded border px-2 py-1 text-sm"
              />
              <input
                placeholder="说明（可选）"
                value={pForm.description}
                onChange={(e) => setPForm({ ...pForm, description: e.target.value })}
                className="rounded border px-2 py-1 text-sm"
              />
              <button
                onClick={saveProvider}
                className="rounded bg-success px-3 py-1.5 text-sm text-white col-span-2"
              >
                保存
              </button>
            </div>
          )}

          <div className="space-y-2">
            {providers.map((p) => (
              <div
                key={p.id}
                className={`flex items-center justify-between rounded-md border bg-surface p-3 text-sm ${p.enabled ? "" : "opacity-50"}`}
              >
                <div>
                  <span className="font-medium">{p.name}</span>
                  <span className="ml-2 rounded bg-surface-2 px-1.5 py-0.5 text-xs">{p.type}</span>
                  <div className="mt-0.5 text-xs text-text-muted">{p.base_url}</div>
                  {p.description && <div className="text-xs text-text-muted">{p.description}</div>}
                </div>
                <button onClick={() => toggleProvider(p)} className="text-xs text-accent">
                  {p.enabled ? "禁用" : "启用"}
                </button>
              </div>
            ))}
            {providers.length === 0 && <div className="text-sm text-text-muted">暂无供应商</div>}
          </div>
        </div>
      )}

      {/* 模型 Tab */}
      {tab === "models" && (
        <div>
          <div className="mb-3 flex justify-between items-center">
            <span className="text-sm font-semibold">模型（{models.length}）</span>
            <button
              onClick={() => {
                setMForm({ ...mForm, provider_id: providers[0]?.id || "" });
                setShowMForm(!showMForm);
              }}
              className="rounded bg-accent px-3 py-1 text-xs text-white"
            >
              {showMForm ? "取消" : "＋ 添加模型"}
            </button>
          </div>

          {showMForm && (
            <div className="mb-3 grid grid-cols-1 gap-2 rounded-lg border bg-surface p-3 sm:grid-cols-2">
              <select
                value={mForm.provider_id}
                onChange={(e) => setMForm({ ...mForm, provider_id: e.target.value })}
                className="rounded border px-2 py-1 text-sm"
              >
                <option value="">选供应商</option>
                {providers.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </select>
              <input
                placeholder="模型名（如 glm-5.1）"
                value={mForm.name}
                onChange={(e) => setMForm({ ...mForm, name: e.target.value })}
                className="rounded border px-2 py-1 text-sm"
              />
              <input
                placeholder="显示名（如 GLM-5.1）"
                value={mForm.display_name}
                onChange={(e) => setMForm({ ...mForm, display_name: e.target.value })}
                className="rounded border px-2 py-1 text-sm"
              />
              <select
                value={mForm.modality}
                onChange={(e) => setMForm({ ...mForm, modality: e.target.value })}
                className="rounded border px-2 py-1 text-sm"
              >
                <option value="text">文本</option>
                <option value="vision">视觉</option>
                <option value="code">编码</option>
                <option value="voice">语音</option>
              </select>
              <input
                type="number"
                placeholder="上下文窗口"
                value={mForm.context_window || ""}
                onChange={(e) => setMForm({ ...mForm, context_window: +e.target.value })}
                className="rounded border px-2 py-1 text-sm"
              />
              <input
                type="number"
                placeholder="最大输出"
                value={mForm.max_output || ""}
                onChange={(e) => setMForm({ ...mForm, max_output: +e.target.value })}
                className="rounded border px-2 py-1 text-sm"
              />
              <input
                type="number"
                step="0.001"
                placeholder="输入单价(元/千token)"
                value={mForm.cost_input || ""}
                onChange={(e) => setMForm({ ...mForm, cost_input: +e.target.value })}
                className="rounded border px-2 py-1 text-sm"
              />
              <input
                type="number"
                step="0.001"
                placeholder="输出单价(元/千token)"
                value={mForm.cost_output || ""}
                onChange={(e) => setMForm({ ...mForm, cost_output: +e.target.value })}
                className="rounded border px-2 py-1 text-sm"
              />
              <button
                onClick={saveModel}
                className="rounded bg-success px-3 py-1.5 text-sm text-white col-span-2"
              >
                保存
              </button>
            </div>
          )}

          {providers.map((p) => (
            <div key={p.id} className="mb-4">
              <div className="mb-1 text-xs font-semibold text-text-muted">{p.name}</div>
              <div className="space-y-1">
                {(modelsByProvider[p.id] || []).map((m) => (
                  <div
                    key={m.id}
                    className={`flex items-center justify-between rounded border bg-surface p-2 text-sm ${m.enabled ? "" : "opacity-50"}`}
                  >
                    <div className="flex items-center gap-2">
                      <span className="font-medium">{m.display_name || m.name}</span>
                      <span className="rounded bg-accent/10 px-1.5 py-0.5 text-xs text-accent">
                        {MODALITY_LABEL[m.modality] || m.modality}
                      </span>
                      {m.context_window && m.context_window > 0 && (
                        <span className="text-xs text-text-muted">
                          {(m.context_window / 1000).toFixed(0)}K ctx
                        </span>
                      )}
                      {(m.cost_input > 0 || m.cost_output > 0) && (
                        <span className="text-xs text-text-muted">
                          ¥{m.cost_input}/{m.cost_output}
                        </span>
                      )}
                    </div>
                    <button onClick={() => toggleModel(m)} className="text-xs text-accent">
                      {m.enabled ? "禁用" : "启用"}
                    </button>
                  </div>
                ))}
                {(!modelsByProvider[p.id] || []).length === 0 && (
                  <div className="text-xs text-text-muted">无模型</div>
                )}
              </div>
            </div>
          ))}
          {providers.length === 0 && <div className="text-sm text-text-muted">请先添加供应商</div>}
        </div>
      )}

      {/* 看板 Tab — 保持原有用量看板（从 /usage 加载） */}
      {tab === "usage" && <UsageDashboard />}

      {/* 路由 Tab */}
      {tab === "routes" && (
        <div>
          <div className="mb-2 text-sm font-semibold">路由策略（任务类型 → 模型 + fallback）</div>
          <p className="mb-3 text-xs text-text-muted">
            不同任务自动选不同模型：需求规格用快的、编码用强的。主模型失败自动切 fallback。
          </p>
          <div className="space-y-2">
            {allTaskTypes.map((tt) => {
              const rt = routes.find((r) => r.task_type === tt);
              return (
                <RouteRow key={tt} taskType={tt} route={rt} models={models} onSave={saveRoute} />
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}

// 用量看板子组件（从原 compute page 迁移）
function UsageDashboard() {
  const [spaces, setSpaces] = useState<{ id: string; name: string; slug: string }[]>([]);
  const [psID, setPsID] = useState("");
  const [stats, setStats] = useState<{
    total_tokens: number;
    total_calls: number;
    by_model: { model: string; tokens: number; calls: number }[];
  } | null>(null);

  useEffect(() => {
    fetch(`${API_BASE_URL}/project-spaces`)
      .then((r) => r.json())
      .then((r) => {
        setSpaces(r.data ?? []);
        if (r.data?.[0]) setPsID(r.data[0].id);
      });
  }, []);
  useEffect(() => {
    if (!psID) return;
    fetch(`${API_BASE_URL}/project-spaces/${psID}/usage/stats`)
      .then((r) => r.json())
      .then((r) => setStats(r.data));
  }, [psID]);

  return (
    <div>
      <div className="mb-3">
        <label className="text-xs text-text-muted">项目空间</label>
        <select
          value={psID}
          onChange={(e) => setPsID(e.target.value)}
          className="ml-2 rounded border px-2 py-1 text-sm"
        >
          {spaces.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </select>
      </div>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div className="rounded-lg border bg-surface p-4">
          <div className="text-sm text-text-muted">总 Token</div>
          <div className="text-2xl font-bold">{stats?.total_tokens?.toLocaleString() ?? 0}</div>
        </div>
        <div className="rounded-lg border bg-surface p-4">
          <div className="text-sm text-text-muted">总调用</div>
          <div className="text-2xl font-bold">{stats?.total_calls ?? 0}</div>
        </div>
      </div>
      {stats?.by_model && stats.by_model.length > 0 && (
        <div className="mt-4">
          <div className="mb-2 text-sm font-semibold">按模型分布</div>
          {stats.by_model.map((m) => (
            <div key={m.model} className="mb-1 flex items-center gap-2 text-sm">
              <span className="w-32 truncate">{m.model}</span>
              <div className="flex-1 rounded-full bg-surface-2">
                <div
                  className="h-5 rounded-full bg-blue-500 px-2 text-xs leading-5 text-white"
                  style={{
                    width: `${Math.max(10, (m.tokens / (stats.total_tokens || 1)) * 100)}%`,
                  }}
                >
                  {m.tokens.toLocaleString()}
                </div>
              </div>
              <span className="text-xs text-text-muted">{m.calls}次</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// 路由行组件
function RouteRow({
  taskType,
  route,
  models,
  onSave,
}: {
  taskType: string;
  route?: Route;
  models: Model[];
  onSave: (tt: string, mid: string, fb: string) => void;
}) {
  const [primary, setPrimary] = useState(route?.primary_model_id || "");
  const [fallback, setFallback] = useState(route?.fallback_model_id || "");

  useEffect(() => {
    setPrimary(route?.primary_model_id || "");
    setFallback(route?.fallback_model_id || "");
  }, [route]);

  return (
    <div className="flex items-center gap-2 rounded-md border bg-surface p-2 text-sm">
      <span className="w-28 font-medium">{TASK_LABELS[taskType] || taskType}</span>
      <select
        value={primary}
        onChange={(e) => setPrimary(e.target.value)}
        className="flex-1 rounded border px-2 py-1 text-sm"
      >
        <option value="">选主模型</option>
        {models
          .filter((m) => m.enabled)
          .map((m) => (
            <option key={m.id} value={m.id}>
              {m.display_name || m.name}
            </option>
          ))}
      </select>
      <span className="text-xs text-text-muted">fallback</span>
      <select
        value={fallback}
        onChange={(e) => setFallback(e.target.value)}
        className="w-40 rounded border px-2 py-1 text-sm"
      >
        <option value="">无</option>
        {models
          .filter((m) => m.enabled && m.id !== primary)
          .map((m) => (
            <option key={m.id} value={m.id}>
              {m.display_name || m.name}
            </option>
          ))}
      </select>
      <button
        onClick={() => onSave(taskType, primary, fallback)}
        disabled={!primary}
        className="rounded bg-accent px-2 py-1 text-xs text-white disabled:opacity-40"
      >
        保存
      </button>
    </div>
  );
}

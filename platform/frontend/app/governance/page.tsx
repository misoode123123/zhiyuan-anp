"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL } from "@/lib/api";

type Envelope<T> = { code: number; data: T; message?: string };
type ProjectSpace = { id: string; name: string; slug: string };
type Rule = {
  id: string;
  name: string;
  category: string;
  type: string;
  condition: string;
  condition_field: string;
  action: string;
  scope: string;
  enabled: boolean;
  description: string;
};
type Std = {
  id: string;
  project_space_id: string | null;
  name: string;
  category: string;
  content: string;
  priority: number;
  enabled: boolean;
};

const TYPE_LABEL: Record<string, string> = {
  mandatory: "强制",
  should: "应遵循",
  reference: "参考",
};
const ACTION_LABEL: Record<string, string> = {
  block: "阻断",
  warn: "告警",
  require_approval: "需审批",
};
const ACTION_COLOR: Record<string, string> = {
  block: "bg-red-100 text-red-700",
  warn: "bg-amber-100 text-amber-700",
  require_approval: "bg-blue-100 text-blue-700",
};
const STD_CAT_COLOR: Record<string, string> = {
  general: "bg-neutral-100 text-neutral-700",
  language: "bg-blue-100 text-blue-700",
  framework: "bg-purple-100 text-purple-700",
  security: "bg-red-100 text-red-700",
  testing: "bg-emerald-100 text-emerald-700",
};

const empty = {
  name: "",
  category: "coding",
  type: "mandatory",
  condition: "",
  condition_field: "prompt",
  action: "block",
  scope: "dev",
  description: "",
};

export default function GovernancePage() {
  const [tab, setTab] = useState<"rules" | "standards">("rules");

  // 规则（RaC）
  const [rules, setRules] = useState<Rule[]>([]);
  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState({ ...empty });
  const [msg, setMsg] = useState("");

  // 编码规范
  const [spaces, setSpaces] = useState<ProjectSpace[]>([]);
  const [psID, setPsID] = useState("");
  const [globalStds, setGlobalStds] = useState<Std[]>([]);
  const [projStds, setProjStds] = useState<Std[]>([]);
  const [stdForm, setStdForm] = useState({
    name: "",
    category: "general",
    content: "",
    priority: 100,
  });
  const [stdScope, setStdScope] = useState<"global" | "project">("global");
  const [effPreview, setEffPreview] = useState<string | null>(null);

  const load = () =>
    fetch(`${API_BASE_URL}/rules`)
      .then((r) => r.json())
      .then((r: Envelope<Rule[]>) => setRules(r.data ?? []));
  useEffect(() => {
    load();
  }, []);

  useEffect(() => {
    fetch(`${API_BASE_URL}/project-spaces`)
      .then((r) => r.json())
      .then((r: Envelope<ProjectSpace[]>) => {
        setSpaces(r.data ?? []);
        if (r.data?.[0]) setPsID(r.data[0].id);
      });
  }, []);

  const loadStds = () => {
    fetch(`${API_BASE_URL}/standards`)
      .then((r) => r.json())
      .then((r: Envelope<Std[]>) => setGlobalStds(r.data ?? []));
    if (psID)
      fetch(`${API_BASE_URL}/project-spaces/${psID}/standards`)
        .then((r) => r.json())
        .then((r: Envelope<Std[]>) => setProjStds(r.data ?? []));
  };
  useEffect(() => {
    loadStds();
  }, [psID]);

  async function create() {
    if (!form.name || !form.condition) {
      setMsg("名称和条件必填");
      return;
    }
    const res = await fetch(`${API_BASE_URL}/rules`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(form),
    });
    const r = await res.json();
    setMsg(r.code === 0 ? "✓ 规则已创建" : `✗ ${r.message}`);
    if (r.code === 0) {
      setShowForm(false);
      setForm({ ...empty });
      load();
    }
  }

  async function toggle(r: Rule) {
    const res = await fetch(`${API_BASE_URL}/rules/${r.id}/enabled`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ enabled: !r.enabled }),
    });
    if ((await res.json()).code === 0) load();
  }

  async function del(id: string) {
    if (!confirm("删除该规则？")) return;
    const res = await fetch(`${API_BASE_URL}/rules/${id}`, { method: "DELETE" });
    if ((await res.json()).code === 0) load();
  }

  async function submitStd() {
    if (!stdForm.name || !stdForm.content) {
      setMsg("名称和正文必填");
      return;
    }
    const url =
      stdScope === "global"
        ? `${API_BASE_URL}/standards`
        : `${API_BASE_URL}/project-spaces/${psID}/standards`;
    const res = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(stdForm),
    });
    const r = await res.json();
    setMsg(r.code === 0 ? "✓ 规范已创建" : `✗ ${r.message}`);
    if (r.code === 0) {
      setStdForm({ name: "", category: "general", content: "", priority: 100 });
      loadStds();
    }
  }

  async function toggleStd(s: Std) {
    await fetch(`${API_BASE_URL}/standards/${s.id}/enabled`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ enabled: !s.enabled }),
    });
    loadStds();
  }

  async function deleteStd(id: string) {
    if (!confirm("删除该规范？")) return;
    await fetch(`${API_BASE_URL}/standards/${id}`, { method: "DELETE" });
    loadStds();
  }

  async function previewEffective() {
    if (!psID) return;
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/standards/effective`);
    const r = await res.json();
    setEffPreview(r.data?.prompt_section ?? "");
  }

  const grouped = rules.reduce(
    (acc, r) => {
      (acc[r.category] ||= []).push(r);
      return acc;
    },
    {} as Record<string, Rule[]>
  );

  const sel = (k: string, opts: string[], labels?: Record<string, string>) => (
    <select
      value={(form as Record<string, string>)[k]}
      onChange={(e) => setForm({ ...form, [k]: e.target.value })}
      className="rounded-md border border-neutral-300 px-2 py-1 text-sm"
    >
      {opts.map((o) => (
        <option key={o} value={o}>
          {labels?.[o] ?? o}
        </option>
      ))}
    </select>
  );

  const stdList = (title: string, list: Std[]) => (
    <div className="mb-5">
      <div className="mb-2 text-sm font-semibold text-neutral-700">
        {title}（{list.length}）
      </div>
      <div className="space-y-2">
        {list.map((s) => (
          <div
            key={s.id}
            className={`rounded-md border border-neutral-200 bg-white p-3 text-sm ${s.enabled ? "" : "opacity-50"}`}
          >
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                <span
                  className={`rounded px-1.5 py-0.5 text-xs ${STD_CAT_COLOR[s.category] ?? STD_CAT_COLOR.general}`}
                >
                  {s.category}
                </span>
                <span className="font-medium">{s.name}</span>
                <span className="text-xs text-neutral-400">prio {s.priority}</span>
              </div>
              <div className="flex gap-2">
                <button onClick={() => toggleStd(s)} className="text-xs text-blue-600">
                  {s.enabled ? "禁用" : "启用"}
                </button>
                <button onClick={() => deleteStd(s.id)} className="text-xs text-red-600">
                  删除
                </button>
              </div>
            </div>
            <div className="mt-1 text-xs text-neutral-600">{s.content}</div>
          </div>
        ))}
        {list.length === 0 && <div className="text-sm text-neutral-400">暂无</div>}
      </div>
    </div>
  );

  return (
    <div>
      <h1 className="mb-1 text-xl font-bold">⭐ 规则治理中心</h1>
      <div className="mb-4 flex gap-2">
        <button
          onClick={() => setTab("rules")}
          className={`rounded-md px-3 py-1.5 text-sm ${tab === "rules" ? "bg-blue-600 text-white" : "bg-neutral-200 text-neutral-700"}`}
        >
          规则 (RaC)
        </button>
        <button
          onClick={() => setTab("standards")}
          className={`rounded-md px-3 py-1.5 text-sm ${tab === "standards" ? "bg-blue-600 text-white" : "bg-neutral-200 text-neutral-700"}`}
        >
          编码规范
        </button>
      </div>
      {msg && <div className="mb-3 text-sm text-blue-700">{msg}</div>}

      {tab === "rules" && (
        <>
          <div className="mb-1 flex items-center justify-between">
            <p className="text-sm text-neutral-600">
              制度/红线结构化为可执行规则（RaC），约束所有 AI 行为。<b>block</b> 规则违反将阻断 AI
              操作（需🚪人工评估）。
            </p>
            <button
              onClick={() => setShowForm(!showForm)}
              className="rounded-md bg-blue-600 px-3 py-1.5 text-sm text-white"
            >
              {showForm ? "取消" : "＋ 新建规则"}
            </button>
          </div>

          {showForm && (
            <div className="mb-4 grid grid-cols-2 gap-3 rounded-lg border border-neutral-200 bg-white p-4">
              <input
                placeholder="规则名称"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                className="col-span-2 rounded-md border border-neutral-300 px-2 py-1 text-sm"
              />
              <input
                placeholder="条件（正则或关键字，大小写不敏感）"
                value={form.condition}
                onChange={(e) => setForm({ ...form, condition: e.target.value })}
                className="col-span-2 rounded-md border border-neutral-300 px-2 py-1 text-sm"
              />
              {sel("category", ["coding", "security", "process", "general"])}
              {sel("type", ["mandatory", "should", "reference"], TYPE_LABEL)}
              {sel("action", ["block", "warn", "require_approval"], ACTION_LABEL)}
              {sel("scope", ["dev", "requirement", "all"])}
              <input
                placeholder="匹配字段 prompt/output/code_path"
                value={form.condition_field}
                onChange={(e) => setForm({ ...form, condition_field: e.target.value })}
                className="col-span-2 rounded-md border border-neutral-300 px-2 py-1 text-sm"
              />
              <input
                placeholder="说明（可选）"
                value={form.description}
                onChange={(e) => setForm({ ...form, description: e.target.value })}
                className="col-span-2 rounded-md border border-neutral-300 px-2 py-1 text-sm"
              />
              <button
                onClick={create}
                className="col-span-2 rounded-md bg-emerald-600 px-4 py-1.5 text-sm text-white"
              >
                创建规则
              </button>
            </div>
          )}

          {Object.entries(grouped).map(([cat, list]) => (
            <div key={cat} className="mb-5">
              <div className="mb-2 text-sm font-semibold text-neutral-700">{cat}</div>
              <div className="space-y-2">
                {list.map((r) => (
                  <div
                    key={r.id}
                    className={`rounded-md border border-neutral-200 bg-white p-3 text-sm ${r.enabled ? "" : "opacity-50"}`}
                  >
                    <div className="flex items-center justify-between">
                      <div className="flex items-center gap-2">
                        <span className="font-medium">{r.name}</span>
                        <span
                          className={`rounded px-1.5 py-0.5 text-xs ${ACTION_COLOR[r.action] ?? "bg-neutral-100"}`}
                        >
                          {ACTION_LABEL[r.action] ?? r.action}
                        </span>
                        <span className="text-xs text-neutral-400">
                          {TYPE_LABEL[r.type] ?? r.type} · {r.scope} · {r.condition_field}
                        </span>
                      </div>
                      <div className="flex gap-2">
                        <button onClick={() => toggle(r)} className="text-xs text-blue-600">
                          {r.enabled ? "禁用" : "启用"}
                        </button>
                        <button onClick={() => del(r.id)} className="text-xs text-red-600">
                          删除
                        </button>
                      </div>
                    </div>
                    <div className="mt-1 font-mono text-xs text-neutral-500">{r.condition}</div>
                    {r.description && (
                      <div className="mt-1 text-xs text-neutral-400">{r.description}</div>
                    )}
                  </div>
                ))}
              </div>
            </div>
          ))}
          {rules.length === 0 && <div className="text-sm text-neutral-400">暂无规则</div>}
        </>
      )}

      {tab === "standards" && (
        <div>
          <p className="mb-3 text-sm text-neutral-600">
            编码规范 = <b>注入式生成指导</b>：编码时拼进 prompt 告诉 AI「怎么写」（全局 +
            项目级叠加）；与 RaC 规则（硬约束/block）互补。
          </p>

          <div className="mb-3 flex flex-wrap items-center gap-2">
            <label className="text-xs text-neutral-500">项目空间</label>
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
              onClick={previewEffective}
              className="rounded-md bg-neutral-700 px-3 py-1 text-xs text-white"
            >
              预览生效规范
            </button>
          </div>

          {effPreview !== null && (
            <pre className="mb-4 max-h-48 overflow-auto whitespace-pre-wrap rounded bg-neutral-900 p-3 text-xs text-green-300">
              {effPreview || "(无生效规范)"}
            </pre>
          )}

          <div className="mb-4 rounded-lg border border-neutral-200 bg-white p-3">
            <div className="mb-2 flex flex-wrap gap-2 text-sm">
              <select
                value={stdScope}
                onChange={(e) => setStdScope(e.target.value as "global" | "project")}
                className="rounded border border-neutral-300 px-2 py-1"
              >
                <option value="global">全局</option>
                <option value="project">项目级(当前空间)</option>
              </select>
              <input
                placeholder="规范名"
                value={stdForm.name}
                onChange={(e) => setStdForm({ ...stdForm, name: e.target.value })}
                className="flex-1 rounded border border-neutral-300 px-2 py-1"
              />
              <select
                value={stdForm.category}
                onChange={(e) => setStdForm({ ...stdForm, category: e.target.value })}
                className="rounded border border-neutral-300 px-2 py-1"
              >
                {["general", "language", "framework", "security", "testing"].map((c) => (
                  <option key={c} value={c}>
                    {c}
                  </option>
                ))}
              </select>
              <input
                type="number"
                value={stdForm.priority}
                onChange={(e) => setStdForm({ ...stdForm, priority: Number(e.target.value) })}
                className="w-20 rounded border border-neutral-300 px-2 py-1"
                title="priority"
              />
            </div>
            <textarea
              placeholder="规范正文（自然语言/Markdown）"
              value={stdForm.content}
              onChange={(e) => setStdForm({ ...stdForm, content: e.target.value })}
              rows={2}
              className="w-full rounded border border-neutral-300 px-2 py-1 text-sm"
            />
            <button
              onClick={submitStd}
              className="mt-2 rounded bg-blue-600 px-3 py-1 text-sm text-white"
            >
              新建规范
            </button>
          </div>

          {stdList("全局规范", globalStds)}
          {stdList("项目级规范（当前空间）", projStds)}
        </div>
      )}
    </div>
  );
}

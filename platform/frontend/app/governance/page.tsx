"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL } from "@/lib/api";

type Envelope<T> = { code: number; data: T; message?: string };
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

const TYPE_LABEL: Record<string, string> = { mandatory: "强制", should: "应遵循", reference: "参考" };
const ACTION_LABEL: Record<string, string> = { block: "阻断", warn: "告警", require_approval: "需审批" };
const ACTION_COLOR: Record<string, string> = {
  block: "bg-red-100 text-red-700",
  warn: "bg-amber-100 text-amber-700",
  require_approval: "bg-blue-100 text-blue-700",
};

const empty = {
  name: "", category: "coding", type: "mandatory", condition: "",
  condition_field: "prompt", action: "block", scope: "dev", description: "",
};

export default function GovernancePage() {
  const [rules, setRules] = useState<Rule[]>([]);
  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState({ ...empty });
  const [msg, setMsg] = useState("");

  const load = () =>
    fetch(`${API_BASE_URL}/rules`)
      .then((r) => r.json())
      .then((r: Envelope<Rule[]>) => setRules(r.data ?? []));
  useEffect(() => {
    load();
  }, []);

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

  const grouped = rules.reduce((acc, r) => {
    (acc[r.category] ||= []).push(r);
    return acc;
  }, {} as Record<string, Rule[]>);

  const sel = (k: string, opts: string[], labels?: Record<string, string>) => (
    <select
      value={(form as any)[k]}
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

  return (
    <div>
      <div className="mb-1 flex items-center justify-between">
        <h1 className="text-xl font-bold">⭐ 规则治理中心</h1>
        <button
          onClick={() => setShowForm(!showForm)}
          className="rounded-md bg-blue-600 px-3 py-1.5 text-sm text-white"
        >
          {showForm ? "取消" : "＋ 新建规则"}
        </button>
      </div>
      <p className="mb-4 text-sm text-neutral-600">
        制度/红线结构化为可执行规则（RaC），约束所有 AI 行为。<b>block</b> 规则违反将阻断 AI 操作（需🚪人工评估）。
      </p>
      {msg && <div className="mb-3 text-sm text-blue-700">{msg}</div>}

      {showForm && (
        <div className="mb-4 grid grid-cols-2 gap-3 rounded-lg border border-neutral-200 bg-white p-4">
          <input placeholder="规则名称" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} className="col-span-2 rounded-md border border-neutral-300 px-2 py-1 text-sm" />
          <input placeholder="条件（正则或关键字，大小写不敏感）" value={form.condition} onChange={(e) => setForm({ ...form, condition: e.target.value })} className="col-span-2 rounded-md border border-neutral-300 px-2 py-1 text-sm" />
          {sel("category", ["coding", "security", "process", "general"])}
          {sel("type", ["mandatory", "should", "reference"], TYPE_LABEL)}
          {sel("action", ["block", "warn", "require_approval"], ACTION_LABEL)}
          {sel("scope", ["dev", "requirement", "all"])}
          <input placeholder="匹配字段 prompt/output/code_path" value={form.condition_field} onChange={(e) => setForm({ ...form, condition_field: e.target.value })} className="col-span-2 rounded-md border border-neutral-300 px-2 py-1 text-sm" />
          <input placeholder="说明（可选）" value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} className="col-span-2 rounded-md border border-neutral-300 px-2 py-1 text-sm" />
          <button onClick={create} className="col-span-2 rounded-md bg-emerald-600 px-4 py-1.5 text-sm text-white">创建规则</button>
        </div>
      )}

      {Object.entries(grouped).map(([cat, list]) => (
        <div key={cat} className="mb-5">
          <div className="mb-2 text-sm font-semibold text-neutral-700">{cat}</div>
          <div className="space-y-2">
            {list.map((r) => (
              <div key={r.id} className={`rounded-md border border-neutral-200 bg-white p-3 text-sm ${r.enabled ? "" : "opacity-50"}`}>
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <span className="font-medium">{r.name}</span>
                    <span className={`rounded px-1.5 py-0.5 text-xs ${ACTION_COLOR[r.action] ?? "bg-neutral-100"}`}>
                      {ACTION_LABEL[r.action] ?? r.action}
                    </span>
                    <span className="text-xs text-neutral-400">
                      {TYPE_LABEL[r.type] ?? r.type} · {r.scope} · {r.condition_field}
                    </span>
                  </div>
                  <div className="flex gap-2">
                    <button onClick={() => toggle(r)} className="text-xs text-blue-600">{r.enabled ? "禁用" : "启用"}</button>
                    <button onClick={() => del(r.id)} className="text-xs text-red-600">删除</button>
                  </div>
                </div>
                <div className="mt-1 font-mono text-xs text-neutral-500">{r.condition}</div>
                {r.description && <div className="mt-1 text-xs text-neutral-400">{r.description}</div>}
              </div>
            ))}
          </div>
        </div>
      ))}
      {rules.length === 0 && <div className="text-sm text-neutral-400">暂无规则</div>}
    </div>
  );
}

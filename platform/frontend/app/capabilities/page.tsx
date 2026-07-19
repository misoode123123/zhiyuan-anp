"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL } from "@/lib/api";

type Envelope<T> = { code: number; data: T; message?: string };
type PS = { id: string; name: string; slug: string };

type Skill = {
  id: string;
  code: string;
  name: string;
  description: string;
  category: string;
  prompt_template: string;
  version: string;
  status: string;
  risk_level: string;
  is_public: boolean;
};
type APIKey = {
  id: string;
  app_name: string;
  key_prefix: string;
  allowed_skills: string;
  scope: string;
  status: string;
  created_at: string;
};
type UsageStat = {
  skill_id: string;
  calls: number;
  input_tokens: number;
  output_tokens: number;
  success_count: number;
};

const STATUS_COLOR: Record<string, string> = {
  active: "bg-emerald-100 text-emerald-700",
  draft: "bg-neutral-100 text-neutral-500",
  pending_review: "bg-amber-100 text-amber-700",
  offline: "bg-red-100 text-red-700",
};

export default function CapabilitiesPage() {
  const [spaces, setSpaces] = useState<PS[]>([]);
  const [psID, setPsID] = useState("");
  const [skills, setSkills] = useState<Skill[]>([]);
  const [keys, setKeys] = useState<APIKey[]>([]);
  const [usage, setUsage] = useState<UsageStat[]>([]);
  const [showSkillForm, setShowSkillForm] = useState(false);
  const [skillForm, setSkillForm] = useState({
    code: "",
    name: "",
    description: "",
    category: "assistant",
    prompt_template: "",
    risk_level: "low",
  });
  const [newKeySecret, setNewKeySecret] = useState("");
  const [keyForm, setKeyForm] = useState({ app_name: "", allowed_skills: "" });
  // 调用测试器
  const [tester, setTester] = useState({ apiKey: "", skillCode: "data-qa", input: "" });
  const [invokeResult, setInvokeResult] = useState<string>("");
  const [invokeBusy, setInvokeBusy] = useState(false);

  useEffect(() => {
    fetch(`${API_BASE_URL}/project-spaces`)
      .then((r) => r.json())
      .then((r: Envelope<PS[]>) => {
        setSpaces(r.data ?? []);
        const def = (r.data ?? []).find((s) => s.id === "ps_default") ?? (r.data ?? [])[0];
        if (def) setPsID(def.id);
      });
  }, []);

  const load = (id: string) => {
    if (!id) return;
    fetch(`${API_BASE_URL}/project-spaces/${id}/capabilities/skills`)
      .then((r) => r.json())
      .then((r: Envelope<Skill[]>) => setSkills(r.data ?? []));
    fetch(`${API_BASE_URL}/project-spaces/${id}/capabilities/api-keys`)
      .then((r) => r.json())
      .then((r: Envelope<APIKey[]>) => setKeys(r.data ?? []));
    fetch(`${API_BASE_URL}/project-spaces/${id}/capabilities/usage/by-skill`)
      .then((r) => r.json())
      .then((r: Envelope<UsageStat[]>) => setUsage(r.data ?? []));
  };
  useEffect(() => {
    load(psID);
  }, [psID]);

  async function submitSkill() {
    if (!skillForm.code.trim() || !skillForm.name.trim()) return;
    await fetch(`${API_BASE_URL}/project-spaces/${psID}/capabilities/skills`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(skillForm),
    });
    setSkillForm({
      code: "",
      name: "",
      description: "",
      category: "assistant",
      prompt_template: "",
      risk_level: "low",
    });
    setShowSkillForm(false);
    load(psID);
  }
  async function lifecycle(id: string, action: "submit" | "approve" | "offline") {
    await fetch(`${API_BASE_URL}/project-spaces/${psID}/capabilities/skills/${id}/${action}`, {
      method: "POST",
    });
    load(psID);
  }
  async function createKey() {
    if (!keyForm.app_name.trim()) return;
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/capabilities/api-keys`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(keyForm),
    });
    const r = await res.json();
    if (r.data?.secret) {
      setNewKeySecret(r.data.secret);
      setTester((t) => ({ ...t, apiKey: r.data.secret }));
    }
    setKeyForm({ app_name: "", allowed_skills: "" });
    load(psID);
  }
  async function revokeKey(id: string) {
    await fetch(`${API_BASE_URL}/project-spaces/${psID}/capabilities/api-keys/${id}/revoke`, {
      method: "POST",
    });
    load(psID);
  }
  async function invoke() {
    if (!tester.apiKey || !tester.skillCode || !tester.input.trim()) return;
    setInvokeBusy(true);
    setInvokeResult("");
    try {
      const res = await fetch(`${API_BASE_URL}/capabilities/invoke`, {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-Api-Key": tester.apiKey },
        body: JSON.stringify({
          skill_code: tester.skillCode,
          input: tester.input,
          render_hint: "text",
        }),
      });
      const r = await res.json();
      setInvokeResult(r.data?.result?.content ?? `调用失败: ${r.message ?? res.status}`);
    } catch (e) {
      setInvokeResult("调用异常: " + String(e));
    }
    setInvokeBusy(false);
    load(psID);
  }

  const activeSkills = skills.filter((s) => s.status === "active");

  return (
    <div>
      <div className="mb-4 flex items-center gap-3">
        <h1 className="text-xl font-bold">🧩 AI 能力市场</h1>
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
      </div>
      <p className="mb-4 text-sm text-neutral-600">
        平台 AI 能力的统一对外窗口：技能注册/上架 + APIKey +
        统一调用网关（鉴权→校验→调模型→计费）。M1 用 HTTP 替代 gRPC；Copilot SDK
        与多技能编排为后续阶段。
      </p>

      {/* 公共目录 */}
      <div className="mb-6">
        <div className="mb-2 text-sm font-semibold">技能目录（已上架 {activeSkills.length}）</div>
        <div className="grid grid-cols-1 gap-2 md:grid-cols-3">
          {activeSkills.map((s) => (
            <div key={s.id} className="rounded-lg border border-neutral-200 bg-white p-3">
              <div className="flex items-center gap-2">
                <span className="font-mono text-xs text-blue-600">{s.code}</span>
                <span className={`rounded px-1.5 py-0.5 text-xs ${STATUS_COLOR[s.status]}`}>
                  {s.status}
                </span>
              </div>
              <div className="mt-1 font-medium">{s.name}</div>
              <div className="mt-1 text-xs text-neutral-500">{s.description}</div>
              <div className="mt-1 text-[11px] text-neutral-400">
                {s.category} · v{s.version}
              </div>
            </div>
          ))}
          {activeSkills.length === 0 && (
            <div className="text-sm text-neutral-400">暂无已上架技能</div>
          )}
        </div>
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        {/* 技能管理 */}
        <div>
          <div className="mb-2 flex items-center gap-2">
            <span className="text-sm font-semibold">技能管理（{skills.length}）</span>
            <button
              onClick={() => setShowSkillForm(!showSkillForm)}
              className="text-xs text-blue-600 hover:underline"
            >
              ＋ 新建
            </button>
          </div>
          {showSkillForm && (
            <div className="mb-2 space-y-1 rounded-md border border-neutral-200 bg-white p-2">
              <div className="flex gap-2">
                <input
                  placeholder="code（如 data-qa）"
                  value={skillForm.code}
                  onChange={(e) => setSkillForm({ ...skillForm, code: e.target.value })}
                  className="flex-1 rounded border border-neutral-300 px-2 py-1 text-sm"
                />
                <input
                  placeholder="名称"
                  value={skillForm.name}
                  onChange={(e) => setSkillForm({ ...skillForm, name: e.target.value })}
                  className="flex-1 rounded border border-neutral-300 px-2 py-1 text-sm"
                />
              </div>
              <input
                placeholder="描述"
                value={skillForm.description}
                onChange={(e) => setSkillForm({ ...skillForm, description: e.target.value })}
                className="w-full rounded border border-neutral-300 px-2 py-1 text-sm"
              />
              <textarea
                placeholder="提示模板（用 {input} 占位用户输入）"
                value={skillForm.prompt_template}
                onChange={(e) => setSkillForm({ ...skillForm, prompt_template: e.target.value })}
                rows={2}
                className="w-full rounded border border-neutral-300 px-2 py-1 text-sm"
              />
              <button
                onClick={submitSkill}
                className="rounded bg-blue-600 px-3 py-1 text-xs text-white"
              >
                创建(draft)
              </button>
            </div>
          )}
          <div className="space-y-2">
            {skills.map((s) => (
              <div key={s.id} className="rounded-md border border-neutral-200 bg-white p-2 text-sm">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-mono text-xs text-blue-600">{s.code}</span>
                  <span className="font-medium">{s.name}</span>
                  <span
                    className={`rounded px-1.5 py-0.5 text-xs ${STATUS_COLOR[s.status] ?? "bg-neutral-100"}`}
                  >
                    {s.status}
                  </span>
                  {s.status === "draft" && (
                    <button
                      onClick={() => lifecycle(s.id, "submit")}
                      className="ml-auto text-xs text-amber-600 hover:underline"
                    >
                      提交评审
                    </button>
                  )}
                  {s.status === "pending_review" && (
                    <button
                      onClick={() => lifecycle(s.id, "approve")}
                      className="ml-auto text-xs text-emerald-600 hover:underline"
                    >
                      审批上架
                    </button>
                  )}
                  {(s.status === "active" || s.status === "pending_review") && (
                    <button
                      onClick={() => lifecycle(s.id, "offline")}
                      className="text-xs text-red-600 hover:underline"
                    >
                      下线
                    </button>
                  )}
                </div>
              </div>
            ))}
            {skills.length === 0 && <div className="text-sm text-neutral-400">暂无技能</div>}
          </div>
        </div>

        {/* APIKey + 用量 */}
        <div className="space-y-6">
          <div>
            <div className="mb-2 text-sm font-semibold">APIKey（{keys.length}）</div>
            {newKeySecret && (
              <div className="mb-2 rounded-md border border-amber-200 bg-amber-50 p-2 text-xs">
                <div className="font-medium text-amber-800">
                  ⚠️ 新 Key 明文仅此一次，请立即复制保存：
                </div>
                <code className="mt-1 block break-all rounded bg-white p-1">{newKeySecret}</code>
                <button
                  onClick={() => setNewKeySecret("")}
                  className="mt-1 text-blue-600 hover:underline"
                >
                  已保存
                </button>
              </div>
            )}
            <div className="mb-2 flex gap-2">
              <input
                placeholder="调用方应用名"
                value={keyForm.app_name}
                onChange={(e) => setKeyForm({ ...keyForm, app_name: e.target.value })}
                className="flex-1 rounded border border-neutral-300 px-2 py-1 text-sm"
              />
              <input
                placeholder="授权技能(逗号分隔,空=全部)"
                value={keyForm.allowed_skills}
                onChange={(e) => setKeyForm({ ...keyForm, allowed_skills: e.target.value })}
                className="flex-1 rounded border border-neutral-300 px-2 py-1 text-sm"
              />
              <button
                onClick={createKey}
                className="rounded bg-blue-600 px-3 py-1 text-xs text-white"
              >
                申请
              </button>
            </div>
            <div className="space-y-1">
              {keys.map((k) => (
                <div
                  key={k.id}
                  className="flex items-center gap-2 rounded-md border border-neutral-200 bg-white p-2 text-xs"
                >
                  <span className="font-mono">{k.key_prefix}</span>
                  <span className="text-neutral-500">{k.app_name}</span>
                  {k.allowed_skills && (
                    <span className="text-neutral-400">[{k.allowed_skills}]</span>
                  )}
                  <span
                    className={`rounded px-1.5 py-0.5 ${k.status === "active" ? "bg-emerald-100 text-emerald-700" : "bg-red-100 text-red-700"}`}
                  >
                    {k.status}
                  </span>
                  {k.status === "active" && (
                    <button
                      onClick={() => revokeKey(k.id)}
                      className="ml-auto text-red-600 hover:underline"
                    >
                      吊销
                    </button>
                  )}
                </div>
              ))}
              {keys.length === 0 && <div className="text-sm text-neutral-400">暂无 APIKey</div>}
            </div>
          </div>

          <div>
            <div className="mb-2 text-sm font-semibold">用量（按技能）</div>
            <div className="space-y-1">
              {usage.map((u) => (
                <div
                  key={u.skill_id}
                  className="flex items-center gap-2 rounded-md border border-neutral-200 bg-white p-2 text-xs"
                >
                  <span className="font-mono">{u.skill_id}</span>
                  <span className="text-neutral-500">{u.calls} 次</span>
                  <span className="text-neutral-400">{u.input_tokens + u.output_tokens} tok</span>
                  <span className="text-emerald-600">成功 {u.success_count}</span>
                </div>
              ))}
              {usage.length === 0 && <div className="text-sm text-neutral-400">暂无调用记录</div>}
            </div>
          </div>
        </div>
      </div>

      {/* 调用测试器（端到端） */}
      <div className="mt-6 rounded-lg border border-neutral-200 bg-white p-3">
        <div className="mb-2 text-sm font-semibold">
          🔍 调用测试器（端到端：APIKey → 网关 → 模型 → 计费）
        </div>
        <div className="space-y-2">
          <input
            placeholder="APIKey（申请后自动填入，或粘贴 sk_anp_...）"
            value={tester.apiKey}
            onChange={(e) => setTester({ ...tester, apiKey: e.target.value })}
            className="w-full rounded border border-neutral-300 px-2 py-1 font-mono text-xs"
          />
          <div className="flex gap-2">
            <select
              value={tester.skillCode}
              onChange={(e) => setTester({ ...tester, skillCode: e.target.value })}
              className="rounded border border-neutral-300 px-2 py-1 text-sm"
            >
              {["data-qa", "doc-gen"].map((c) => (
                <option key={c} value={c}>
                  {c}
                </option>
              ))}
              {activeSkills.map((s) => (
                <option key={s.id} value={s.code}>
                  {s.code}
                </option>
              ))}
            </select>
            <input
              placeholder="输入内容（如：本月发票异常有几张）"
              value={tester.input}
              onChange={(e) => setTester({ ...tester, input: e.target.value })}
              className="flex-1 rounded border border-neutral-300 px-2 py-1 text-sm"
            />
            <button
              onClick={invoke}
              disabled={invokeBusy || !tester.apiKey || !tester.input.trim()}
              className="rounded bg-blue-600 px-3 py-1 text-sm text-white disabled:opacity-50"
            >
              {invokeBusy ? "调用中…" : "调用"}
            </button>
          </div>
          {invokeResult && (
            <div className="rounded bg-neutral-50 p-2 text-sm whitespace-pre-wrap">
              {invokeResult}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

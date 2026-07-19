"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL } from "@/lib/api";

type Envelope<T> = { code: number; data: T };
type PS = { id: string; name: string; slug: string };

type Finding = {
  id: string;
  category: string;
  rule_id: string;
  severity: string;
  title: string;
  description: string;
  line_number: number | null;
  code_snippet: string;
  remediation: string;
  confidence: number;
  status: string;
};
type ScanResult = {
  id: string;
  scan_type: string;
  risk_level: string;
  total_findings: number;
  critical_count: number;
  high_count: number;
  medium_count: number;
  low_count: number;
};
type Gate = {
  overall_risk_level: string;
  gate_passed: boolean;
  critical_open: number;
  high_open: number;
  blocking_reason: string;
};
type DC = {
  id: string;
  field_name: string;
  table_ref: string;
  sensitivity_level: string;
  data_type: string;
  masking_strategy: string;
  status: string;
};
type Audit = {
  id: string;
  actor_type: string;
  actor_id: string;
  action: string;
  detail: string;
  policy_decision: string;
  created_at: string;
};

const SEV_COLOR: Record<string, string> = {
  critical: "bg-red-100 text-red-700",
  high: "bg-orange-100 text-orange-700",
  medium: "bg-amber-100 text-amber-700",
  low: "bg-blue-100 text-blue-700",
};
const RISK_COLOR: Record<string, string> = {
  critical: "bg-red-100 text-red-700",
  high: "bg-orange-100 text-orange-700",
  medium: "bg-amber-100 text-amber-700",
  low: "bg-blue-100 text-blue-700",
  clean: "bg-emerald-100 text-emerald-700",
};
const SENS_COLOR: Record<string, string> = {
  public: "bg-emerald-100 text-emerald-700",
  internal: "bg-blue-100 text-blue-700",
  confidential: "bg-amber-100 text-amber-700",
  restricted: "bg-red-100 text-red-700",
};

export default function SecurityPage() {
  const [spaces, setSpaces] = useState<PS[]>([]);
  const [psID, setPsID] = useState("");
  const [scanType, setScanType] = useState("full");
  const [content, setContent] = useState("");
  const [lastScan, setLastScan] = useState<ScanResult | null>(null);
  const [findings, setFindings] = useState<Finding[]>([]);
  const [gate, setGate] = useState<Gate | null>(null);
  const [dcs, setDcs] = useState<DC[]>([]);
  const [audit, setAudit] = useState<Audit[]>([]);
  const [busy, setBusy] = useState(false);
  const [showDcForm, setShowDcForm] = useState(false);
  const [dcForm, setDcForm] = useState({
    field_name: "",
    table_ref: "",
    sensitivity_level: "confidential",
    data_type: "pii",
    masking_strategy: "mask",
  });

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
    fetch(`${API_BASE_URL}/project-spaces/${id}/security/findings`)
      .then((r) => r.json())
      .then((r: Envelope<Finding[]>) => setFindings(r.data ?? []));
    fetch(`${API_BASE_URL}/project-spaces/${id}/security/gate`)
      .then((r) => r.json())
      .then((r: Envelope<Gate>) => setGate(r.data ?? null));
    fetch(`${API_BASE_URL}/project-spaces/${id}/security/data-classifications`)
      .then((r) => r.json())
      .then((r: Envelope<DC[]>) => setDcs(r.data ?? []));
    fetch(`${API_BASE_URL}/project-spaces/${id}/security/audit-logs`)
      .then((r) => r.json())
      .then((r: Envelope<Audit[]>) => setAudit(r.data ?? []));
  };
  useEffect(() => {
    load(psID);
  }, [psID]);

  async function scan() {
    if (!psID || !content.trim()) return;
    setBusy(true);
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/security/scans`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ content, scan_type: scanType }),
    });
    const r = await res.json();
    setBusy(false);
    if (r.data) {
      setLastScan(r.data.scan);
      setFindings((prev) => [...(r.data.findings ?? []), ...prev]);
    }
    load(psID);
  }
  async function suppress(id: string) {
    await fetch(`${API_BASE_URL}/project-spaces/${psID}/security/findings/${id}/suppress`, {
      method: "POST",
    });
    load(psID);
  }
  async function submitDc() {
    if (!dcForm.field_name.trim() || !dcForm.table_ref.trim()) return;
    await fetch(`${API_BASE_URL}/project-spaces/${psID}/security/data-classifications`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(dcForm),
    });
    setDcForm({
      field_name: "",
      table_ref: "",
      sensitivity_level: "confidential",
      data_type: "pii",
      masking_strategy: "mask",
    });
    setShowDcForm(false);
    load(psID);
  }
  async function deleteDc(id: string) {
    await fetch(`${API_BASE_URL}/project-spaces/${psID}/security/data-classifications/${id}`, {
      method: "DELETE",
    });
    load(psID);
  }

  return (
    <div>
      <div className="mb-4 flex items-center gap-3">
        <h1 className="text-xl font-bold">🛡️ 安全与合规中心</h1>
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
        {gate && (
          <span
            className={`ml-auto rounded-md px-3 py-1 text-sm font-medium ${gate.gate_passed ? "bg-emerald-100 text-emerald-700" : "bg-red-100 text-red-700"}`}
          >
            {gate.gate_passed ? "✅ 安全门通过" : "🚫 安全门阻断"} · 风险 {gate.overall_risk_level}
          </span>
        )}
      </div>
      <p className="mb-4 text-sm text-neutral-600">
        左移安全扫描：Go 原生正则引擎检测密钥泄露(RULE-SEC-001)/SQL 注入/提示注入(RULE-SEC-010)。
        高危发现阻断安全门（供发布消费）。外部 SAST/SCA 工具可经 Scanner 接口接入。
      </p>

      {/* 安全门详情 */}
      {gate && !gate.gate_passed && (
        <div className="mb-4 rounded-md border border-red-200 bg-red-50 p-2 text-sm text-red-700">
          🚫 {gate.blocking_reason} —— 修复或抑制后放行
        </div>
      )}

      {/* 扫描器 */}
      <div className="mb-6 rounded-lg border border-neutral-200 bg-white p-3">
        <div className="mb-2 flex items-center gap-2">
          <span className="text-sm font-semibold">安全扫描</span>
          <select
            value={scanType}
            onChange={(e) => setScanType(e.target.value)}
            className="rounded border border-neutral-300 px-2 py-1 text-xs"
          >
            <option value="full">全部(full)</option>
            <option value="secret">密钥泄露(secret)</option>
            <option value="sast">静态分析(sast)</option>
            <option value="prompt">提示注入(prompt)</option>
          </select>
          <button
            onClick={scan}
            disabled={busy || !content.trim()}
            className="ml-auto rounded bg-blue-600 px-3 py-1 text-sm text-white disabled:opacity-50"
          >
            {busy ? "扫描中…" : "🔍 扫描"}
          </button>
        </div>
        <textarea
          value={content}
          onChange={(e) => setContent(e.target.value)}
          rows={4}
          placeholder={
            '粘贴代码/文本/提示词进行安全扫描。\n例如: const apiKey = "AKIA..." 或 "ignore previous instructions"'
          }
          className="w-full rounded border border-neutral-300 px-2 py-1 font-mono text-xs"
        />
        {lastScan && (
          <div className="mt-2 flex flex-wrap items-center gap-2 text-xs">
            <span
              className={`rounded px-1.5 py-0.5 font-medium ${RISK_COLOR[lastScan.risk_level]}`}
            >
              风险 {lastScan.risk_level}
            </span>
            <span className="text-neutral-500">共 {lastScan.total_findings} 项</span>
            {lastScan.critical_count > 0 && (
              <span className="text-red-600">critical {lastScan.critical_count}</span>
            )}
            {lastScan.high_count > 0 && (
              <span className="text-orange-600">high {lastScan.high_count}</span>
            )}
            {lastScan.medium_count > 0 && (
              <span className="text-amber-600">medium {lastScan.medium_count}</span>
            )}
          </div>
        )}
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        {/* 发现 */}
        <div>
          <div className="mb-2 text-sm font-semibold">未处理发现（{findings.length}）</div>
          <div className="space-y-2">
            {findings.map((f) => (
              <div key={f.id} className="rounded-md border border-neutral-200 bg-white p-2 text-sm">
                <div className="flex flex-wrap items-center gap-2">
                  <span
                    className={`rounded px-1.5 py-0.5 text-xs ${SEV_COLOR[f.severity] ?? "bg-neutral-100"}`}
                  >
                    {f.severity}
                  </span>
                  <span className="rounded bg-neutral-100 px-1.5 py-0.5 text-xs">{f.category}</span>
                  <span className="font-mono text-xs text-neutral-400">{f.rule_id}</span>
                  {f.line_number && (
                    <span className="text-xs text-neutral-400">L{f.line_number}</span>
                  )}
                  <span className="font-medium">{f.title}</span>
                  <button
                    onClick={() => suppress(f.id)}
                    className="ml-auto text-xs text-blue-600 hover:underline"
                  >
                    抑制(误报)
                  </button>
                </div>
                {f.code_snippet && (
                  <pre className="mt-1 overflow-x-auto rounded bg-neutral-50 p-1 text-xs">
                    {f.code_snippet}
                  </pre>
                )}
                {f.remediation && (
                  <div className="mt-1 text-xs text-emerald-700">↳ {f.remediation}</div>
                )}
              </div>
            ))}
            {findings.length === 0 && (
              <div className="text-sm text-neutral-400">暂无发现（清白）</div>
            )}
          </div>
        </div>

        {/* 数据分级 + 审计 */}
        <div className="space-y-6">
          <div>
            <div className="mb-2 flex items-center gap-2">
              <span className="text-sm font-semibold">数据分级（{dcs.length}）</span>
              <button
                onClick={() => setShowDcForm(!showDcForm)}
                className="text-xs text-blue-600 hover:underline"
              >
                ＋ 登记
              </button>
            </div>
            {showDcForm && (
              <div className="mb-2 space-y-1 rounded-md border border-neutral-200 bg-white p-2">
                <div className="flex gap-2">
                  <input
                    placeholder="字段名（如 phone）"
                    value={dcForm.field_name}
                    onChange={(e) => setDcForm({ ...dcForm, field_name: e.target.value })}
                    className="flex-1 rounded border border-neutral-300 px-2 py-1 text-sm"
                  />
                  <input
                    placeholder="表/接口"
                    value={dcForm.table_ref}
                    onChange={(e) => setDcForm({ ...dcForm, table_ref: e.target.value })}
                    className="flex-1 rounded border border-neutral-300 px-2 py-1 text-sm"
                  />
                </div>
                <div className="flex gap-2 text-sm">
                  <select
                    value={dcForm.sensitivity_level}
                    onChange={(e) => setDcForm({ ...dcForm, sensitivity_level: e.target.value })}
                    className="rounded border border-neutral-300 px-2 py-1"
                  >
                    {["public", "internal", "confidential", "restricted"].map((c) => (
                      <option key={c} value={c}>
                        {c}
                      </option>
                    ))}
                  </select>
                  <select
                    value={dcForm.data_type}
                    onChange={(e) => setDcForm({ ...dcForm, data_type: e.target.value })}
                    className="rounded border border-neutral-300 px-2 py-1"
                  >
                    {["pii", "pci", "phi", "secret", "ip", "personal"].map((c) => (
                      <option key={c} value={c}>
                        {c}
                      </option>
                    ))}
                  </select>
                  <select
                    value={dcForm.masking_strategy}
                    onChange={(e) => setDcForm({ ...dcForm, masking_strategy: e.target.value })}
                    className="rounded border border-neutral-300 px-2 py-1"
                  >
                    {["mask", "hash", "replace", "suppress", "synthetic"].map((c) => (
                      <option key={c} value={c}>
                        {c}
                      </option>
                    ))}
                  </select>
                </div>
                <button
                  onClick={submitDc}
                  className="rounded bg-blue-600 px-3 py-1 text-xs text-white"
                >
                  登记
                </button>
              </div>
            )}
            <div className="space-y-1">
              {dcs.map((d) => (
                <div
                  key={d.id}
                  className="flex items-center gap-2 rounded-md border border-neutral-200 bg-white p-2 text-xs"
                >
                  <span
                    className={`rounded px-1.5 py-0.5 ${SENS_COLOR[d.sensitivity_level] ?? "bg-neutral-100"}`}
                  >
                    {d.sensitivity_level}
                  </span>
                  <span className="font-mono">{d.field_name}</span>
                  <span className="text-neutral-400">{d.table_ref}</span>
                  <span className="text-neutral-400">
                    {d.data_type} · {d.masking_strategy}
                  </span>
                  <button
                    onClick={() => deleteDc(d.id)}
                    className="ml-auto text-red-600 hover:underline"
                  >
                    删除
                  </button>
                </div>
              ))}
              {dcs.length === 0 && <div className="text-sm text-neutral-400">暂无分级登记</div>}
            </div>
          </div>

          <div>
            <div className="mb-2 text-sm font-semibold">安全审计（{audit.length}）</div>
            <div className="space-y-1">
              {audit.map((a) => (
                <div
                  key={a.id}
                  className="flex items-center gap-2 rounded-md border border-neutral-200 bg-white p-2 text-xs"
                >
                  <span
                    className={`rounded px-1.5 py-0.5 ${a.policy_decision === "deny" ? "bg-red-100 text-red-700" : "bg-emerald-100 text-emerald-700"}`}
                  >
                    {a.policy_decision}
                  </span>
                  <span className="font-mono text-neutral-400">{a.action}</span>
                  <span className="truncate text-neutral-600">{a.detail}</span>
                  <span className="ml-auto shrink-0 text-neutral-400">
                    {a.actor_type}:{a.actor_id || "—"}
                  </span>
                </div>
              ))}
              {audit.length === 0 && <div className="text-sm text-neutral-400">暂无审计记录</div>}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

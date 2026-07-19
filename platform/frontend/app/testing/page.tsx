"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL } from "@/lib/api";

type Envelope<T> = { code: number; data: T; message?: string };
type PS = { id: string; name: string; slug: string };
type Req = { id: string; title: string; application_id?: string };
type TC = {
  id: string;
  title: string;
  steps: string;
  expected: string;
  status: string;
  requirement_id: string;
  method?: string;
  path?: string;
  expected_status?: number;
  expected_body?: string;
  actual_status?: number;
  actual_body?: string;
  run_at?: string;
};

const STATUS_STYLE: Record<string, string> = {
  passed: "bg-emerald-100 text-emerald-700",
  failed: "bg-red-100 text-red-700",
  manual: "bg-amber-100 text-amber-700",
  draft: "bg-neutral-100 text-neutral-500",
};
const STATUS_LABEL: Record<string, string> = {
  passed: "✓ 通过",
  failed: "✗ 失败",
  manual: "⊙ 人工",
  draft: "未运行",
};

export default function TestingPage() {
  const [spaces, setSpaces] = useState<PS[]>([]);
  const [psID, setPsID] = useState("");
  const [reqs, setReqs] = useState<Req[]>([]);
  const [cases, setCases] = useState<TC[]>([]);
  const [genID, setGenID] = useState("");
  const [runID, setRunID] = useState("");
  const [runReqID, setRunReqID] = useState("");
  const [msg, setMsg] = useState("");

  useEffect(() => {
    fetch(`${API_BASE_URL}/project-spaces`)
      .then((r) => r.json())
      .then((r: Envelope<PS[]>) => {
        setSpaces(r.data ?? []);
        if (r.data?.[0]) setPsID(r.data[0].id);
      });
  }, []);

  const load = (id: string) => {
    if (!id) return;
    fetch(`${API_BASE_URL}/project-spaces/${id}/requirements`)
      .then((r) => r.json())
      .then((r: Envelope<Req[]>) => setReqs(r.data ?? []));
    fetch(`${API_BASE_URL}/project-spaces/${id}/test-cases`)
      .then((r) => r.json())
      .then((r: Envelope<TC[]>) => setCases(r.data ?? []));
  };
  useEffect(() => {
    load(psID);
  }, [psID]);

  async function generate(rid: string) {
    setGenID(rid);
    setMsg("");
    try {
      const res = await fetch(
        `${API_BASE_URL}/project-spaces/${psID}/requirements/${rid}/generate-tests`,
        { method: "POST" }
      );
      const r = await res.json();
      setMsg(
        r.code === 0
          ? `✓ 已生成 ${r.data?.length ?? 0} 条测试用例（含可执行 HTTP 检查）`
          : `✗ ${r.message}`
      );
      load(psID);
    } catch (e) {
      setMsg(String(e));
    } finally {
      setGenID("");
    }
  }

  async function runOne(tcid: string) {
    setRunID(tcid);
    setMsg("");
    try {
      const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/test-cases/${tcid}/run`, {
        method: "POST",
      });
      const r = await res.json();
      if (r.code === 0) {
        const tc: TC = r.data;
        setMsg(
          `「${tc.title}」→ ${STATUS_LABEL[tc.status] ?? tc.status}（实际 ${tc.actual_status || "—"}）`
        );
      } else {
        setMsg(`✗ ${r.message}`);
      }
      load(psID);
    } catch (e) {
      setMsg(`✗ ${e}`);
    } finally {
      setRunID("");
    }
  }

  async function runAll(rid: string) {
    setRunReqID(rid);
    setMsg("");
    try {
      const res = await fetch(
        `${API_BASE_URL}/project-spaces/${psID}/requirements/${rid}/run-tests`,
        { method: "POST" }
      );
      const r = await res.json();
      if (r.code === 0) {
        const d = r.data;
        setMsg(
          `批量验收完成：✓ ${d.passed} 通过 · ✗ ${d.failed} 失败 · ⊙ ${d.manual} 人工（共 ${d.total}）${d.base_url ? ` · 被测 ${d.base_url}` : " · 未归属已部署应用，HTTP 用例标为人工"}`
        );
      } else {
        setMsg(`✗ ${r.message}`);
      }
      load(psID);
    } catch (e) {
      setMsg(`✗ ${e}`);
    } finally {
      setRunReqID("");
    }
  }

  // 用例按需求分组，便于在每个需求块内"批量运行"。
  const casesByReq = new Map<string, TC[]>();
  cases.forEach((c) => {
    const k = c.requirement_id || "";
    if (!casesByReq.has(k)) casesByReq.set(k, []);
    casesByReq.get(k)!.push(c);
  });
  const reqMap = new Map(reqs.map((r) => [r.id, r]));

  return (
    <div>
      <h1 className="mb-1 text-xl font-bold">🧪 测试中心</h1>
      <p className="mb-4 text-sm text-neutral-600">
        AI 把需求验收标准转为可执行测试用例，并对着<b>已部署应用的 URL</b> 自动发请求验收（状态码 +
        响应体比对）。
      </p>

      <div className="mb-4 flex items-center gap-2">
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
      </div>

      {msg && <div className="mb-3 rounded-md bg-blue-50 p-2 text-sm text-blue-800">{msg}</div>}

      <div className="mb-5">
        <div className="mb-2 text-sm font-semibold">需求（生成用例 / 批量验收）</div>
        <div className="space-y-1">
          {reqs.map((r) => (
            <div
              key={r.id}
              className="flex items-center justify-between rounded-md border border-neutral-200 bg-white p-2 text-sm"
            >
              <div className="flex items-center gap-2">
                <span>{r.title}</span>
                {r.application_id ? (
                  <span className="rounded bg-purple-100 px-1.5 py-0.5 text-xs text-purple-700">
                    📦 已归属应用
                  </span>
                ) : (
                  <span className="rounded bg-neutral-100 px-1.5 py-0.5 text-xs text-neutral-500">
                    未归属应用
                  </span>
                )}
                {casesByReq.get(r.id)?.length ? (
                  <span className="text-xs text-neutral-400">
                    · {casesByReq.get(r.id)!.length} 用例
                  </span>
                ) : null}
              </div>
              <div className="flex gap-1">
                <button
                  onClick={() => generate(r.id)}
                  disabled={!!genID}
                  className="rounded bg-blue-600 px-2 py-1 text-xs text-white disabled:opacity-50"
                >
                  {genID === r.id ? "生成中…" : "生成测试用例"}
                </button>
                {casesByReq.get(r.id)?.length ? (
                  <button
                    onClick={() => runAll(r.id)}
                    disabled={!!runReqID}
                    className="rounded bg-emerald-600 px-2 py-1 text-xs text-white disabled:opacity-50"
                  >
                    {runReqID === r.id ? "验收中…" : "⚡ 批量验收"}
                  </button>
                ) : null}
              </div>
            </div>
          ))}
          {reqs.length === 0 && <div className="text-sm text-neutral-400">暂无需求</div>}
        </div>
      </div>

      <div>
        <div className="mb-2 text-sm font-semibold">测试用例（{cases.length}）</div>
        <div className="space-y-2">
          {cases.map((c) => {
            let steps: string[] = [];
            try {
              steps = JSON.parse(c.steps);
            } catch {}
            const hasHTTP = !!(c.method || c.path || c.expected_status || c.expected_body);
            return (
              <div key={c.id} className="rounded-md border border-neutral-200 bg-white p-3 text-sm">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <span className="font-medium">{c.title}</span>
                    <span
                      className={`rounded px-1.5 py-0.5 text-xs ${STATUS_STYLE[c.status] ?? "bg-neutral-100"}`}
                    >
                      {STATUS_LABEL[c.status] ?? c.status}
                    </span>
                  </div>
                  <button
                    onClick={() => runOne(c.id)}
                    disabled={!!runID}
                    className="rounded bg-neutral-800 px-2 py-1 text-xs text-white disabled:opacity-50"
                  >
                    {runID === c.id ? "运行中…" : "▶ 运行"}
                  </button>
                </div>

                {hasHTTP && (
                  <div className="mt-1.5 rounded bg-neutral-50 px-2 py-1 font-mono text-xs text-neutral-700">
                    <b className="text-blue-700">{c.method || "GET"}</b> {c.path || "/"}
                    {c.expected_status ? (
                      <>
                        {" "}
                        · 期望 <b>{c.expected_status}</b>
                      </>
                    ) : null}
                    {c.expected_body ? (
                      <>
                        {" "}
                        · 响应含 <b>"{c.expected_body}"</b>
                      </>
                    ) : null}
                  </div>
                )}

                {steps.length > 0 && (
                  <ol className="ml-5 mt-1 list-decimal text-xs text-neutral-500">
                    {steps.map((s, i) => (
                      <li key={i}>{s}</li>
                    ))}
                  </ol>
                )}

                {c.run_at && (
                  <div className="mt-1.5 rounded bg-neutral-50 px-2 py-1 text-xs">
                    <span className="text-neutral-400">最近运行：</span>
                    <b
                      className={
                        c.status === "passed"
                          ? "text-emerald-700"
                          : c.status === "failed"
                            ? "text-red-700"
                            : "text-amber-700"
                      }
                    >
                      实际 {c.actual_status || "—"}
                    </b>
                    {c.actual_body && (
                      <span className="ml-1 text-neutral-500">· {c.actual_body.slice(0, 160)}</span>
                    )}
                  </div>
                )}
                <div className="mt-1 text-[11px] text-neutral-300">
                  归属：{reqMap.get(c.requirement_id)?.title ?? c.requirement_id}
                </div>
              </div>
            );
          })}
          {cases.length === 0 && (
            <div className="text-sm text-neutral-400">
              暂无测试用例（先对某需求「生成测试用例」）
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL } from "@/lib/api";

type Envelope<T> = { code: number; data: T; message?: string };
type PS = { id: string; name: string; slug: string };
type Req = { id: string; title: string };
type TC = {
  id: string;
  title: string;
  steps: string;
  expected: string;
  status: string;
  requirement_id: string;
};

export default function TestingPage() {
  const [spaces, setSpaces] = useState<PS[]>([]);
  const [psID, setPsID] = useState("");
  const [reqs, setReqs] = useState<Req[]>([]);
  const [cases, setCases] = useState<TC[]>([]);
  const [genID, setGenID] = useState("");
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
      setMsg(r.code === 0 ? `✓ 已生成 ${r.data?.length ?? 0} 条测试用例` : `✗ ${r.message}`);
      load(psID);
    } catch (e) {
      setMsg(String(e));
    } finally {
      setGenID("");
    }
  }

  return (
    <div>
      <h1 className="mb-1 text-xl font-bold">🧪 测试中心</h1>
      <p className="mb-4 text-sm text-neutral-600">AI 把需求验收标准自动转为可执行测试用例。</p>

      <div className="mb-4">
        <label className="text-xs text-neutral-500">项目空间</label>
        <select
          value={psID}
          onChange={(e) => setPsID(e.target.value)}
          className="ml-2 rounded-md border border-neutral-300 px-2 py-1 text-sm"
        >
          {spaces.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name} ({s.slug})
            </option>
          ))}
        </select>
      </div>

      {msg && <div className="mb-3 text-sm text-blue-700">{msg}</div>}

      <div className="mb-5">
        <div className="mb-2 text-sm font-semibold">需求（点「生成测试用例」）</div>
        <div className="space-y-1">
          {reqs.map((r) => (
            <div key={r.id} className="flex items-center justify-between rounded-md border border-neutral-200 bg-white p-2 text-sm">
              <span>{r.title}</span>
              <button
                onClick={() => generate(r.id)}
                disabled={!!genID}
                className="rounded bg-blue-600 px-2 py-1 text-xs text-white disabled:opacity-50"
              >
                {genID === r.id ? "生成中…" : "生成测试用例"}
              </button>
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
            return (
              <div key={c.id} className="rounded-md border border-neutral-200 bg-white p-3 text-sm">
                <div className="font-medium">{c.title}</div>
                <ol className="ml-5 list-decimal text-xs text-neutral-600">
                  {steps.map((s, i) => (
                    <li key={i}>{s}</li>
                  ))}
                </ol>
                <div className="mt-1 text-xs text-neutral-500">预期：{c.expected}</div>
              </div>
            );
          })}
          {cases.length === 0 && <div className="text-sm text-neutral-400">暂无测试用例</div>}
        </div>
      </div>
    </div>
  );
}

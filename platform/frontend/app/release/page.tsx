"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL } from "@/lib/api";
import { FlowStepper } from "../_components/stepper";

type Envelope<T> = { code: number; data: T; message?: string };
type PS = { id: string; name: string; slug: string };
type Change = {
  id: string;
  kind: string;
  prompt: string;
  status: string;
  app_name: string;
  created_at: string;
};
type Rel = { id: string; change_id: string; version: string; status: string; created_at: string };

export default function ReleasePage() {
  const [spaces, setSpaces] = useState<PS[]>([]);
  const [psID, setPsID] = useState("");
  const [approved, setApproved] = useState<Change[]>([]);
  const [releases, setReleases] = useState<Rel[]>([]);
  const [msg, setMsg] = useState("");
  const [gateOn, setGateOn] = useState(false); // 发布测试门禁开关

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
    fetch(`${API_BASE_URL}/changes?status=approved`)
      .then((r) => r.json())
      .then((r: Envelope<Change[]>) => setApproved(r.data ?? []));
    fetch(`${API_BASE_URL}/project-spaces/${id}/releases`)
      .then((r) => r.json())
      .then((r: Envelope<Rel[]>) => setReleases(r.data ?? []));
  };
  useEffect(() => {
    load(psID);
  }, [psID]);

  // 读发布测试门禁开关（系统配置 release_require_passed_test）
  useEffect(() => {
    fetch(`${API_BASE_URL}/config`)
      .then((r) => r.json())
      .then((r: Envelope<{ key: string; value: string }[]>) => {
        const it = (r.data ?? []).find((x) => x.key === "release_require_passed_test");
        setGateOn(it?.value === "true");
      })
      .catch(() => {});
  }, []);

  async function release(changeID: string) {
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/releases`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ change_id: changeID }),
    });
    const r = await res.json();
    setMsg(
      r.code === 0
        ? `✓ 已发布 ${r.data?.version} 🎉  下一步：去「🛠️ 运维中心」监控运行 / 回「💬 需求工作台」提下一个需求`
        : `✗ ${r.message}`
    );
    load(psID);
  }

  return (
    <div>
      <h1 className="mb-1 text-xl font-bold">🚀 发布中心</h1>
      <FlowStepper current={3} />
      <p className="mb-4 text-sm text-neutral-600">
        已审批（🚪G3 通过）的变更发布上线，版本号自增。
      </p>

      {gateOn ? (
        <div className="mb-4 rounded-md border border-amber-300 bg-amber-50 p-2 text-xs text-amber-800">
          🧪 <b>测试门禁已开启</b>：发布前要求来源需求至少 1 条 <b>passed</b>{" "}
          测试用例，否则将被拦截。先到「测试中心」生成并运行用例；或在「系统配置」关闭{" "}
          <code>release_require_passed_test</code>。
        </div>
      ) : (
        <div className="mb-4 rounded-md border border-neutral-200 bg-neutral-50 p-2 text-xs text-neutral-500">
          🧪 测试门禁关闭中（可在「系统配置」开启 <code>release_require_passed_test</code>
          ，让发布要求 passed 用例）。
        </div>
      )}

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
        <div className="mb-2 text-sm font-semibold">待发布（已审批变更）</div>
        <div className="space-y-1">
          {approved.map((c) => (
            <div
              key={c.id}
              className="flex items-center justify-between rounded-md border border-neutral-200 bg-white p-2 text-sm"
            >
              <div className="min-w-0">
                <span className="font-medium text-neutral-800">
                  {c.app_name || c.id.slice(0, 12)}
                </span>
                <span className="ml-2 font-mono text-[10px] text-neutral-400">
                  {c.id.slice(0, 12)}
                </span>
                {c.created_at && (
                  <span className="ml-2 text-xs text-neutral-400">
                    📅 {new Date(c.created_at).toLocaleString("zh-CN", { hour12: false })}
                  </span>
                )}
              </div>
              <button
                onClick={() => release(c.id)}
                className="rounded bg-emerald-600 px-2 py-1 text-xs text-white"
              >
                发布
              </button>
            </div>
          ))}
          {approved.length === 0 && (
            <div className="text-sm text-neutral-400">暂无已审批变更（先在「变更审批」批准）</div>
          )}
        </div>
      </div>

      <div>
        <div className="mb-2 text-sm font-semibold">发布历史（{releases.length}）</div>
        <div className="space-y-1">
          {releases.map((r) => (
            <div
              key={r.id}
              className="flex items-center gap-3 rounded-md border border-neutral-200 bg-white p-2 text-sm"
            >
              <span className="rounded bg-emerald-100 px-2 py-0.5 text-xs text-emerald-700">
                {r.version}
              </span>
              <span className="text-xs text-neutral-400">{r.status}</span>
              {r.created_at && (
                <span className="ml-auto text-xs text-neutral-400">
                  📅 {new Date(r.created_at).toLocaleString("zh-CN", { hour12: false })}
                </span>
              )}
            </div>
          ))}
          {releases.length === 0 && <div className="text-sm text-neutral-400">暂无发布</div>}
        </div>
      </div>
    </div>
  );
}

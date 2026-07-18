"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useSearchParams } from "next/navigation";
import { API_BASE_URL } from "@/lib/api";
import { WorkspaceToolbar, type DeployState } from "./workspace-toolbar";
import { ContextDrawer, type WorkspaceDetail } from "./context-drawer";

// 编码工作台 tab 主体(期1 载体):
// - 左侧 ContextDrawer:项目上下文(需求/变更/发布,来自 /detail)
// - 顶部 WorkspaceToolbar:构建部署(test)+ 部署状态轮询 + opencode 新窗口/重连
// - 主体:opencode 全屏 iframe
// 后续期2(变更闸门)/期3(需求申请单)等治理功能在本组件呈现。
//
// 注意:effect 内不同步 setState(react-hooks/set-state-in-effect)——
//   抽屉开关用 lazy initializer 读 localStorage;setState 都在 fetch/事件/轮询回调里。
export default function WorkspaceFrame() {
  const sp = useSearchParams();
  const appID = sp.get("app") || "";
  const psID = sp.get("ps") || "";
  const tool = sp.get("tool") || "opencode";
  const missingParams = !appID || !psID;

  const [url, setUrl] = useState("");
  const [err, setErr] = useState("");
  const [loading, setLoading] = useState(true);
  const [reloadKey, setReloadKey] = useState(0);

  const [detail, setDetail] = useState<WorkspaceDetail | null>(null);
  const [detailErr, setDetailErr] = useState("");

  // 抽屉开关:lazy initializer 读 localStorage,避免 effect 内同步 setState
  const [drawerOpen, setDrawerOpen] = useState<boolean>(() => {
    if (typeof window === "undefined") return true;
    const s = window.localStorage.getItem("anp.workspace.drawer");
    return s === null ? true : s === "1";
  });

  const [deployState, setDeployState] = useState<DeployState>("idle");
  const [testUrl, setTestUrl] = useState("");
  const [deployErr, setDeployErr] = useState("");
  const [registering, setRegistering] = useState(false);
  const [selectedReq, setSelectedReq] = useState(""); // 当前驱动开发的需求
  const [dispatching, setDispatching] = useState(false);
  const [taskMsg, setTaskMsg] = useState("");
  const [testing, setTesting] = useState(false);
  const [testMsg, setTestMsg] = useState("");
  const [testResults, setTestResults] = useState<{ method?: string; path?: string; expected_status?: number; actual_status?: number }[] | null>(null);
  const [subtasks, setSubtasks] = useState<{ text: string; done: boolean }[]>([]);
  const [breaking, setBreaking] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [submitMsg, setSubmitMsg] = useState("");
  const [merging, setMerging] = useState(false);

  // 部署状态轮询句柄(卸载时清理)
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);
  useEffect(() => () => { if (pollRef.current) clearInterval(pollRef.current); }, []);

  function toggleDrawer() {
    setDrawerOpen((v) => {
      const nv = !v;
      try { window.localStorage.setItem("anp.workspace.drawer", nv ? "1" : "0"); } catch {}
      return nv;
    });
  }

  // 拉项目上下文 + 应用状态(抽屉与部署轮询共用);返回完整 detail 供轮询判状态
  const fetchDetail = useCallback(async (): Promise<{ application?: { instances?: { env: string; status: string; url: string }[]; last_error?: string } } | null> => {
    try {
      const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/detail`);
      const r = await res.json();
      if (r.code === 0 && r.data) {
        setDetail({ requirements: r.data.requirements, changes: r.data.changes, releases: r.data.releases });
        setDetailErr("");
        return r.data;
      }
      setDetailErr(r.message || "加载失败");
      return null;
    } catch (e) {
      setDetailErr(String(e));
      return null;
    }
  }, [psID, appID]);

  // 首次加载上下文(setState 在 fetch 回调里,非 effect 同步,符合 set-state-in-effect)
  useEffect(() => {
    if (missingParams) return;
    let aborted = false;
    fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/detail`)
      .then((r) => r.json())
      .then((r) => {
        if (aborted) return;
        if (r.code === 0 && r.data) {
          setDetail({ requirements: r.data.requirements, changes: r.data.changes, releases: r.data.releases });
          setDetailErr("");
        } else {
          setDetailErr(r.message || "加载失败");
        }
      })
      .catch((e) => { if (!aborted) setDetailErr(String(e)); });
    return () => { aborted = true; };
  }, [missingParams, psID, appID]);

  // 拉起 opencode 工作台
  useEffect(() => {
    if (missingParams) return;
    let aborted = false;
    fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/workspace`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ tool }),
    })
      .then((r) => r.json())
      .then((r) => {
        if (aborted) return;
        if (r.code === 0 && r.data?.url) {
          setUrl(r.data.deep_url || r.data.url);
          setErr("");
        } else {
          setErr(r.message || "启动编码工作台失败");
        }
        setLoading(false);
      })
      .catch((e) => {
        if (!aborted) { setErr(String(e)); setLoading(false); }
      })
      .finally(() => {
        if (!aborted) setLoading(false);
      });
    return () => { aborted = true; };
  }, [appID, psID, tool, reloadKey, missingParams]);

  // 构建部署到 test,轮询 test 实例状态直到 running/failed(~2min 超时)
  async function deploy() {
    if (pollRef.current) clearInterval(pollRef.current);
    setDeployState("building");
    setDeployErr("");
    setTestUrl("");
    try {
      const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/deploy`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ env: "test" }),
      });
      const r = await res.json();
      if (r.code !== 0) {
        setDeployState("failed");
        setDeployErr(r.message || "部署失败");
        return;
      }
    } catch (e) {
      setDeployState("failed");
      setDeployErr(String(e));
      return;
    }
    let n = 0;
    pollRef.current = setInterval(async () => {
      n += 1;
      const d = await fetchDetail();
      const ins = d?.application?.instances?.find((i) => i.env === "test");
      if (ins?.status === "running") {
        setTestUrl(ins.url);
        setDeployState("running");
        if (pollRef.current) clearInterval(pollRef.current);
      } else if (ins?.status === "failed") {
        setDeployErr(d?.application?.last_error || "构建失败");
        setDeployState("failed");
        if (pollRef.current) clearInterval(pollRef.current);
      }
      if (n > 40 && pollRef.current) clearInterval(pollRef.current); // ~2min
    }, 3000);
  }

  // 登记变更:后端自动从 opencode 对话总结变更说明(免手填),刷新抽屉看 pending 变更。
  async function registerChange() {
    setRegistering(true);
    try {
      const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/register-change`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ req_id: selectedReq }),
      });
      const r = await res.json();
      if (r.code !== 0) {
        alert(r.message || "登记失败");
      } else {
        await fetchDetail();
      }
    } catch (e) {
      alert(String(e));
    }
    setRegistering(false);
  }

  // 变更审批（approve/reject）：pending 变更可直接在抽屉审批
  async function decideChange(id: string, decision: "approve" | "reject") {
    try {
      const res = await fetch(`${API_BASE_URL}/changes/${id}/${decision}`, { method: "POST" });
      const r = await res.json();
      if (r.code !== 0) {
        alert(r.message);
        return;
      }
      await fetchDetail();
    } catch (e) {
      alert(String(e));
    }
  }

  // 需求驱动:把需求规格注入 opencode 会话,AI 在工作台实时编码(看过程,可介入)。
  async function dispatchReq(taskIdx?: number) {
    if (!selectedReq) return;
    const req = detail?.requirements?.find((q) => q.id === selectedReq);
    if (!req) { setTaskMsg("需求不存在"); return; }
    // 按子任务逐个:指定 taskIdx 或下一个未完成;没拆解则整个需求
    const next = taskIdx !== undefined ? subtasks[taskIdx] : subtasks.find((t) => !t.done);
    const prompt = next
      ? `当前在实现需求「${req.title}」。\n【严格·只做这一步】\n  👉 ${next.text}\n做完这一步就停,等我确认再做下一个。\n【禁止】不要做其他子任务、不要扩展范围、不要重构无关代码,只完成上面这一步。\n【方式】基于现有代码增量(先读 server.js/index.html/package.json 等再改),不重写已有功能。\n需求背景:${req.description || req.user_story || ""}`
      : `请按以下需求规格实现/修改代码。\n【重要·必须遵守】本应用已有代码,你不能从零重写:\n1. 第一步先用读文件工具读现有代码:README.md、docs/ 下文档、主要代码文件(server.js / index.html / package.json / Dockerfile 等),完整理解当前实现;\n2. 在现有代码基础上**增量修改/扩展**——只新增或修改实现本需求所需的部分,绝不删除或重写已有功能;\n3. 保持现有文件结构与技术栈,不另起炉灶。\n\n需求规格:\n标题:${req.title}\n用户故事:${req.user_story || "(无)"}\n验收标准:${req.acceptance_criteria || "(无)"}\n描述:${req.description || ""}`;
    setDispatching(true);
    setTaskMsg(next ? `发送子任务给 opencode:${next.text}` : "把需求发给 opencode…");
    try {
      const r = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/inject-requirement`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ prompt }),
      }).then((rr) => rr.json());
      if (r.code !== 0) { setTaskMsg(r.message || "失败"); setDispatching(false); return; }
      setTaskMsg(next
        ? `✅ 已发送子任务: ${next.text}\n做完后在左侧 checklist 打勾,再点「🤖AI编码」做下一个`
        : "✅ 需求已发给 opencode → 在右侧工作台看 AI 实时编码,可随时介入/纠偏");
    } catch (e) {
      setTaskMsg(String(e));
    }
    setDispatching(false);
  }

  // 自动测试:当前需求 → AI 按验收标准生成用例 + 批量对着应用 URL 验收,显示通过/失败。
  async function runAutoTest() {
    if (!selectedReq) { alert("先在左侧选一个需求"); return; }
    setTesting(true);
    setTestMsg("生成测试用例…");
    setTestResults(null);
    try {
      let r = await fetch(`${API_BASE_URL}/project-spaces/${psID}/requirements/${selectedReq}/generate-tests`, { method: "POST" }).then((rr) => rr.json());
      if (r.code !== 0) { setTestMsg(r.message || "生成用例失败"); setTesting(false); return; }
      setTestMsg("运行自动验收…(需先构建部署 test)");
      r = await fetch(`${API_BASE_URL}/project-spaces/${psID}/requirements/${selectedReq}/run-tests`, { method: "POST" }).then((rr) => rr.json());
      if (r.code !== 0) { setTestMsg(r.message || "运行失败"); setTesting(false); return; }
      const list: { method?: string; path?: string; expected_status?: number; actual_status?: number }[] = r.data ?? [];
      setTestResults(list);
      const passed = list.filter((x) => x.actual_status === x.expected_status).length;
      setTestMsg(`测试完成:${passed}/${list.length} 通过`);
    } catch (e) {
      setTestMsg(String(e));
    }
    setTesting(false);
  }

  // AI 拆解当前需求→子任务 checklist(逐项打勾,引导按需求开发)
  async function breakdownReq() {
    if (!selectedReq) return;
    setBreaking(true);
    try {
      const r = await fetch(`${API_BASE_URL}/project-spaces/${psID}/requirements/${selectedReq}/breakdown`, { method: "POST" }).then((rr) => rr.json());
      if (r.code !== 0) { alert(r.message || "拆解失败"); setBreaking(false); return; }
      try { setSubtasks(JSON.parse(r.data?.tasks || "[]")); } catch { setSubtasks([]); }
    } catch (e) { alert(String(e)); }
    setBreaking(false);
  }

  // 合并 dev-<user> 到 main(上线前;worktree 模式必要)。
  async function mergeReq() {
    setMerging(true);
    try {
      const r = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/merge`, { method: "POST" }).then((rr) => rr.json());
      if (r.code !== 0) { alert(r.message || "合并失败"); setMerging(false); return; }
      alert("✅ 已合并到主线 main,可点「🚀上线」");
    } catch (e) { alert(String(e)); }
    setMerging(false);
  }

  // 提交核对门禁:AI 核对代码 vs 需求验收标准,不匹配拦(列差异),匹配放行。
  async function submitReq() {
    if (!selectedReq) { alert("先选需求"); return; }
    const req = detail?.requirements?.find((q) => q.id === selectedReq);
    if (!req) { return; }
    setSubmitting(true);
    setSubmitMsg("AI 核对代码 vs 需求验收标准…");
    try {
      const r = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/submit`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ title: req.title, acceptance_criteria: req.acceptance_criteria || "" }),
      }).then((rr) => rr.json());
      if (r.code !== 0) {
        setSubmitMsg("❌ 核对未通过,请按差异修正:\n" + (r.message || ""));
        setSubmitting(false);
        return;
      }
      setSubmitMsg("✅ 核对通过,可点「📝登记变更」提交");
    } catch (e) {
      setSubmitMsg(String(e));
    }
    setSubmitting(false);
  }

  const showErr = missingParams ? "缺少 app/ps 参数（请从应用卡片点「编码」进入）" : err;

  return (
    <div className="-m-4 flex h-[calc(100vh-2.25rem)] flex-col md:-m-6">
      <WorkspaceToolbar
        appID={appID}
        appName={detail?.application?.name}
        tool={tool}
        deployState={deployState}
        testUrl={testUrl}
        deployErr={deployErr}
        onDeploy={deploy}
        onRegister={registerChange}
        registering={registering}
        onOpenWindow={() => { if (url) window.open(url, "_blank"); }}
        onReconnect={() => { setUrl(""); setReloadKey((k) => k + 1); }}
        drawerOpen={drawerOpen}
        onToggleDrawer={toggleDrawer}
      />
      {selectedReq && (() => {
        const req = detail?.requirements?.find((q) => q.id === selectedReq);
        if (!req) return null;
        return (
          <div className="border-b border-blue-200 bg-blue-50 px-3 py-1 text-xs">
            <div className="flex items-center gap-2">
              <span className="truncate font-medium text-blue-700">🎯 当前需求:{req.title}</span>
              <button
                onClick={() => dispatchReq()}
                disabled={dispatching}
                className="shrink-0 rounded bg-blue-600 px-2 py-0.5 text-white"
                title="AI 按此需求规格自动编码,完成后你可协助修正"
              >
                {dispatching ? "编码中…" : "🤖 AI 编码此需求"}
              </button>
              <button
                onClick={runAutoTest}
                disabled={testing}
                className="shrink-0 rounded bg-emerald-100 px-2 py-0.5 text-emerald-700"
                title="AI 按需求验收标准生成用例 + 对着应用 URL 自动验收"
              >
                {testing ? "测试中…" : "🧪 自动测试"}
              </button>
              <button
                onClick={breakdownReq}
                disabled={breaking}
                className="shrink-0 rounded bg-purple-100 px-2 py-0.5 text-purple-700"
                title="AI 把需求拆成子任务清单,逐项打勾推进"
              >
                {breaking ? "拆解中…" : "📋 拆解子任务"}
              </button>
              <button
                onClick={submitReq}
                disabled={submitting}
                className="shrink-0 rounded bg-amber-600 px-2 py-0.5 text-white"
                title="提交前 AI 核对代码是否实现需求验收标准,不匹配会被拦"
              >
                {submitting ? "核对中…" : "🔒 提交核对"}
              </button>
              <button
                onClick={mergeReq}
                disabled={merging}
                className="shrink-0 rounded bg-emerald-700 px-2 py-0.5 text-white"
                title="合并 dev-<你> 到主线 main(worktree 模式上线前必做)"
              >
                {merging ? "合并中…" : "🔀 合并主线"}
              </button>
              <button onClick={() => { setSelectedReq(""); setTaskMsg(""); setTestMsg(""); setTestResults(null); setSubtasks([]); setSubmitMsg(""); }} className="shrink-0 text-neutral-400">✕</button>
            </div>
            {taskMsg && <div className="mt-0.5 whitespace-pre-wrap text-blue-600">{taskMsg}</div>}
            {submitMsg && <div className="mt-0.5 whitespace-pre-wrap text-amber-700">{submitMsg}</div>}
            {testMsg && <div className="mt-0.5 text-emerald-700">{testMsg}</div>}
            {testResults && testResults.length > 0 && (
              <div className="mt-1 space-y-0.5">
                {testResults.map((tc, i) => (
                  <div key={i} className={tc.actual_status === tc.expected_status ? "text-emerald-700" : "text-red-600"}>
                    {tc.actual_status === tc.expected_status ? "✅" : "❌"} {tc.method} {tc.path} → {tc.actual_status || "(未跑)"}
                  </div>
                ))}
              </div>
            )}
            {subtasks.length > 0 && (
              <div className="mt-1 space-y-0.5">
                {subtasks.map((t, i) => (
                  <div key={i} className="flex items-center gap-1">
                    <input
                      type="checkbox"
                      checked={t.done}
                      onChange={() => setSubtasks((prev) => prev.map((x, j) => (j === i ? { ...x, done: !x.done } : x)))}
                    />
                    <span className={`flex-1 ${t.done ? "line-through text-neutral-400" : "text-neutral-700"}`}>{t.text}</span>
                    {!t.done && (
                      <button onClick={() => dispatchReq(i)} className="shrink-0 text-blue-600" title="让 AI 做这一步">▶</button>
                    )}
                  </div>
                ))}
              </div>
            )}
          </div>
        );
      })()}
      <div className="flex min-h-0 flex-1">
        {drawerOpen && !missingParams && (
          <ContextDrawer
            detail={detail}
            loading={!detail && !detailErr}
            err={detailErr}
            onClose={toggleDrawer}
            onApprove={(id) => decideChange(id, "approve")}
            onReject={(id) => decideChange(id, "reject")}
            psID={psID}
            appID={appID}
            selectedReq={selectedReq}
            onStartReq={async (id) => {
              // 认领(互斥):被他人认领会 409 拒绝
              try {
                const r = await fetch(`${API_BASE_URL}/project-spaces/${psID}/requirements/${id}/assign`, { method: "POST" }).then((rr) => rr.json());
                if (r.code !== 0) { alert(r.message || "认领失败"); return; }
              } catch (e) { alert(String(e)); return; }
              setSelectedReq(id); setTaskMsg(""); setTestMsg(""); setTestResults(null); setSubmitMsg("");
              try { setSubtasks(JSON.parse(detail?.requirements?.find((q) => q.id === id)?.tasks || "[]")); } catch { setSubtasks([]); }
              fetchDetail();
            }}
          />
        )}
        <div className="flex min-h-0 flex-1 flex-col">
          {loading && !missingParams && (
            <div className="p-4 text-sm text-neutral-500">启动 opencode 工作台…（首次约 3-5 秒）</div>
          )}
          {showErr && !url && <div className="p-4 text-sm text-red-600">{showErr}</div>}
          {url && <iframe src={url} className="min-h-0 flex-1" title="opencode 编码工作台" />}
        </div>
      </div>
    </div>
  );
}

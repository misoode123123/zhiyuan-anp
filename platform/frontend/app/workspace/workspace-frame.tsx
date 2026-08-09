"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useSearchParams } from "next/navigation";
import { API_BASE_URL } from "@/lib/api";
import { WorkspaceToolbar, type DeployState } from "./workspace-toolbar";
import { Sidebar } from "./sidebar";
import type { WorkspaceDetail, ReqState, ReqActions } from "./types";

// 编码工作台 tab 主体(期1 载体):
// - 左侧 Sidebar:活动栏 + 需求/源代码管理/发布/文件视图 + diff 抽屉（数据来自 /detail 与 /git-status）
// - 顶部 WorkspaceToolbar:构建部署(test)+ 部署状态轮询 + opencode 新窗口/重连
// - 主体:opencode 全屏 iframe
// 后续期2(变更闸门)/期3(需求申请单)等治理功能在本组件呈现。
//
// 注意:effect 内不同步 setState(react-hooks/set-state-in-effect)——
//   抽屉开关用 lazy initializer 读 localStorage;setState 都在 fetch/事件/轮询回调里。
// 构建注入 opencode 的需求 prompt（纯函数：boot 即时注入 与 dispatchReq 手动注入 共用，避免模板漂移）。
// next=undefined → 注入完整需求规格（新会话/手动编码）；next 非空 → 只注入单步子任务（拆解后逐项）。
function buildReqPrompt(
  req: { title: string; user_story?: string; acceptance_criteria?: string; description?: string },
  next?: { text: string }
): string {
  if (next) {
    return `当前在实现需求「${req.title}」。\n【严格·只做这一步】\n  👉 ${next.text}\n做完这一步就停,等我确认再做下一个。\n【禁止】不要做其他子任务、不要扩展范围、不要重构无关代码,只完成上面这一步。\n【方式】基于现有代码增量(先读 server.js/index.html/package.json 等再改),不重写已有功能。\n需求背景:${req.description || req.user_story || ""}`;
  }
  return `请按以下需求规格实现/修改代码。\n【重要·必须遵守】本应用已有代码,你不能从零重写:\n1. 第一步先用读文件工具读现有代码:README.md、docs/ 下文档、主要代码文件(server.js / index.html / package.json / Dockerfile 等),完整理解当前实现;\n2. 在现有代码基础上**增量修改/扩展**——只新增或修改实现本需求所需的部分,绝不删除或重写已有功能;\n3. 保持现有文件结构与技术栈,不另起炉灶。\n\n需求规格:\n标题:${req.title}\n用户故事:${req.user_story || "(无)"}\n验收标准:${req.acceptance_criteria || "(无)"}\n描述:${req.description || ""}`;
}

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

  // force_new：点「🆕 新会话」时置 true，boot effect 读取后立即复位；
  // newSessionKey：变更即触发 boot effect 重跑（与 reloadKey 同机制），透传 force_new。
  const forceNewRef = useRef(false);
  const [newSessionKey, setNewSessionKey] = useState(0);

  // 当前用户授权的编码模型（cmd_xxx）；空=未选/未授权，后端兜底用全局 config。
  const [model, setModel] = useState("");
  // model 固定在 session boot 时读取。ModelSelect 异步 seed model，若把 model 加入 boot
  // effect 的 deps 会 double-boot，与后端「复用存活会话不重注 model」相撞。故用 ref 暂存
  // 最新值，boot 读 ref.current；改 model 后点「重连」(reloadKey++) 重新 boot 才生效。
  // 用独立 effect 写 ref（而非渲染期写），满足 react-hooks/refs；不影响 boot effect deps。
  const modelRef = useRef(model);
  useEffect(() => {
    modelRef.current = model;
  }, [model]);

  const [detail, setDetail] = useState<WorkspaceDetail | null>(null);
  // 需求列表用 ref 暂存（仿 modelRef）：boot effect 读 reqsRef.current 拼 bootPrompt，
  // 不把 detail?.requirements 加进 boot deps——否则 detail 刷新(状态轮询)会触发重 boot。
  const reqsRef = useRef(detail?.requirements);
  useEffect(() => {
    reqsRef.current = detail?.requirements;
  }, [detail?.requirements]);
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
  const [testResults, setTestResults] = useState<
    { method?: string; path?: string; expected_status?: number; actual_status?: number }[] | null
  >(null);
  const [subtasks, setSubtasks] = useState<{ text: string; done: boolean }[]>([]);
  const [breaking, setBreaking] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [submitMsg, setSubmitMsg] = useState("");
  const [merging, setMerging] = useState(false);

  // 部署状态轮询句柄(卸载时清理)
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);
  useEffect(
    () => () => {
      if (pollRef.current) clearInterval(pollRef.current);
    },
    []
  );

  function toggleDrawer() {
    setDrawerOpen((v) => {
      const nv = !v;
      try {
        window.localStorage.setItem("anp.workspace.drawer", nv ? "1" : "0");
      } catch {}
      return nv;
    });
  }

  // 拉项目上下文 + 应用状态(抽屉与部署轮询共用);返回完整 detail 供轮询判状态
  const fetchDetail = useCallback(async (): Promise<{
    application?: { last_error?: string };
    // 后端 AppDetail(detail.go:88)把 instances 放在顶层 r.data.instances，
    // 不在 r.data.application 下（Application.Instances 是 db:"-" 恒空）。
    instances?: { env: string; status: string; url: string }[];
  } | null> => {
    try {
      const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/detail`);
      const r = await res.json();
      if (r.code === 0 && r.data) {
        setDetail({
          application: r.data.application,
          requirements: r.data.requirements,
          changes: r.data.changes,
          releases: r.data.releases,
        });
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
          setDetail({
            application: r.data.application,
            requirements: r.data.requirements,
            changes: r.data.changes,
            releases: r.data.releases,
          });
          setDetailErr("");
        } else {
          setDetailErr(r.message || "加载失败");
        }
      })
      .catch((e) => {
        if (!aborted) setDetailErr(String(e));
      });
    return () => {
      aborted = true;
    };
  }, [missingParams, psID, appID]);

  // 拉起 opencode 工作台（F-2：未选需求不 boot——避免进页面空 boot 一次、认领需求又被
  // forceNew kill+reboot 浪费 ~6s 且触发并发 Ensure；selectedReq 变化加 400ms 防抖，rapid
  // 切需求合并为一次 Ensure，配合后端端口注册表不再并发抢端口/泄漏。）
  useEffect(() => {
    if (missingParams || !selectedReq) return;
    let aborted = false;
    const timer = setTimeout(() => {
      const wantForceNew = forceNewRef.current;
      forceNewRef.current = false;
      // 新会话(force_new)：把需求规格拼成 prompt 随 boot 一起发——后端 Ensure 建会话后立即注入，
      // 会话成为活动会话且已含需求，再返回 deep_url；iframe 加载时"活动会话==deep_url会话==已含需求"，
      // 消除旧"先 setUrl 后异步注入"导致的会话错位/空窗（opencode SPA 加载瞬间读到旧活动会话）。
      // 复用会话(继续)不发 prompt、不重复注入；手动「🤖AI编码」仍走 dispatchReq→/inject-requirement。
      let bootPrompt: string | undefined;
      if (wantForceNew) {
        const req = reqsRef.current?.find((q) => q.id === selectedReq);
        if (req) bootPrompt = buildReqPrompt(req);
      }
      fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/workspace`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          tool,
          requirement_id: selectedReq,
          model: modelRef.current || undefined,
          force_new: wantForceNew || undefined,
          prompt: bootPrompt,
        }),
      })
        .then((r) => r.json())
        .then((r) => {
          if (aborted) return;
          if (r.code === 0 && r.data?.url) {
            setUrl(r.data.deep_url || r.data.url);
            setErr("");
            if (wantForceNew) {
              // 需求已由后端在 boot 时注入（会话即活动会话），右侧工作台直接可见 AI 实时编码。
              setTaskMsg(
                bootPrompt
                  ? "✅ 需求已发给 opencode → 在右侧工作台看 AI 实时编码,可随时介入/纠偏"
                  : "新会话已就绪（未取到需求规格，可点「🤖 AI 编码」手动注入）"
              );
            }
          } else {
            setErr(r.message || "启动编码工作台失败");
          }
          setLoading(false);
        })
        .catch((e) => {
          if (!aborted) {
            setErr(String(e));
            setLoading(false);
          }
        })
        .finally(() => {
          if (!aborted) setLoading(false);
        });
    }, 400);
    return () => {
      aborted = true;
      clearTimeout(timer);
    };
  }, [appID, psID, tool, reloadKey, newSessionKey, missingParams, selectedReq]);

  // 构建部署到 test,轮询 test 实例状态直到 running/failed(~2min 超时)
  async function deploy() {
    if (pollRef.current) clearInterval(pollRef.current);
    setDeployState("building");
    setDeployErr("");
    setTestUrl("");
    try {
      let r = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/deploy`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ env: "test", from_workspace: true }),
      }).then((rr) => rr.json());
      // 编码工作台：检测到 dev 分支有未提交改动 → 提示 → 确认后自动提交（AI 生成说明）再部署
      if (r.code === 0 && r.data?.status === "need_commit") {
        if (
          !confirm(
            r.data?.note || `检测到 ${r.data?.uncommitted ?? ""} 个文件未提交，是否提交并部署？`
          )
        ) {
          setDeployState("idle");
          return;
        }
        r = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/deploy`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ env: "test", from_workspace: true, auto_commit: true }),
        }).then((rr) => rr.json());
      }
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
      const ins = d?.instances?.find((i) => i.env === "test");
      if (ins?.status === "running") {
        setTestUrl(ins.url);
        setDeployState("running");
        if (pollRef.current) clearInterval(pollRef.current);
      } else if (ins?.status === "failed") {
        setDeployErr(d?.application?.last_error || "构建失败");
        setDeployState("failed");
        if (pollRef.current) clearInterval(pollRef.current);
      }
      if (n > 40 && pollRef.current) {
        // ~2min 超时兜底：后端异常时（如 docker build 卡死、状态迟迟不翻）不能让按钮一直"构建中…"，
        // 置 failed 提示用户去应用部署页看构建日志或重试。
        clearInterval(pollRef.current);
        pollRef.current = null;
        setDeployState("failed");
        setDeployErr("构建超时（2 分钟未完成），请在应用部署页查看构建日志或重试");
      }
    }, 3000);
  }

  // 登记变更:后端自动从 opencode 对话总结变更说明(免手填),刷新抽屉看 pending 变更。
  async function registerChange() {
    setRegistering(true);
    try {
      const res = await fetch(
        `${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/register-change`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ req_id: selectedReq }),
        }
      );
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
    if (!req) {
      setTaskMsg("需求不存在");
      return;
    }
    // 按子任务逐个:指定 taskIdx 或下一个未完成;没拆解则整个需求
    const next = taskIdx !== undefined ? subtasks[taskIdx] : subtasks.find((t) => !t.done);
    const prompt = buildReqPrompt(req, next);
    setDispatching(true);
    setTaskMsg(next ? `发送子任务给 opencode:${next.text}` : "把需求发给 opencode…");
    try {
      const r = await fetch(
        `${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/inject-requirement`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ prompt }),
        }
      ).then((rr) => rr.json());
      if (r.code !== 0) {
        setTaskMsg(r.message || "失败");
        setDispatching(false);
        return;
      }
      setTaskMsg(
        next
          ? `✅ 已发送子任务: ${next.text}\n做完后在左侧 checklist 打勾,再点「🤖AI编码」做下一个`
          : "✅ 需求已发给 opencode → 在右侧工作台看 AI 实时编码,可随时介入/纠偏"
      );
    } catch (e) {
      setTaskMsg(String(e));
    }
    setDispatching(false);
  }

  // 自动测试:当前需求 → AI 按验收标准生成用例 + 批量对着应用 URL 验收,显示通过/失败。
  async function runAutoTest() {
    if (!selectedReq) {
      alert("先在左侧选一个需求");
      return;
    }
    setTesting(true);
    setTestMsg("生成测试用例…");
    setTestResults(null);
    try {
      let r = await fetch(
        `${API_BASE_URL}/project-spaces/${psID}/requirements/${selectedReq}/generate-tests`,
        { method: "POST" }
      ).then((rr) => rr.json());
      if (r.code !== 0) {
        setTestMsg(r.message || "生成用例失败");
        setTesting(false);
        return;
      }
      setTestMsg("运行自动验收…(需先构建部署 test)");
      r = await fetch(
        `${API_BASE_URL}/project-spaces/${psID}/requirements/${selectedReq}/run-tests`,
        { method: "POST" }
      ).then((rr) => rr.json());
      if (r.code !== 0) {
        setTestMsg(r.message || "运行失败");
        setTesting(false);
        return;
      }
      const list: {
        method?: string;
        path?: string;
        expected_status?: number;
        actual_status?: number;
      }[] = r.data ?? [];
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
      const r = await fetch(
        `${API_BASE_URL}/project-spaces/${psID}/requirements/${selectedReq}/breakdown`,
        { method: "POST" }
      ).then((rr) => rr.json());
      if (r.code !== 0) {
        alert(r.message || "拆解失败");
        setBreaking(false);
        return;
      }
      try {
        setSubtasks(JSON.parse(r.data?.tasks || "[]"));
      } catch {
        setSubtasks([]);
      }
    } catch (e) {
      alert(String(e));
    }
    setBreaking(false);
  }

  // 合并 dev-<user> 到 main(上线前;worktree 模式必要)。
  // 期2-A:传 req_id 触发后端收敛(释放认领+需求 delivered+清 worktree)。
  async function mergeReq() {
    if (!selectedReq) {
      alert("先选需求");
      return;
    }
    setMerging(true);
    try {
      const r = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/merge`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ req_id: selectedReq }),
      }).then((rr) => rr.json());
      if (r.code !== 0) {
        alert(r.message || "合并失败");
        setMerging(false);
        return;
      }
      const d = r.data || {};
      alert(
        `✅ 已合并到主线 main,可点「🚀上线」\n${d.delivered ? "📦 需求已交付" : ""}${d.released ? " · 🔓 已释放认领" : ""}${d.worktree_cleaned ? " · 🧹 已清理工作区" : ""}\n\n💡 本需求已完成——为节省 token,建议认领下一个需求,将自动开启新的编码会话(旧会话历史不会带入)。`
      );
      fetchDetail();
    } catch (e) {
      alert(String(e));
    }
    setMerging(false);
  }

  // 提交核对门禁:AI 核对代码 vs 需求验收标准,不匹配拦(列差异),匹配放行。
  async function submitReq() {
    if (!selectedReq) {
      alert("先选需求");
      return;
    }
    setSubmitting(true);
    setSubmitMsg("AI 核对代码 vs 需求验收标准…");
    try {
      const r = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/submit`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ req_id: selectedReq }),
      }).then((rr) => rr.json());
      if (r.code !== 0) {
        setSubmitMsg("❌ 核对未通过,请按差异修正:\n" + (r.message || ""));
        setSubmitting(false);
        return;
      }
      setSubmitMsg("✅ 核对通过,已登记变更 " + (r.data?.change_id || "") + ",待审批");
    } catch (e) {
      setSubmitMsg(String(e));
    }
    setSubmitting(false);
  }

  const showErr = missingParams ? "缺少 app/ps 参数（请从应用卡片点「编码」进入）" : err;

  // 需求操作状态与回调：复用本组件已声明 state/函数，透传给 Sidebar → RequirementsView。
  const reqState: ReqState = {
    dispatching,
    testing,
    breaking,
    submitting,
    merging,
    taskMsg,
    testMsg,
    testResults,
    subtasks,
    submitMsg,
  };
  const reqActions: ReqActions = {
    dispatch: dispatchReq,
    runAutoTest: runAutoTest,
    breakdown: breakdownReq,
    submit: submitReq,
    merge: mergeReq,
    toggleSubtask: (i: number) =>
      setSubtasks((prev) => prev.map((x, j) => (j === i ? { ...x, done: !x.done } : x))),
  };

  return (
    <div className="-m-4 flex h-[calc(100vh-2.25rem)] flex-col md:-m-6">
      <WorkspaceToolbar
        appID={appID}
        appName={detail?.application?.name}
        tool={tool}
        model={model}
        onModelChange={setModel}
        deployState={deployState}
        testUrl={testUrl}
        deployErr={deployErr}
        onDeploy={deploy}
        onRegister={registerChange}
        registering={registering}
        onOpenWindow={() => {
          if (url) window.open(url, "_blank");
        }}
        onReconnect={() => {
          setUrl("");
          setLoading(true); // 修空白屏：重连 boot 期间显示"启动 opencode 工作台…"，不再白屏
          setReloadKey((k) => k + 1);
        }}
        drawerOpen={drawerOpen}
        onToggleDrawer={toggleDrawer}
      />
      <div className="flex min-h-0 flex-1">
        {drawerOpen && !missingParams && (
          <Sidebar
            psID={psID}
            appID={appID}
            detail={detail}
            loading={!detail && !detailErr}
            err={detailErr}
            selectedReq={selectedReq}
            onStartReq={async (id, fresh) => {
              // 认领 + 建/复用工作区（原 onStartReq 逻辑整体迁入）。
              // fresh=true（需求列表「🔄 新会话」）→ force_new 开空会话：丢弃当前上下文；
              // 需求内容不自动注入（由用户在左侧详情看完后手动点「🤖 AI 编码」）。
              try {
                const r = await fetch(
                  `${API_BASE_URL}/project-spaces/${psID}/requirements/${id}/assign`,
                  { method: "POST" }
                ).then((rr) => rr.json());
                if (r.code !== 0) {
                  alert(r.message || "认领失败");
                  return;
                }
              } catch (e) {
                alert(String(e));
                return;
              }
              if (fresh) {
                forceNewRef.current = true; // boot effect 读取后复位；force_new 跳过磁盘复用开空会话
                setNewSessionKey((k) => k + 1); // 即使 selectedReq 未变也强制 boot 重跑
              }
              setLoading(true); // 修空白屏：boot 期间显示"启动 opencode 工作台…"，不再白屏
              setSelectedReq(id);
              // 认领后工作台由上方 boot useEffect 重新拉起（其 deps 含 selectedReq / newSessionKey），
              // 不再内联 POST /workspace——否则与 effect 重跑并发双发，撞 Ensure 跨锁 race 致 opencode 进程/端口泄漏（I1）。
              setTaskMsg("");
              setTestMsg("");
              setTestResults(null);
              setSubmitMsg("");
              try {
                setSubtasks(
                  JSON.parse(detail?.requirements?.find((q) => q.id === id)?.tasks || "[]")
                );
              } catch {
                setSubtasks([]);
              }
              fetchDetail();
            }}
            onApprove={(id) => decideChange(id, "approve")}
            onReject={(id) => decideChange(id, "reject")}
            reqState={reqState}
            reqActions={reqActions}
          />
        )}
        <div className="flex min-h-0 flex-1 flex-col">
          {!missingParams && !selectedReq && !url && (
            <div className="p-4 text-sm text-neutral-500">
              请先在左侧认领一个需求，将自动启动该需求的编码工作台
            </div>
          )}
          {loading && !missingParams && selectedReq && !url && (
            <div className="p-4 text-sm text-neutral-500">
              启动 opencode 工作台…（首次约 3-5 秒）
            </div>
          )}
          {showErr && !url && <div className="p-4 text-sm text-red-600">{showErr}</div>}
          {url && <iframe src={url} className="min-h-0 flex-1" title="opencode 编码工作台" />}
        </div>
      </div>
    </div>
  );
}

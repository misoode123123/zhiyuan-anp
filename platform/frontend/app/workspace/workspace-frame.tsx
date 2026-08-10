"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useSearchParams } from "next/navigation";
import Link from "next/link";
import { API_BASE_URL } from "@/lib/api";
import { WorkspaceToolbar, type DeployState } from "./workspace-toolbar";
import type { WorkspaceDetail } from "./types";

// 编码工作台 tab 主体：
// - 顶部 WorkspaceToolbar：模型选择 + 构建部署(test)+ 部署状态轮询 + opencode 新窗口/重连
// - 主体：opencode 全屏 iframe
// 无需求化（#51 扶正为唯一路径）：进页面点「开始编码」即自主发起 session，后端注入应用上下文
// （AppContextPrompt）+ 开发规范写进 worktree AGENTS.md，opencode 自动加载。
//
// 注意：effect 内不同步 setState(react-hooks/set-state-in-effect)——
//   setState 都在 fetch/事件/轮询回调里。

export default function WorkspaceFrame() {
  const sp = useSearchParams();
  const appID = sp.get("app") || "";
  const psID = sp.get("ps") || "";
  const tool = sp.get("tool") || "opencode";
  // 绑需求开发（A 方案）：从需求页跳来带 req（requirement_id）+ rtitle（标题，仅展示）。
  // requirement_id 透传给后端 boot，新会话(force_new)时后端按此拼需求规格注入（单一真源）。
  const reqID = sp.get("req") || "";
  const reqTitle = sp.get("rtitle") || "";
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

  const [deployState, setDeployState] = useState<DeployState>("idle");
  const [testUrl, setTestUrl] = useState("");
  const [deployErr, setDeployErr] = useState("");
  // 自主发起编码（无需求绑定）：进页面点「开始编码」置 true，触发 boot。
  // 后端在 force_new + 无 requirement_id 时注入应用上下文（AppContextPrompt）；
  // 公司开发规范由 RefreshAgentsMD 写进 worktree AGENTS.md，opencode 自动加载。
  const [selfInitiated, setSelfInitiated] = useState(false);

  // 部署状态轮询句柄(卸载时清理)
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);
  useEffect(
    () => () => {
      if (pollRef.current) clearInterval(pollRef.current);
    },
    []
  );

  // 拉应用状态(部署轮询共用);返回完整 detail 供轮询判状态
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
        setDetail({ application: r.data.application });
        return r.data;
      }
      return null;
    } catch {
      return null;
    }
  }, [psID, appID]);

  // 首次加载应用名(setState 在 fetch 回调里,非 effect 同步,符合 set-state-in-effect)
  useEffect(() => {
    if (missingParams) return;
    let aborted = false;
    fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/detail`)
      .then((r) => r.json())
      .then((r) => {
        if (aborted) return;
        if (r.code === 0 && r.data) {
          setDetail({ application: r.data.application });
        }
      })
      .catch(() => {});
    return () => {
      aborted = true;
    };
  }, [missingParams, psID, appID]);

  // 拉起 opencode 工作台：进页面点「开始编码」(selfInitiated) 后 boot。
  // 自主发起：发 force_new 不发 requirement_id/prompt，后端据此注入应用上下文
  // （AppContextPrompt：应用名/仓库结构/依赖/开发规范指引）。selectedReq 不复存在。
  useEffect(() => {
    if (missingParams || !selfInitiated) return;
    let aborted = false;
    const timer = setTimeout(() => {
      const wantForceNew = forceNewRef.current;
      forceNewRef.current = false;
      fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/workspace`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          tool,
          model: modelRef.current || undefined,
          force_new: wantForceNew || undefined,
          // 绑需求：透传 requirement_id 供会话绑定 + codews requirement-switch 检测；
          // 注入与否由后端按 ForceNew 决定（新会话注入需求规格，继续=复用不注入）。
          requirement_id: reqID || undefined,
        }),
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
  }, [appID, psID, tool, reloadKey, newSessionKey, missingParams, selfInitiated, reqID]);

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

  // 进入编码：forceNew=false 复用上次会话（继续，不重发应用上下文）；
  // true 强制新建 + 后端注入应用上下文 AppContextPrompt（丢弃当前上下文从头开始）。
  const startCoding = (forceNew: boolean) => {
    forceNewRef.current = forceNew;
    setSelfInitiated(true);
    setNewSessionKey((k) => k + 1);
    setLoading(true);
    setErr("");
  };

  // missingParams 时由下方引导卡片接管（不再显示这句裸文案）。
  const showErr = missingParams ? "" : err;

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
        onOpenWindow={() => {
          if (url) window.open(url, "_blank");
        }}
        onReconnect={() => {
          setUrl("");
          setLoading(true); // 修空白屏：重连 boot 期间显示"启动 opencode 工作台…"，不再白屏
          setReloadKey((k) => k + 1);
        }}
        onNewSession={() => {
          // 🆕 新会话：丢弃当前上下文开空会话，后端重新注入应用上下文 + 刷新 AGENTS.md。
          forceNewRef.current = true; // boot effect 读取后复位；force_new 跳过磁盘复用开空会话
          setUrl("");
          setLoading(true);
          setErr("");
          setNewSessionKey((k) => k + 1); // 即使 selfInitiated 已 true 也强制 boot 重跑
        }}
      />
      {reqID && (
        <div className="border-b border-emerald-200 bg-emerald-50 px-4 py-1.5 text-xs text-emerald-700">
          🔗 已绑定需求「{reqTitle || reqID}」——新会话注入需求规格，继续编码复用上次会话
        </div>
      )}
      <div className="flex min-h-0 flex-1 flex-col">
        {missingParams && !url && (
          <div className="flex flex-col items-start gap-3 p-6 text-sm">
            <div className="text-base font-medium text-text">请选择应用或需求后进入编码</div>
            <div className="text-xs text-text-muted">
              编码工作台需要应用上下文。从「需求工作台」认领需求并编码，或从「应用部署」选应用后进入。
            </div>
            <div className="flex gap-2">
              <Link
                href="/requirements"
                className="rounded bg-accent px-3 py-1.5 text-xs text-white"
              >
                💬 需求工作台
              </Link>
              <Link
                href="/applications"
                className="rounded border border-border bg-surface px-3 py-1.5 text-xs text-text"
              >
                📦 应用部署
              </Link>
            </div>
          </div>
        )}
        {!missingParams && !selfInitiated && !url && (
          <div className="flex flex-col gap-2 p-4 text-sm text-neutral-500">
            <div className="flex gap-2">
              <button
                type="button"
                onClick={() => startCoding(false)}
                className="rounded bg-emerald-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-emerald-700"
              >
                ▶ 继续编码
              </button>
              <button
                type="button"
                onClick={() => startCoding(true)}
                className="rounded border border-border bg-surface px-3 py-1.5 text-xs font-medium text-text hover:bg-surface-2"
              >
                🆕 新会话
              </button>
            </div>
            <div className="text-xs text-text-muted">
              {reqID
                ? "继续编码 = 接着上次进度（不重发）；新会话 = 从头按需求规格编码。"
                : "继续编码 = 接着上次的进度（不重发上下文）；新会话 = 丢弃上下文从头开始。"}
            </div>
          </div>
        )}
        {loading && !missingParams && selfInitiated && !url && (
          <div className="p-4 text-sm text-neutral-500">启动 opencode 工作台…（首次约 3-5 秒）</div>
        )}
        {showErr && !url && <div className="p-4 text-sm text-red-600">{showErr}</div>}
        {url && <iframe src={url} className="min-h-0 flex-1" title="opencode 编码工作台" />}
      </div>
    </div>
  );
}

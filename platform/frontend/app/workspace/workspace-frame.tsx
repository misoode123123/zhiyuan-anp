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

  const showErr = missingParams ? "缺少 app/ps 参数（请从应用卡片点「编码」进入）" : err;

  return (
    <div className="-m-4 flex h-[calc(100vh-2.25rem)] flex-col md:-m-6">
      <WorkspaceToolbar
        appID={appID}
        tool={tool}
        deployState={deployState}
        testUrl={testUrl}
        deployErr={deployErr}
        onDeploy={deploy}
        onOpenWindow={() => { if (url) window.open(url, "_blank"); }}
        onReconnect={() => { setUrl(""); setReloadKey((k) => k + 1); }}
        drawerOpen={drawerOpen}
        onToggleDrawer={toggleDrawer}
      />
      <div className="flex min-h-0 flex-1">
        {drawerOpen && !missingParams && (
          <ContextDrawer detail={detail} loading={!detail && !detailErr} err={detailErr} onClose={toggleDrawer} />
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

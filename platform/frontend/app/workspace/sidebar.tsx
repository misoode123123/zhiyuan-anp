"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL } from "@/lib/api";
import { ActivityBar, type ViewKey } from "./activity-bar";
import { DiffDrawer } from "./diff-drawer";
import { RequirementsView } from "./views/requirements-view";
import { SourceControlView } from "./views/source-control-view";
import { ReleasesView } from "./views/releases-view";
import { FilesView } from "./views/files-view";
import type { GitStatus, WorkspaceDetail, ReqState, ReqActions } from "./types";

// 编码工作台左面板：活动栏 + 当前视图 + diff 浮层抽屉。
// git 数据与需求操作状态由 WorkspaceFrame 注入。
export function Sidebar({
  psID,
  appID,
  detail,
  loading,
  err,
  selectedReq,
  onStartReq,
  onApprove,
  onReject,
  reqState,
  reqActions,
}: {
  psID: string;
  appID: string;
  detail: WorkspaceDetail | null;
  loading: boolean;
  err: string;
  selectedReq: string;
  onStartReq: (id: string) => void;
  onApprove: (id: string) => void;
  onReject: (id: string) => void;
  reqState: ReqState;
  reqActions: ReqActions;
}) {
  const [view, setView] = useState<ViewKey>(() => {
    if (typeof window === "undefined") return "requirements";
    return (window.localStorage.getItem("anp.workspace.view") as ViewKey) || "requirements";
  });
  const [gitStatus, setGitStatus] = useState<GitStatus | null>(null);
  const [gitLoading, setGitLoading] = useState(true);
  const [gitErr, setGitErr] = useState("");
  const [diffPath, setDiffPath] = useState("");
  const [diffSha, setDiffSha] = useState<string | undefined>(undefined);
  const [diffText, setDiffText] = useState("");
  const [diffLoading, setDiffLoading] = useState(false);
  const [diffTrunc, setDiffTrunc] = useState(false);

  function selectView(k: ViewKey) {
    setView(k);
    try {
      window.localStorage.setItem("anp.workspace.view", k);
    } catch {}
  }

  const fetchGit = () => {
    fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/git-status`)
      .then((r) => r.json())
      .then((r) => {
        if (r.code === 0 && r.data) {
          setGitStatus(r.data);
          setGitErr("");
        } else {
          setGitErr(r.message || "加载失败");
        }
      })
      .catch((e) => setGitErr(String(e)))
      .finally(() => setGitLoading(false));
  };

  // 首次 + 10s 轮询 git-status（编码时看实时工作区改动）。
  useEffect(() => {
    if (!psID || !appID) return;
    fetchGit();
    const t = setInterval(fetchGit, 10000);
    return () => clearInterval(t);
  }, [psID, appID]);

  async function openFile(path: string, sha?: string) {
    setDiffPath(path);
    setDiffSha(sha);
    setDiffText("");
    setDiffTrunc(false);
    setDiffLoading(true);
    try {
      const q = `path=${encodeURIComponent(path)}${sha ? `&sha=${encodeURIComponent(sha)}` : ""}`;
      const r = await fetch(
        `${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/file-diff?${q}`
      ).then((rr) => rr.json());
      if (r.code === 0 && r.data) {
        setDiffText(r.data.diff || "");
        setDiffTrunc(!!r.data.truncated);
      } else {
        setDiffText(r.message || "加载失败");
      }
    } catch (e) {
      setDiffText(String(e));
    }
    setDiffLoading(false);
  }

  const badges: Record<ViewKey, number> = {
    requirements: (detail?.requirements ?? []).filter((q) => !q.assignee).length,
    "source-control": gitStatus?.changes?.length ?? 0,
    releases: detail?.releases?.length ?? 0,
    files: 0,
  };

  return (
    <aside className="relative flex h-full w-[280px] shrink-0 border-r border-border bg-surface-2">
      <ActivityBar active={view} onSelect={selectView} badges={badges} />
      <div className="flex min-w-0 flex-1 flex-col">
        {err ? (
          <div className="p-3 text-[11px] text-danger">{err}</div>
        ) : view === "requirements" ? (
          <RequirementsView
            detail={detail}
            selectedReq={selectedReq}
            onStartReq={onStartReq}
            reqState={reqState}
            reqActions={reqActions}
          />
        ) : view === "source-control" ? (
          <SourceControlView
            psID={psID}
            appID={appID}
            gitStatus={gitStatus}
            gitLoading={gitLoading}
            gitErr={gitErr}
            detail={detail}
            onApprove={onApprove}
            onReject={onReject}
            onOpenFile={openFile}
            onCommitted={fetchGit}
          />
        ) : view === "releases" ? (
          <ReleasesView detail={detail} />
        ) : (
          <FilesView psID={psID} appID={appID} />
        )}
      </div>
      {diffPath && (
        <DiffDrawer
          path={diffPath}
          diff={diffText}
          loading={diffLoading}
          truncated={diffTrunc}
          onClose={() => setDiffPath("")}
        />
      )}
    </aside>
  );
}

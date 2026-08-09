"use client";

import { useState } from "react";
import { ActivityBar, type ViewKey } from "./activity-bar";
import { RequirementsView } from "./views/requirements-view";
import { ReleasesView } from "./views/releases-view";
import type { WorkspaceDetail, ReqState, ReqActions } from "./types";

// 编码工作台左面板：活动栏 + 当前视图。
// 仅「需求 + 发布」两个视图：文件浏览/源代码管理 opencode 自带，ANP 侧不再重复暴露
// （原 git-status 轮询 / diff 抽屉 / 变更审批 UI 随 source-control 视图一并移除）。
// 需求操作状态由 WorkspaceFrame 注入。
export function Sidebar({
  detail,
  err,
  selectedReq,
  onStartReq,
  reqState,
  reqActions,
}: {
  detail: WorkspaceDetail | null;
  err: string;
  selectedReq: string;
  onStartReq: (id: string, fresh?: boolean) => void;
  reqState: ReqState;
  reqActions: ReqActions;
}) {
  const [view, setView] = useState<ViewKey>(() => {
    if (typeof window === "undefined") return "requirements";
    const saved = window.localStorage.getItem("anp.workspace.view");
    // 兼容旧值：历史可能存 "source-control"/"files"（视图已移除），回退到 requirements。
    return saved === "releases" ? "releases" : "requirements";
  });

  function selectView(k: ViewKey) {
    setView(k);
    try {
      window.localStorage.setItem("anp.workspace.view", k);
    } catch {}
  }

  const badges: Record<ViewKey, number> = {
    requirements: (detail?.requirements ?? []).filter((q) => !q.assignee).length,
    releases: detail?.releases?.length ?? 0,
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
        ) : (
          <ReleasesView detail={detail} />
        )}
      </div>
    </aside>
  );
}

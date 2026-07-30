"use client";

import { useState } from "react";
import { API_BASE_URL } from "@/lib/api";
import type { GitStatus, WorkspaceDetail } from "../types";
import { fileStatusColor, formatRelativeTime } from "@/lib/workspace";

// 源代码管理视图：工作区改动 + 提交历史 + 待审批变更 + 提交框。
// diff 展示委派给上层 DiffDrawer（onOpenFile）。
export function SourceControlView({
  psID,
  appID,
  gitStatus,
  gitLoading,
  gitErr,
  detail,
  onApprove,
  onReject,
  onOpenFile,
  onCommitted,
}: {
  psID: string;
  appID: string;
  gitStatus: GitStatus | null;
  gitLoading: boolean;
  gitErr: string;
  detail: WorkspaceDetail | null;
  onApprove: (id: string) => void;
  onReject: (id: string) => void;
  onOpenFile: (path: string, sha?: string) => void;
  onCommitted: () => void;
}) {
  const [openSha, setOpenSha] = useState<string | null>(null);
  const [commitFiles, setCommitFiles] = useState<
    Record<string, { path: string; status: string }[]>
  >({});
  const [committing, setCommitting] = useState(false);
  const [msg, setMsg] = useState("");

  const changes = gitStatus?.changes ?? [];
  const commits = gitStatus?.commits ?? [];
  const pending = (detail?.changes ?? []).filter((c) => c.status === "pending");

  async function toggleSha(sha: string) {
    if (openSha === sha) {
      setOpenSha(null);
      return;
    }
    setOpenSha(sha);
    if (!commitFiles[sha]) {
      try {
        const r = await fetch(
          `${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/commit-files?sha=${encodeURIComponent(sha)}`
        ).then((rr) => rr.json());
        if (r.code === 0) {
          setCommitFiles((m) => ({ ...m, [sha]: r.data?.files ?? [] }));
        }
      } catch {}
    }
  }

  async function commit() {
    setCommitting(true);
    try {
      const r = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/commit`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ message: msg }),
      }).then((rr) => rr.json());
      if (r.code !== 0) {
        alert(r.message || "提交失败");
      } else {
        setMsg("");
        onCommitted();
      }
    } catch (e) {
      alert(String(e));
    }
    setCommitting(false);
  }

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center justify-between border-b border-border px-3 py-2 text-[11px] uppercase tracking-wide text-text-muted">
        <span>源代码管理</span>
        <span className="font-mono text-[10px] text-text-muted">
          {gitStatus?.branch || "dev-?"}
        </span>
      </div>
      <div className="flex-1 overflow-auto">
        {gitErr && <div className="px-3 py-1 text-[11px] text-danger">{gitErr}</div>}
        {!gitStatus?.worktree_exists ? (
          <div className="px-3 py-2 text-[11px] text-text-muted">
            工作区未创建（请先认领需求生成 dev 分支）
          </div>
        ) : (
          <>
            {/* 工作区改动 */}
            <Section title="工作区改动" count={changes.length} />
            {changes.map((f, i) => (
              <div
                key={i}
                className="flex items-center gap-2 px-3 py-1 pl-7 leading-relaxed hover:bg-surface-2"
              >
                <span
                  className="w-[14px] shrink-0 text-center text-[11px] font-bold"
                  style={{ color: fileStatusColor(f.status) }}
                >
                  {f.status}
                </span>
                <button
                  onClick={() => onOpenFile(f.path)}
                  className="min-w-0 flex-1 truncate text-left font-mono text-[12px] text-text"
                >
                  {f.path}
                </button>
              </div>
            ))}

            {/* 提交历史 */}
            <Section title="提交历史" count={commits.length} />
            {commits.map((c) => (
              <div key={c.sha}>
                <div className="flex items-center gap-2 px-3 py-1 pl-7 leading-relaxed hover:bg-surface-2">
                  <button
                    onClick={() => toggleSha(c.sha)}
                    className="flex min-w-0 flex-1 items-center gap-2 text-left"
                  >
                    <span className="text-[9px] text-text-muted">
                      {openSha === c.sha ? "▾" : "▸"}
                    </span>
                    <span className="font-mono text-[11px] text-text-muted">{c.sha}</span>
                    <span className="truncate text-[12px] text-text">{c.message}</span>
                  </button>
                  <span className="ml-auto shrink-0 text-[10px] text-text-muted">
                    {formatRelativeTime(c.date)}
                  </span>
                </div>
                {openSha === c.sha && commitFiles[c.sha] && (
                  <div className="pb-1">
                    {(commitFiles[c.sha] ?? []).map((f, j) => (
                      <button
                        key={j}
                        onClick={() => onOpenFile(f.path, c.sha)}
                        className="flex w-full items-center gap-2 px-3 py-0.5 pl-[52px] text-left hover:bg-surface-2"
                      >
                        <span
                          className="w-[14px] text-center text-[11px] font-bold"
                          style={{ color: fileStatusColor(f.status) }}
                        >
                          {f.status}
                        </span>
                        <span className="truncate font-mono text-[12px] text-text-muted">
                          {f.path}
                        </span>
                      </button>
                    ))}
                  </div>
                )}
              </div>
            ))}

            {/* 待审批变更 */}
            {pending.length > 0 && <Section title="待审批变更" count={pending.length} />}
            {pending.map((c) => (
              <div key={c.id} className="px-3 py-1 pl-7">
                <div className="flex items-center gap-2">
                  <span className="w-[3px] rounded bg-warn" style={{ height: 15 }} />
                  <span className="flex-1 truncate text-[12px] text-text">提交核对 · {c.kind}</span>
                  <button onClick={() => onApprove(c.id)} className="text-[11px] text-success">
                    ✓ 通过
                  </button>
                  <button onClick={() => onReject(c.id)} className="text-[11px] text-danger">
                    ✕ 拒绝
                  </button>
                </div>
              </div>
            ))}

            {/* 提交框 */}
            <div className="m-3 rounded border border-border bg-surface p-1.5">
              <input
                value={msg}
                onChange={(e) => setMsg(e.target.value)}
                placeholder="提交说明（留空让 AI 总结）"
                className="w-full rounded border border-border px-2 py-1 text-[12px] outline-none focus:border-accent"
              />
              <div className="mt-1.5 flex items-center gap-2">
                <button
                  onClick={commit}
                  disabled={committing || changes.length === 0}
                  className="rounded bg-success px-2 py-0.5 text-[11px] text-accent-fg disabled:opacity-50"
                >
                  {committing ? "提交中…" : "提交"}
                </button>
                <span className="text-[10px] text-text-muted">仅提交，部署走顶部工具栏</span>
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function Section({ title, count }: { title: string; count: number }) {
  return (
    <div className="flex items-center gap-1.5 px-3 pb-1 pt-2 text-[11px] uppercase tracking-wide text-text-muted">
      <span className="text-[9px] text-text-muted">▾</span>
      <span>{title}</span>
      <span className="rounded-full bg-surface-2 px-1.5 text-[10px] text-text-muted">{count}</span>
    </div>
  );
}

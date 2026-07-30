"use client";

import type { WorkspaceDetail } from "../types";
import { statusColor, statusLabel } from "@/lib/workspace";

// 发布视图：已上线版本记录（IDE 风格）。
export function ReleasesView({ detail }: { detail: WorkspaceDetail | null }) {
  const rels = detail?.releases ?? [];
  return (
    <div className="flex h-full flex-col">
      <div className="border-b border-border px-3 py-2 text-[11px] uppercase tracking-wide text-text-muted">
        发布
      </div>
      <div className="flex-1 overflow-auto py-0.5">
        {rels.length === 0 ? (
          <div className="p-3 text-text-muted">暂无发布</div>
        ) : (
          rels.map((r) => (
            <div
              key={r.id}
              className="flex items-center gap-2 px-3 py-1.5 leading-relaxed hover:bg-surface-2"
            >
              <span
                className="w-[3px] shrink-0 rounded"
                style={{ height: 15, background: statusColor(r.status) }}
              />
              <span className="font-mono text-[12px] text-text-muted">v{r.version}</span>
              <span className="text-[11px] text-text-muted">{statusLabel(r.status)}</span>
            </div>
          ))
        )}
      </div>
    </div>
  );
}

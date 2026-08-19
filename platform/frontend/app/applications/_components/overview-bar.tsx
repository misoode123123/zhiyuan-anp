"use client";

// 应用总览条（spec §2）：每应用一格，横向滚动；点击=开/切 tab；激活格高亮边框。
// 纯受控展示，数据推导在 _lib/overview.ts（cells 由壳传入）。
import { STATUS_COLOR } from "../_lib/predicates";
import type { OverviewCell } from "../_lib/overview";

const ENV_DOT: Record<string, string> = {
  ok: "bg-success", // T✓/P✓ 绿点
  bad: "bg-danger", // 实例存在但非 running
  none: "bg-surface-2", // 无实例
};

function EnvDot({ label, state }: { label: string; state: "ok" | "bad" | "none" }) {
  return (
    <span
      className="flex items-center gap-0.5 text-[10px] text-text-muted"
      title={`${label}: ${state}`}
    >
      <span className={`inline-block h-2 w-2 rounded-full ${ENV_DOT[state]}`} />
      {label}
    </span>
  );
}

export function OverviewBar({
  cells,
  activeId,
  onSelect,
}: {
  cells: OverviewCell[];
  activeId: string;
  onSelect: (appId: string) => void;
}) {
  return (
    <div className="mb-3 flex gap-2 overflow-x-auto pb-1" data-testid="overview-bar">
      {cells.map((c) => (
        <button
          key={c.id}
          onClick={() => onSelect(c.id)}
          className={`shrink-0 rounded-lg border px-3 py-1.5 text-left text-xs transition-colors ${
            c.id === activeId
              ? "border-accent bg-accent/10"
              : "border-border bg-surface hover:border-accent/50"
          }`}
          title={c.isImporting ? `导入中：${c.importingHint}` : c.status}
        >
          <div className="flex items-center gap-2">
            <span className={`rounded px-1 py-0.5 ${STATUS_COLOR[c.status] ?? "bg-surface-2"}`}>
              {c.isImporting ? "导入中" : c.status}
            </span>
            <span className="font-medium">{c.name}</span>
            {c.showVersion && <span className="text-text-muted">v{c.version}</span>}
          </div>
          {!c.isExternal && (
            <div className="mt-1 flex items-center gap-3">
              <EnvDot label="T" state={c.test} />
              <EnvDot label="P" state={c.prod} />
            </div>
          )}
        </button>
      ))}
      {cells.length === 0 && (
        <span className="text-xs text-text-muted">暂无应用（在上方注册或导入）</span>
      )}
    </div>
  );
}

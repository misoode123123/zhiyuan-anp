"use client";

import { parseDiff, type DiffLine } from "@/lib/workspace";

// diff 浮层抽屉：从侧边栏右缘滑出，覆盖 opencode 左部（默认 480px，CSS 可调）。
// 行级 diff：add 绿底 / del 红底 / ctx 灰 / hunk 浅蓝。
export function DiffDrawer({
  path,
  diff,
  loading,
  truncated,
  onClose,
}: {
  path: string;
  diff: string;
  loading: boolean;
  truncated: boolean;
  onClose: () => void;
}) {
  const lines: DiffLine[] = parseDiff(diff);
  const bg: Record<DiffLine["type"], string> = {
    add: "bg-[#dafbe1] text-[#1a7f37]",
    del: "bg-[#ffebe9] text-[#cf222e]",
    ctx: "text-[#57606a]",
    hunk: "bg-[#ddf4ff] text-[#0969da]",
    meta: "text-[#8b949e]",
  };
  return (
    <div className="absolute inset-y-0 left-[280px] z-20 flex w-[480px] max-w-[60vw] flex-col border-r border-[#d0d0d0] bg-white shadow-lg">
      <div className="flex items-center justify-between border-b border-[#e6e6e6] px-3 py-2 text-xs">
        <span className="truncate font-mono text-[#57606a]">{path}</span>
        <button onClick={onClose} className="text-[#9a9a9a] hover:text-[#57606a]" title="关闭">
          ✕
        </button>
      </div>
      <div className="flex-1 overflow-auto font-mono text-[11.5px] leading-[1.6]">
        {loading ? (
          <div className="p-3 text-[#9a9a9a]">加载 diff…</div>
        ) : lines.length === 0 ? (
          <div className="p-3 text-[#9a9a9a]">无差异</div>
        ) : (
          lines.map((ln, i) => (
            <div key={i} className={`whitespace-pre px-3 ${bg[ln.type]}`}>
              {ln.text || " "}
            </div>
          ))
        )}
      </div>
      {truncated && (
        <div className="border-t border-[#e6e6e6] px-3 py-1 text-[11px] text-[#8b949e]">
          diff 超长，已截断前 2000 行
        </div>
      )}
    </div>
  );
}

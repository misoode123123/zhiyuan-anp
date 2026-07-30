"use client";

// 编码工作台活动栏：4 视图图标，选中项左侧蓝色竖条，右上角红点 badge。
// 浅色风格：白底 + 右细分隔线与 #f3f3f3 面板区分；选中态深色图标 + 左蓝条，未选中灰图标 + hover 浅灰底。
export type ViewKey = "requirements" | "source-control" | "releases" | "files";

const VIEWS: { key: ViewKey; icon: string; label: string }[] = [
  { key: "requirements", icon: "📋", label: "需求" },
  { key: "source-control", icon: "🔀", label: "源代码管理" },
  { key: "releases", icon: "🚀", label: "发布" },
  { key: "files", icon: "📁", label: "文件" },
];

export function ActivityBar({
  active,
  onSelect,
  badges,
}: {
  active: ViewKey;
  onSelect: (k: ViewKey) => void;
  badges: Record<ViewKey, number>;
}) {
  return (
    <div className="flex w-[46px] shrink-0 flex-col items-center gap-1.5 border-r border-border bg-surface py-2">
      {VIEWS.map((v) => {
        const n = badges[v.key] || 0;
        return (
          <button
            key={v.key}
            onClick={() => onSelect(v.key)}
            title={v.label}
            className={`relative flex h-[34px] w-[34px] items-center justify-center rounded-md text-[17px] transition-colors ${
              active === v.key
                ? "border-l-2 border-l-accent text-text"
                : "border-l-2 border-l-transparent text-text-muted hover:bg-surface-2 hover:text-text"
            }`}
          >
            <span>{v.icon}</span>
            {n > 0 && (
              <span className="absolute right-0.5 top-0 rounded-full bg-danger px-1 text-[9px] font-bold leading-[13px] text-accent-fg">
                {n > 99 ? "99+" : n}
              </span>
            )}
          </button>
        );
      })}
    </div>
  );
}

// 主线流程引导：需求 → 编码 → 审批 → 发布
const STEPS = ["需求", "编码", "审批", "发布"];

export function FlowStepper({ current }: { current: number }) {
  return (
    <div className="mb-4 flex items-center gap-1 text-xs">
      {STEPS.map((s, i) => (
        <div key={s} className="flex items-center gap-1">
          <span
            className={`rounded-full px-2 py-1 ${
              i === current
                ? "bg-accent text-white"
                : i < current
                  ? "bg-emerald-500 text-white"
                  : "bg-surface-2 text-text-muted"
            }`}
          >
            {i + 1}. {s}
          </span>
          {i < STEPS.length - 1 && <span className="text-text-muted">→</span>}
        </div>
      ))}
      <span className="ml-2 text-text-muted">您在此</span>
    </div>
  );
}

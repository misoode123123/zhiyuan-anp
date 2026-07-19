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
                ? "bg-blue-600 text-white"
                : i < current
                  ? "bg-emerald-500 text-white"
                  : "bg-neutral-200 text-neutral-500"
            }`}
          >
            {i + 1}. {s}
          </span>
          {i < STEPS.length - 1 && <span className="text-neutral-400">→</span>}
        </div>
      ))}
      <span className="ml-2 text-neutral-400">您在此</span>
    </div>
  );
}

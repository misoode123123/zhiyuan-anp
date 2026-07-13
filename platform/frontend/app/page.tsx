export default function Home() {
  return (
    <div>
      <h1 className="mb-2 text-2xl font-bold">智源 ANP 平台概览</h1>
      <p className="mb-6 text-neutral-600">企业 AI 原生研发平台 · M0 骨架</p>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        {[
          { k: "需求 → 上线", v: "AI 驱动全流程" },
          { k: "项目空间", v: "多租户隔离" },
          { k: "规则治理", v: "RaC 硬约束" },
        ].map((c) => (
          <div key={c.k} className="rounded-lg border border-neutral-200 bg-white p-4">
            <div className="text-sm text-neutral-500">{c.k}</div>
            <div className="mt-1 text-lg font-semibold">{c.v}</div>
          </div>
        ))}
      </div>
      <div className="mt-6 rounded-lg border border-blue-200 bg-blue-50 p-4 text-sm text-blue-900">
        左侧选择「需求工作台」体验对话（需先启动 Go 后端 :8080 与 AI 运行时 :8001）。
      </div>
    </div>
  );
}

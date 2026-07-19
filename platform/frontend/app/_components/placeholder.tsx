export function Placeholder({ icon, title, desc }: { icon: string; title: string; desc: string }) {
  return (
    <div>
      <h1 className="mb-2 text-xl font-bold">
        {icon} {title}
      </h1>
      <p className="mb-4 text-neutral-600">{desc}</p>
      <div className="rounded-lg border border-amber-200 bg-amber-50 p-4 text-sm text-amber-900">
        🚧 建设中（M1+）。当前已可用：
        <b className="mx-1">概览</b>·<b className="mx-1">需求工作台</b>（智谱 GLM 对话）·
        <b className="mx-1">研发工作台</b>（opencode + GLM 编码）
      </div>
    </div>
  );
}

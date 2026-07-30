// ChangeOutput 结构化展示变更登记时生成的 output(【总结】【说明】【diff】【commits】【对话】【需求】分区)。
// 把一坨 pre 拆成分区:审批人一眼看【总结】,需要时展开【diff】看代码改动。
type Sections = Record<string, string>;

function parseChangeOutput(output: string): Sections {
  const s: Sections = {};
  const re = /【([^】]+)】([\s\S]*?)(?=【[^】]+】|$)/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(output))) {
    s[m[1].trim()] = m[2].trim();
  }
  return s;
}

export function ChangeOutput({ output }: { output: string }) {
  const s = parseChangeOutput(output);
  return (
    <div className="mt-2 space-y-1 text-xs">
      {s["总结"] && <div className="rounded bg-blue-50 p-1.5 text-text">{s["总结"]}</div>}
      {s["需求"] && <div className="text-text-muted">关联需求: {s["需求"]}</div>}
      {s["说明"] && <div className="text-text-muted">📝 {s["说明"]}</div>}
      {s["commits"] && (
        <details>
          <summary className="cursor-pointer text-text-muted">commits</summary>
          <pre className="mt-1 whitespace-pre-wrap rounded bg-surface-2 p-1.5 text-text-muted">
            {s["commits"]}
          </pre>
        </details>
      )}
      {s["diff"] && (
        <details>
          <summary className="cursor-pointer font-medium text-accent">
            📄 代码 diff(点击展开)
          </summary>
          <pre className="mt-1 max-h-80 overflow-auto whitespace-pre-wrap rounded bg-neutral-900 p-2 text-green-300">
            {s["diff"]}
          </pre>
        </details>
      )}
      {s["对话"] && (
        <details>
          <summary className="cursor-pointer text-text-muted">💬 opencode 对话</summary>
          <pre className="mt-1 max-h-60 overflow-auto whitespace-pre-wrap rounded bg-bg p-1.5 text-text-muted">
            {s["对话"]}
          </pre>
        </details>
      )}
    </div>
  );
}

// 编码工作台左面板纯函数：diff 解析、状态配色、相对时间。
// 组件层只做渲染，逻辑集中此处供 vitest 纯函数测试（vitest.config.ts environment=node）。

export type DiffLine = {
  type: "add" | "del" | "ctx" | "hunk" | "meta";
  text: string;
};

// parseDiff 把 git unified diff 文本按行归类。
//   diff --git / index / mode / rename / copy / +++/--- → meta；@@ → hunk；+ → add；- → del；其余 → ctx。
//   注：测试用例以 `diff --git` 行作为 meta，故除 +++/--- 外还需识别 git diff 头部行；
//   hunk 内的正文行必以 + / - / 空格 起头，故按前缀匹配头部关键字不会误伤正文。
export function parseDiff(diff: string): DiffLine[] {
  if (!diff) return [];
  return diff.split("\n").map((text) => {
    if (
      text.startsWith("+++") ||
      text.startsWith("---") ||
      text.startsWith("diff ") ||
      text.startsWith("index ") ||
      text.startsWith("new file") ||
      text.startsWith("deleted file") ||
      text.startsWith("old mode") ||
      text.startsWith("new mode") ||
      text.startsWith("similarity") ||
      text.startsWith("dissimilarity") ||
      text.startsWith("rename ") ||
      text.startsWith("copy ")
    ) {
      return { type: "meta", text };
    }
    if (text.startsWith("@@")) return { type: "hunk", text };
    if (text.startsWith("+")) return { type: "add", text };
    if (text.startsWith("-")) return { type: "del", text };
    return { type: "ctx", text };
  });
}

// statusColor 需求/变更/发布状态 → 左侧色条颜色。
export function statusColor(status: string): string {
  switch (status) {
    case "delivered":
    case "approved":
      return "#3fb950";
    case "pending":
      return "#d29922";
    case "developing":
      return "#007acc";
    case "draft":
    default:
      return "#8b949e";
  }
}

// fileStatusColor git 文件状态字母 → 颜色（M/A/D/U/R/C）。
export function fileStatusColor(st: string): string {
  switch (st) {
    case "M":
      return "#d29922";
    case "A":
      return "#2da44e";
    case "D":
      return "#cf222e";
    case "U":
      return "#8250df";
    case "R":
    case "C":
      return "#0969da";
    default:
      return "#8b949e";
  }
}

// statusLabel 需求状态 → 中文短标签。
export function statusLabel(status: string): string {
  const m: Record<string, string> = {
    delivered: "已交付",
    approved: "已批准",
    pending: "待审批",
    draft: "草稿",
    developing: "开发中",
    specified: "已规格",
    reviewed: "已评审",
    rejected: "已拒绝",
  };
  return m[status] || status;
}

// formatRelativeTime ISO 时间 → 相对时间（X 分钟前/X 小时前/昨天/前天/N 天前）。
export function formatRelativeTime(iso: string): string {
  if (!iso) return "";
  const t = new Date(iso).getTime();
  if (isNaN(t)) return iso;
  const diff = Date.now() - t;
  const min = Math.floor(diff / 60000);
  if (min < 1) return "刚刚";
  if (min < 60) return `${min} 分钟前`;
  const h = Math.floor(min / 60);
  if (h < 24) return `${h} 小时前`;
  const d = Math.floor(h / 24);
  if (d === 1) return "昨天";
  if (d === 2) return "前天";
  if (d < 30) return `${d} 天前`;
  return iso.slice(0, 10);
}

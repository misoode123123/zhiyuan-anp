// 应用部署页纯谓词/工具（从 page.tsx 迁出，原样保留）+ 新增 env 过滤。
import type { Detail } from "./types";

export const STATUS_COLOR: Record<string, string> = {
  running: "bg-success/10 text-success",
  building: "bg-warn/10 text-warn",
  preparing: "bg-warn/10 text-warn", // AI 部署在途（简报→执行→验证），视同 building 同族色
  registered: "bg-surface-2 text-text-muted",
  stopped: "bg-accent/10 text-accent",
  failed: "bg-danger/10 text-danger",
  importing: "bg-warn/10 text-warn", // 导入进行中态（复用 status 列）
};

// 节点过滤辅助（Task 11）：按应用类型 + 部署环境过滤可选节点。
// - node_local 始终可选（.28 本地，env=test/os_type=linux，双环境通用）。
// - Windows 节点分流：ssh/winrm 连接的走原生部署（支持 Windows，需仓库 deploy.yaml，
//   后端校验）；仅非 ssh/winrm 的 Windows 节点（无 docker 守护进程）对 docker 形态不可选。
// - env 过滤：test 部署只匹配 env=test 节点，prod 只匹配 env=prod；node_local 豁免。
export const isDockerKind = (kind: string) =>
  kind === "web" || kind === "service" || kind === "headless" || !kind;

// 判断节点是否可部署到目标环境（node_local 豁免；否则 env 必须匹配）。
export function nodeMatchesEnv(
  n: { id: string; env?: string } | undefined,
  targetEnv: string
): boolean {
  if (!n) return true; // 未选节点（空）由后端用默认，不拦
  if (n.id === "node_local") return true;
  return n.env === targetEnv;
}

// healthBadge headless 实例的健康徽标（进程存活：running/degraded/failed）。
// 配色用本仓既有自定义类：text-success / text-danger（已 grep 确认 app/ 下存在）。
export function healthBadge(status: string): { text: string; cls: string } {
  switch (status) {
    case "running":
      return { text: "运行中", cls: "text-success" };
    case "degraded":
      return { text: "不稳定(crash-loop)", cls: "text-danger" };
    case "failed":
      return { text: "已停止", cls: "text-danger" };
    default:
      return { text: status, cls: "" };
  }
}

// 按 env 过滤部署历史（P3 时间线进环境卡用）：保持原顺序（后端已按时间倒序）。
export function historyForEnv(
  history: Detail["deploy_history"],
  env: string
): Detail["deploy_history"] {
  return history.filter((h) => h.env === env);
}

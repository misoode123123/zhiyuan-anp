"use client";

import { createContext, useCallback, useContext, useState } from "react";

export type Tab = { path: string; label: string; icon: string; search?: string };

type Ctx = {
  tabs: Tab[];
  addTab: (t: Tab) => void;
  updateSearch: (path: string, search: string) => void;
  close: (path: string) => void;
  refreshKey: number;
  refresh: () => void;
};

const TabCtx = createContext<Ctx | null>(null);

export function TabProvider({ children }: { children: React.ReactNode }) {
  const [tabs, setTabs] = useState<Tab[]>([{ path: "/", label: "概览", icon: "📊" }]);
  const [refreshKey, setRefreshKey] = useState(0);

  const addTab = useCallback((t: Tab) => {
    setTabs((prev) => (prev.find((x) => x.path === t.path) ? prev : [...prev, t]));
  }, []);
  // 记录某 tab 最后一次激活时的 query 串（如 workspace 的 ?app=&ps=），
  // 切换 tab 时拼回完整 URL，避免丢参数（修「缺少 app/ps 参数」报错）。
  const updateSearch = useCallback((path: string, search: string) => {
    setTabs((prev) => prev.map((x) => (x.path === path ? { ...x, search } : x)));
  }, []);
  const close = useCallback((path: string) => {
    setTabs((prev) => prev.filter((x) => x.path !== path));
  }, []);
  const refresh = useCallback(() => setRefreshKey((k) => k + 1), []);

  return (
    <TabCtx.Provider value={{ tabs, addTab, updateSearch, close, refreshKey, refresh }}>
      {children}
    </TabCtx.Provider>
  );
}

export function useTabs() {
  const c = useContext(TabCtx);
  if (!c) throw new Error("useTabs 必须在 TabProvider 内使用");
  return c;
}

// 路径 → 标签页元信息（侧边导航 + 打开 tab 共用）
export const NAV_MAP: Record<string, Tab> = {
  "/": { path: "/", label: "概览", icon: "📊" },
  "/requirements": { path: "/requirements", label: "需求工作台", icon: "💬" },
  "/team": { path: "/team", label: "团队看板", icon: "📋" },
  "/performance": { path: "/performance", label: "绩效记录", icon: "📈" },
  "/dev": { path: "/dev", label: "研发工作台", icon: "💻" },
  "/testing": { path: "/testing", label: "测试中心", icon: "🧪" },
  "/release": { path: "/release", label: "发布中心", icon: "🚀" },
  "/ops": { path: "/ops", label: "运维中心", icon: "🛠️" },
  "/governance": { path: "/governance", label: "规则治理", icon: "⭐" },
  "/security": { path: "/security", label: "安全合规", icon: "🛡️" },
  "/compute": { path: "/compute", label: "算力资源", icon: "⚡" },
  "/capabilities": { path: "/capabilities", label: "AI能力市场", icon: "🧩" },
  "/applications": { path: "/applications", label: "应用部署", icon: "📦" },
  "/servers": { path: "/servers", label: "服务器管理", icon: "🖥" },
  "/databases": { path: "/databases", label: "数据库管理", icon: "🗄️" },
  "/workspace": { path: "/workspace", label: "编码工作台", icon: "🧑‍💻" },
  "/admin/config": { path: "/admin/config", label: "系统配置", icon: "⚙️" },
  "/admin/users": { path: "/admin/users", label: "用户权限", icon: "🔐" },
  "/admin/logs": { path: "/admin/logs", label: "系统日志", icon: "📋" },
  "/approvals": { path: "/approvals", label: "变更审批", icon: "🚪" },
};

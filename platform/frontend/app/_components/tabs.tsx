"use client";

import { createContext, useCallback, useContext, useState } from "react";

export type Tab = { path: string; label: string; icon: string };

type Ctx = {
  tabs: Tab[];
  addTab: (t: Tab) => void;
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
  const close = useCallback((path: string) => {
    setTabs((prev) => prev.filter((x) => x.path !== path));
  }, []);
  const refresh = useCallback(() => setRefreshKey((k) => k + 1), []);

  return (
    <TabCtx.Provider value={{ tabs, addTab, close, refreshKey, refresh }}>
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
  "/dev": { path: "/dev", label: "研发工作台", icon: "💻" },
  "/testing": { path: "/testing", label: "测试中心", icon: "🧪" },
  "/release": { path: "/release", label: "发布中心", icon: "🚀" },
  "/ops": { path: "/ops", label: "运维中心", icon: "🛠️" },
  "/governance": { path: "/governance", label: "规则治理", icon: "⭐" },
  "/security": { path: "/security", label: "安全合规", icon: "🛡️" },
  "/compute": { path: "/compute", label: "算力资源", icon: "⚡" },
  "/capabilities": { path: "/capabilities", label: "AI能力市场", icon: "🧩" },
  "/attendance": { path: "/attendance", label: "考勤管理", icon: "🗓️" },
  "/applications": { path: "/applications", label: "应用部署", icon: "📦" },
  "/workspace": { path: "/workspace", label: "编码工作台", icon: "🧑‍💻" },
  "/admin/config": { path: "/admin/config", label: "系统配置", icon: "⚙️" },
  "/admin/users": { path: "/admin/users", label: "用户权限", icon: "🔐" },
  "/approvals": { path: "/approvals", label: "变更审批", icon: "🚪" },
};

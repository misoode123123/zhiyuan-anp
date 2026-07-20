"use client";

import { useEffect } from "react";
import { usePathname, useRouter } from "next/navigation";

import { NAV_MAP, TabProvider, useTabs } from "./tabs";
import { Sidebar } from "./sidebar";
import { WorkspaceSwitcher } from "./workspace-switcher";
import { UserSwitcher } from "./user-switcher";
import { TabBar } from "./tab-bar";
import { installAuthInterceptor, isLoggedIn } from "@/lib/api";

export function Shell({ children }: { children: React.ReactNode }) {
  return (
    <TabProvider>
      <ShellInner>{children}</ShellInner>
    </TabProvider>
  );
}

function ShellInner({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const router = useRouter();
  const { addTab, refreshKey } = useTabs();

  // 客户端挂载后安装全局 fetch 拦截（注入 Authorization / X-Project-Space-Id）。
  useEffect(() => {
    installAuthInterceptor();
  }, []);

  // 登录守卫:未登录且不在登录页 → 跳 /login(撤 X-User 模拟回退后强制真实登录)。
  useEffect(() => {
    if (pathname !== "/login" && !isLoggedIn()) {
      router.replace("/login");
    }
  }, [pathname, router]);

  // 路由变化 → 确保对应 tab 已打开
  useEffect(() => {
    const nav = NAV_MAP[pathname];
    if (nav) addTab(nav);
  }, [pathname, addTab]);

  return (
    <div className="flex min-h-screen">
      <aside className="flex w-56 shrink-0 flex-col gap-4 overflow-y-auto border-r border-neutral-200 bg-white p-4">
        <div className="text-lg font-bold">
          智源 <span className="text-blue-600">ANP</span>
        </div>
        <WorkspaceSwitcher />
        <UserSwitcher />
        <Sidebar />
        <div className="mt-auto text-xs text-neutral-400">v0.1.0</div>
      </aside>
      <div className="flex min-w-0 flex-1 flex-col">
        <TabBar />
        {/* key 变化（路由切换或双击刷新）→ 重新挂载，实现刷新 */}
        <main className="min-w-0 flex-1 p-4 md:p-6" key={pathname + refreshKey}>
          {children}
        </main>
      </div>
    </div>
  );
}

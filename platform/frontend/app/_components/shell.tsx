"use client";

import { useEffect, useState } from "react";
import { usePathname, useRouter } from "next/navigation";

import { NAV_MAP, TabProvider, useTabs } from "./tabs";
import { Sidebar } from "./sidebar";
import { WorkspaceSwitcher } from "./workspace-switcher";
import { UserSwitcher } from "./user-switcher";
import { TabBar } from "./tab-bar";
import { installAuthInterceptor, isLoggedIn } from "@/lib/api";
import { initErrorCapture } from "@/lib/error-report";
import { ToastContainer } from "@/lib/toast";
import { NotifBell } from "./notif-bell";
import { ThemeToggle } from "./theme-toggle";

export function Shell({ children }: { children: React.ReactNode }) {
  return (
    <TabProvider>
      <ShellInner>{children}</ShellInner>
      <ToastContainer />
    </TabProvider>
  );
}

function ShellInner({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const router = useRouter();
  const { addTab, updateSearch, refreshKey } = useTabs();
  const [sidebarOpen, setSidebarOpen] = useState(false); // 移动端抽屉

  useEffect(() => {
    installAuthInterceptor();
    initErrorCapture();
  }, []);

  useEffect(() => {
    if (pathname !== "/login" && !isLoggedIn()) {
      router.replace("/login");
    }
  }, [pathname, router]);

  useEffect(() => {
    const nav = NAV_MAP[pathname];
    if (nav) {
      addTab(nav);
      updateSearch(pathname, typeof window !== "undefined" ? window.location.search : "");
    }
  }, [pathname, addTab, updateSearch]);

  // 路由切换时关闭移动端侧边栏
  useEffect(() => {
    setSidebarOpen(false);
  }, [pathname]);

  return (
    <div className="flex min-h-screen">
      {/* 移动端遮罩 */}
      {sidebarOpen && (
        <div
          className="fixed inset-0 z-30 bg-black/30 lg:hidden"
          onClick={() => setSidebarOpen(false)}
        />
      )}

      {/* 侧边栏：桌面固定 / 移动端抽屉 */}
      <aside
        className={`fixed z-40 flex w-56 shrink-0 flex-col gap-4 overflow-y-auto border-r border-neutral-200 bg-[#f7f8fa] p-4 transition-transform lg:static lg:translate-x-0 ${
          sidebarOpen ? "translate-x-0" : "-translate-x-full lg:translate-x-0"
        }`}
        style={{ height: "100vh" }}
      >
        <div className="flex items-center justify-between">
          <div className="text-lg font-bold">
            智源 <span className="text-blue-600">ANP</span>
          </div>
          <div className="flex items-center gap-1">
            <ThemeToggle />
            <NotifBell />
            {/* 移动端关闭按钮 */}
            <button onClick={() => setSidebarOpen(false)} className="text-text-muted lg:hidden">
              ×
            </button>
          </div>
        </div>
        <WorkspaceSwitcher />
        <UserSwitcher />
        <Sidebar />
        <div className="mt-auto text-xs text-neutral-400">v0.1.0</div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        {/* 移动端顶栏（汉堡 + 标题） */}
        <div className="flex items-center gap-2 border-b border-neutral-200 px-3 py-2 lg:hidden">
          <button
            onClick={() => setSidebarOpen(true)}
            className="rounded p-1.5 hover:bg-neutral-100"
            aria-label="菜单"
          >
            <svg
              width="20"
              height="20"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
            >
              <line x1="3" y1="6" x2="21" y2="6" />
              <line x1="3" y1="12" x2="21" y2="12" />
              <line x1="3" y1="18" x2="21" y2="18" />
            </svg>
          </button>
          <span className="text-sm font-semibold">智源 ANP</span>
        </div>
        <TabBar />
        <main className="min-w-0 flex-1 p-4 md:p-6" key={pathname + refreshKey}>
          {children}
        </main>
      </div>
    </div>
  );
}

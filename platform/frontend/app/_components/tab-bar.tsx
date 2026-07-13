"use client";

import { usePathname, useRouter } from "next/navigation";

import { useTabs } from "./tabs";

export function TabBar() {
  const router = useRouter();
  const pathname = usePathname();
  const { tabs, close, refresh } = useTabs();

  return (
    <div className="flex items-center gap-1 overflow-x-auto border-b border-neutral-200 bg-neutral-100 px-2 py-1">
      {tabs.map((t) => {
        const active = pathname === t.path;
        return (
          <div
            key={t.path}
            onClick={() => router.push(t.path)}
            onDoubleClick={() => refresh()}
            className={`flex cursor-pointer items-center gap-1 whitespace-nowrap rounded-t px-3 py-1.5 text-xs ${
              active
                ? "border-t-2 border-blue-600 bg-white text-blue-700"
                : "text-neutral-600 hover:bg-neutral-200"
            }`}
            title="单击切换 · 双击刷新"
          >
            <span>{t.icon}</span>
            <span>{t.label}</span>
            {t.path !== "/" && (
              <button
                onClick={(e) => {
                  e.stopPropagation();
                  close(t.path);
                  if (pathname === t.path) router.push("/");
                }}
                className="ml-1 text-neutral-400 hover:text-red-600"
                title="关闭"
              >
                ×
              </button>
            )}
          </div>
        );
      })}
    </div>
  );
}

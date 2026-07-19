"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { API_BASE_URL, clearAuthToken, currentUser, isLoggedIn } from "@/lib/api";

// 用户区：已登录显示用户名 + 登出；未登录显示登录入口（未登录以游客 X-User 模拟访问）。
export function UserSwitcher() {
  const [loggedIn, setLoggedIn] = useState(false);
  const [user, setUser] = useState(currentUser());

  useEffect(() => {
    const sync = () => {
      setLoggedIn(isLoggedIn());
      setUser(currentUser());
    };
    sync();
    window.addEventListener("anp:user-changed", sync);
    return () => window.removeEventListener("anp:user-changed", sync);
  }, []);

  async function logout() {
    try {
      await fetch(`${API_BASE_URL}/auth/logout`, { method: "POST" });
    } catch {}
    clearAuthToken();
  }

  if (loggedIn) {
    return (
      <div>
        <div className="mb-1 text-xs text-neutral-500">已登录</div>
        <div className="flex items-center justify-between">
          <span className="truncate font-medium">👤 {user}</span>
          <button onClick={logout} className="rounded bg-neutral-100 px-2 py-0.5 text-xs">
            登出
          </button>
        </div>
      </div>
    );
  }
  return (
    <div>
      <div className="mb-1 text-xs text-neutral-500">未登录</div>
      <Link
        href="/login"
        className="block rounded bg-blue-600 px-2 py-1 text-center text-xs text-white"
      >
        🔑 登录
      </Link>
      <div className="mt-1 text-[11px] text-neutral-400">未登录以游客(X-User 模拟)访问</div>
    </div>
  );
}

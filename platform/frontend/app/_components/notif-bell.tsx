"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { API_BASE_URL, getAuthToken } from "@/lib/api";

type NotifItem = {
  id: number;
  type: string;
  title: string;
  message?: string;
  link?: string;
  read: boolean;
  created_at: string;
};

export function NotifBell() {
  const router = useRouter();
  const [items, setItems] = useState<NotifItem[]>([]);
  const [unread, setUnread] = useState(0);
  const [open, setOpen] = useState(false);

  // 初始加载
  const load = () => {
    fetch(`${API_BASE_URL}/notifications?limit=10`, {
      headers: { Authorization: `Bearer ${getAuthToken()}` },
    })
      .then((r) => r.json())
      .then((r) => {
        setItems(r.data?.items ?? []);
        setUnread(r.data?.unread_count ?? 0);
      })
      .catch(() => {});
  };

  useEffect(() => {
    load();
    // 定时轮询（每 30s 兜底，SSE 失败时也能收到）
    const timer = setInterval(load, 30000);
    return () => clearInterval(timer);
  }, []);

  // SSE 实时推送
  useEffect(() => {
    const token = getAuthToken();
    if (!token) return;
    const es = new EventSource(`${API_BASE_URL}/notifications/stream`, {
      withCredentials: true,
    });
    // EventSource 不支持自定义 header，用轮询兜底（SSE 可能 401）
    es.onmessage = (e) => {
      try {
        const data = JSON.parse(e.data);
        if (data.type === "unread") {
          setUnread(data.count);
        } else {
          // 新通知
          setItems((prev) => [data, ...prev].slice(0, 10));
          setUnread((n) => n + 1);
        }
      } catch {}
    };
    es.onerror = () => es.close();
    return () => es.close();
  }, []);

  async function markRead(id: number) {
    fetch(`${API_BASE_URL}/notifications/${id}/read`, { method: "PATCH" });
    setItems((prev) => prev.map((i) => (i.id === id ? { ...i, read: true } : i)));
    setUnread((n) => Math.max(0, n - 1));
  }

  async function markAllRead() {
    fetch(`${API_BASE_URL}/notifications/read-all`, { method: "POST" });
    setItems((prev) => prev.map((i) => ({ ...i, read: true })));
    setUnread(0);
  }

  return (
    <div className="relative">
      <button
        onClick={() => setOpen(!open)}
        className="relative rounded-lg p-1.5 text-text-muted hover:bg-surface-2"
        aria-label="通知"
      >
        {/* 铃铛 SVG */}
        <svg
          width="20"
          height="20"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
        >
          <path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9" />
          <path d="M13.73 21a2 2 0 0 1-3.46 0" />
        </svg>
        {unread > 0 && (
          <span className="absolute -right-0.5 -top-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-red-500 px-1 text-[10px] font-bold text-white">
            {unread > 9 ? "9+" : unread}
          </span>
        )}
      </button>

      {open && (
        <>
          <div className="fixed inset-0 z-40" onClick={() => setOpen(false)} />
          <div className="absolute right-0 top-full z-50 mt-1 w-80 rounded-lg border border-border bg-surface shadow-xl">
            <div className="flex items-center justify-between border-b px-3 py-2">
              <span className="text-sm font-semibold">通知</span>
              {unread > 0 && (
                <button onClick={markAllRead} className="text-xs text-accent">
                  全部已读
                </button>
              )}
            </div>
            <div className="max-h-80 overflow-y-auto">
              {items.length === 0 && (
                <div className="px-3 py-6 text-center text-sm text-text-muted">暂无通知</div>
              )}
              {items.map((n) => (
                <div
                  key={n.id}
                  className={`cursor-pointer border-b px-3 py-2 hover:bg-bg ${n.read ? "opacity-50" : ""}`}
                  onClick={() => {
                    markRead(n.id);
                    if (n.link) router.push(n.link);
                    setOpen(false);
                  }}
                >
                  <div className="flex items-start gap-2">
                    {!n.read && <span className="mt-1 h-2 w-2 shrink-0 rounded-full bg-accent" />}
                    <div className="min-w-0 flex-1">
                      <div className="text-sm font-medium">{n.title}</div>
                      {n.message && <div className="text-xs text-text-muted">{n.message}</div>}
                      <div className="text-[10px] text-text-muted">
                        {new Date(n.created_at).toLocaleString("zh-CN", { hour12: false })}
                      </div>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          </div>
        </>
      )}
    </div>
  );
}

"use client";

import { useEffect, useState, createElement } from "react";

export type ToastType = "success" | "error" | "info" | "warn";
export interface Toast {
  id: number;
  type: ToastType;
  message: string;
}

const TOAST_EVENT = "anp-toast";
let nextId = 1;

export function showToast(type: ToastType, message: string, duration = 3000) {
  if (typeof window === "undefined") return;
  const t = { id: nextId++, type, message };
  window.dispatchEvent(new CustomEvent(TOAST_EVENT, { detail: t }));
  setTimeout(() => {
    window.dispatchEvent(new CustomEvent(TOAST_EVENT, { detail: { ...t, remove: true } }));
  }, duration);
}

export const toast = {
  success: (msg: string) => showToast("success", msg),
  error: (msg: string) => showToast("error", msg),
  info: (msg: string) => showToast("info", msg),
  warn: (msg: string) => showToast("warn", msg),
};

export function ToastContainer() {
  const [list, setList] = useState<Toast[]>([]);

  useEffect(() => {
    const handler = (e: Event) => {
      const detail = (e as CustomEvent).detail as Toast & { remove?: boolean };
      if (detail.remove) {
        setList((prev) => prev.filter((t) => t.id !== detail.id));
      } else {
        setList((prev) => [...prev, { id: detail.id, type: detail.type, message: detail.message }]);
      }
    };
    window.addEventListener(TOAST_EVENT, handler);
    return () => window.removeEventListener(TOAST_EVENT, handler);
  }, []);

  if (list.length === 0) return null;

  const colors: Record<ToastType, string> = {
    success: "bg-emerald-600",
    error: "bg-red-600",
    info: "bg-blue-600",
    warn: "bg-amber-600",
  };

  return createElement(
    "div",
    { className: "fixed bottom-4 right-4 z-[100] flex flex-col gap-2" },
    list.map((t) =>
      createElement(
        "div",
        {
          key: t.id,
          className: colors[t.type] + " rounded-lg px-4 py-2 text-sm text-white shadow-lg",
        },
        t.message
      )
    )
  );
}

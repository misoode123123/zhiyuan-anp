// 前端错误捕获：自动回传到后端 /api/v1/logs
// 在 layout Shell 中初始化（一次）

const LOG_ENDPOINT = "/api/v1/logs";

// 批量缓冲（避免高频错误刷爆后端）
let buffer: Array<{
  level: string;
  source: string;
  message: string;
  fields?: Record<string, unknown>;
}> = [];
let flushTimer: ReturnType<typeof setTimeout> | null = null;

function scheduleFlush() {
  if (flushTimer) return;
  flushTimer = setTimeout(() => {
    const batch = buffer.splice(0);
    flushTimer = null;
    for (const item of batch) {
      try {
        fetch(LOG_ENDPOINT, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(item),
          keepalive: true, // 页面卸载也能发
        }).catch(() => {});
      } catch {}
    }
  }, 2000);
}

export function reportError(message: string, fields?: Record<string, unknown>) {
  buffer.push({ level: "ERROR", source: "frontend", message, fields });
  scheduleFlush();
}

export function reportWarn(message: string, fields?: Record<string, unknown>) {
  buffer.push({ level: "WARN", source: "frontend", message, fields });
  scheduleFlush();
}

// API 调用失败时调用
export function reportApiError(path: string, status: number, body?: string) {
  reportError(`API ${path} → ${status}`, {
    method: "api",
    path,
    status,
    response: body?.slice(0, 500),
  });
}

// 初始化全局错误捕获（在 Shell 组件 useEffect 中调一次）
export function initErrorCapture() {
  if (typeof window === "undefined") return;

  // JS 运行时错误
  window.addEventListener("error", (e) => {
    reportError(e.message || "Unknown error", {
      stack: e.error?.stack,
      url: location.href,
      line: e.lineno,
      col: e.colno,
    });
  });

  // Promise 未捕获拒绝
  window.addEventListener("unhandledrejection", (e) => {
    const reason = e.reason;
    reportError(`Unhandled rejection: ${reason?.message || reason}`, { stack: reason?.stack });
  });
}

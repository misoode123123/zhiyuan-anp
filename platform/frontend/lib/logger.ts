// lib/logger.ts 前端统一日志入口（M5-3）：info/warn/error 主动埋点，
// 统一回传后端 platform_log（fetch 拦截自动带 trace_id）。
// 与 error-report.ts 互补：error-report 自动捕获未处理异常；logger 供业务事件主动埋点。
// 智能体可经 /logs/query 查到这些前端事件（source=frontend）。
import { API_BASE_URL } from "./api";

type Fields = Record<string, unknown>;

export const logger = {
  info(event: string, fields?: Fields) {
    post("INFO", event, fields);
  },
  warn(event: string, fields?: Fields) {
    post("WARN", event, fields);
  },
  error(event: string, fields?: Fields) {
    post("ERROR", event, fields);
  },
};

function post(level: string, message: string, fields?: Fields) {
  if (typeof window === "undefined") return;
  try {
    fetch(`${API_BASE_URL}/logs`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ level, source: "frontend", message, fields: fields ?? {} }),
      keepalive: true,
    });
  } catch {}
}

"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL } from "@/lib/api";

type Envelope<T> = { code: number; data: T };
type LogEntry = {
  id: number;
  timestamp: string;
  level: string;
  source: string;
  module?: string;
  trace_id?: string;
  user_id?: string;
  message: string;
  stack_trace?: string;
  context?: string;
  resolved: boolean;
  resolved_by?: string;
};

const LEVEL_COLOR: Record<string, string> = {
  ERROR: "bg-danger/10 text-danger",
  FATAL: "bg-danger/20 text-danger",
  WARN: "bg-warn/10 text-warn",
  INFO: "bg-accent/10 text-accent",
  DEBUG: "bg-surface-2 text-text-muted",
};

const SOURCE_ICON: Record<string, string> = {
  frontend: "🌐",
  backend: "⚙️",
  "agent-runtime": "🤖",
};

export default function LogsPage() {
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [stats, setStats] = useState<{
    total_logs: number;
    unresolved_errors: number;
    today_errors: number;
    by_source: Record<string, number>;
  } | null>(null);
  const [trend, setTrend] = useState<{ date: string; errors: number; warns: number }[]>([]);
  const [sources, setSources] = useState<{ source: string; level: string; count: number }[]>([]);
  const [level, setLevel] = useState("");
  const [source, setSource] = useState("");
  const [selected, setSelected] = useState<LogEntry | null>(null);
  const [msg, setMsg] = useState("");

  const load = () => {
    const q = new URLSearchParams();
    if (level) q.set("level", level);
    if (source) q.set("source", source);
    q.set("limit", "50");
    fetch(`${API_BASE_URL}/logs?${q}`)
      .then((r) => r.json())
      .then((r: Envelope<LogEntry[]>) => setLogs(r.data ?? []));
    fetch(`${API_BASE_URL}/logs/stats`)
      .then((r) => r.json())
      .then((r: Envelope<typeof stats>) => setStats(r.data));
    fetch(`${API_BASE_URL}/logs/trend`)
      .then((r) => r.json())
      .then((r: Envelope<typeof trend>) => setTrend(r.data ?? []));
    fetch(`${API_BASE_URL}/logs/sources`)
      .then((r) => r.json())
      .then((r: Envelope<typeof sources>) => setSources(r.data ?? []));
  };
  useEffect(() => {
    load();
  }, [level, source]);

  async function resolve(id: number) {
    const res = await fetch(`${API_BASE_URL}/logs/${id}/resolve`, { method: "PATCH" });
    if ((await res.json()).code === 0) {
      setMsg(`✓ #${id} 已标记处理`);
      load();
    }
  }

  let ctxParsed: Record<string, unknown> = {};
  try {
    ctxParsed = selected?.context ? JSON.parse(selected.context) : {};
  } catch {}

  return (
    <div>
      <h1 className="mb-1 text-xl font-bold">📋 系统日志</h1>
      <p className="mb-4 text-sm text-text-muted">
        跨层（前端/后端/Python）统一错误日志 · 前端报错自动回传
      </p>

      {/* 统计卡片 */}
      {stats && (
        <div className="mb-4 grid grid-cols-2 gap-3 sm:grid-cols-4">
          {[
            { l: "总日志", v: stats.total_logs, c: "text-text" },
            {
              l: "未处理错误",
              v: stats.unresolved_errors,
              c: stats.unresolved_errors > 0 ? "text-danger" : "text-success",
            },
            {
              l: "今日错误",
              v: stats.today_errors,
              c: stats.today_errors > 0 ? "text-danger" : "text-text",
            },
            { l: "来源数", v: Object.keys(stats.by_source).length, c: "text-accent" },
          ].map((s) => (
            <div key={s.l} className="rounded-lg border bg-surface p-3">
              <div className="text-xs text-text-muted">{s.l}</div>
              <div className={`text-xl font-bold ${s.c}`}>{s.v}</div>
            </div>
          ))}
        </div>
      )}

      {/* 筛选 */}
      <div className="mb-3 flex gap-2">
        <select
          value={level}
          onChange={(e) => setLevel(e.target.value)}
          className="rounded-md border px-2 py-1 text-sm"
        >
          <option value="">全部级别</option>
          <option value="ERROR">ERROR</option>
          <option value="WARN">WARN</option>
          <option value="INFO">INFO</option>
          <option value="FATAL">FATAL</option>
        </select>
        <select
          value={source}
          onChange={(e) => setSource(e.target.value)}
          className="rounded-md border px-2 py-1 text-sm"
        >
          <option value="">全部来源</option>
          <option value="frontend">🌐 前端</option>
          <option value="backend">⚙️ 后端</option>
          <option value="agent-runtime">🤖 Python</option>
        </select>
        <button onClick={load} className="rounded-md bg-surface-2 px-3 py-1 text-sm">
          🔄 刷新
        </button>
      </div>

      {/* 趋势 + 来源分布 */}
      <div className="mb-4 grid grid-cols-1 gap-4 lg:grid-cols-3">
        {/* 趋势柱状图 */}
        <div className="rounded-lg border bg-surface p-3 lg:col-span-2">
          <div className="mb-2 text-sm font-semibold">近 7 天 ERROR/WARN 趋势</div>
          <div className="flex items-end gap-1" style={{ height: "120px" }}>
            {trend.map((t, i) => {
              const maxVal = Math.max(...trend.map((x) => x.errors + x.warns), 1);
              const errH = (t.errors / maxVal) * 100;
              const warnH = (t.warns / maxVal) * 100;
              return (
                <div key={i} className="flex flex-1 flex-col items-center gap-1">
                  <div className="flex w-full flex-col justify-end" style={{ height: "90px" }}>
                    <div
                      className="w-full rounded-t bg-warn"
                      style={{ height: `${warnH}%` }}
                      title={`WARN: ${t.warns}`}
                    />
                    <div
                      className="w-full bg-danger"
                      style={{ height: `${errH}%` }}
                      title={`ERROR: ${t.errors}`}
                    />
                  </div>
                  <span className="text-xs text-text-muted">{t.date.slice(5)}</span>
                </div>
              );
            })}
            {trend.length === 0 && <div className="m-auto text-sm text-text-muted">暂无数据</div>}
          </div>
          <div className="mt-1 flex gap-3 text-xs">
            <span className="flex items-center gap-1">
              <span className="inline-block h-2 w-2 rounded bg-danger"></span>ERROR
            </span>
            <span className="flex items-center gap-1">
              <span className="inline-block h-2 w-2 rounded bg-warn"></span>WARN
            </span>
          </div>
        </div>

        {/* 来源分布 */}
        <div className="rounded-lg border bg-surface p-3">
          <div className="mb-2 text-sm font-semibold">来源分布（近 7 天）</div>
          <div className="space-y-2">
            {sources.length > 0 ? (
              sources.slice(0, 8).map((s, i) => {
                const total = sources.reduce((a, b) => a + b.count, 0) || 1;
                return (
                  <div key={i} className="flex items-center gap-2 text-xs">
                    <span className="w-20 truncate">
                      {SOURCE_ICON[s.source] || "❓"} {s.source}
                    </span>
                    <span className={`rounded px-1 ${LEVEL_COLOR[s.level] || "bg-surface-2"}`}>
                      {s.level}
                    </span>
                    <div className="flex-1 rounded-full bg-surface-2">
                      <div
                        className={`h-4 rounded-full ${s.level === "ERROR" || s.level === "FATAL" ? "bg-danger" : s.level === "WARN" ? "bg-warn" : "bg-accent"}`}
                        style={{ width: `${Math.max(8, (s.count / total) * 100)}%` }}
                      />
                    </div>
                    <span className="w-8 text-right">{s.count}</span>
                  </div>
                );
              })
            ) : (
              <div className="text-sm text-text-muted">暂无数据</div>
            )}
          </div>
        </div>
      </div>

      {/* 日志列表 */}
      <div className="mb-2 text-sm font-semibold">日志列表</div>
      <div className="mb-3 flex gap-2">
        <select
          value={level}
          onChange={(e) => setLevel(e.target.value)}
          className="rounded-md border px-2 py-1 text-sm"
        >
          <option value="">全部级别</option>
          <option value="ERROR">ERROR</option>
          <option value="WARN">WARN</option>
          <option value="INFO">INFO</option>
          <option value="FATAL">FATAL</option>
        </select>
        <select
          value={source}
          onChange={(e) => setSource(e.target.value)}
          className="rounded-md border px-2 py-1 text-sm"
        >
          <option value="">全部来源</option>
          <option value="frontend">🌐 前端</option>
          <option value="backend">⚙️ 后端</option>
          <option value="agent-runtime">🤖 Python</option>
        </select>
        <button onClick={load} className="rounded-md bg-surface-2 px-3 py-1 text-sm">
          🔄 刷新
        </button>
      </div>
      {msg && <div className="mb-2 text-sm text-accent">{msg}</div>}

      {/* 列表 */}
      <div className="space-y-1">
        {logs.map((l) => (
          <div
            key={l.id}
            className={`flex items-center gap-2 rounded-md border bg-surface p-2 text-sm ${l.resolved ? "opacity-50" : ""}`}
          >
            <span
              className={`shrink-0 rounded px-1.5 py-0.5 text-xs font-mono ${LEVEL_COLOR[l.level] || "bg-surface-2"}`}
            >
              {l.level}
            </span>
            <span className="shrink-0 text-xs">
              {SOURCE_ICON[l.source] || "❓"} {l.source}
            </span>
            <span className="shrink-0 text-xs text-text-muted">
              {new Date(l.timestamp).toLocaleString("zh-CN", { hour12: false })}
            </span>
            <span
              className="flex-1 truncate cursor-pointer hover:text-accent"
              onClick={() => setSelected(l)}
            >
              {l.message}
            </span>
            {l.module && (
              <span className="shrink-0 rounded bg-surface-2 px-1 text-xs">{l.module}</span>
            )}
            {l.resolved ? (
              <span className="shrink-0 text-xs text-success">✓ {l.resolved_by}</span>
            ) : (
              <button
                onClick={() => resolve(l.id)}
                className="shrink-0 rounded bg-accent px-1.5 py-0.5 text-xs text-white"
              >
                处理
              </button>
            )}
          </div>
        ))}
        {logs.length === 0 && <div className="text-sm text-text-muted">暂无日志</div>}
      </div>

      {/* 详情弹窗 */}
      {selected && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-4"
          onClick={() => setSelected(null)}
        >
          <div
            className="max-h-80vh max-w-2xl overflow-auto rounded-lg bg-surface p-4 shadow-xl"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="mb-2 flex items-center gap-2">
              <span
                className={`rounded px-1.5 py-0.5 text-xs font-mono ${LEVEL_COLOR[selected.level]}`}
              >
                {selected.level}
              </span>
              <span className="text-xs">
                {SOURCE_ICON[selected.source]} {selected.source}
              </span>
              {selected.module && (
                <span className="rounded bg-surface-2 px-1 text-xs">{selected.module}</span>
              )}
              <span className="ml-auto text-xs text-text-muted">#{selected.id}</span>
            </div>
            <div className="mb-2 text-sm font-medium">{selected.message}</div>
            <div className="mb-2 text-xs text-text-muted">
              {new Date(selected.timestamp).toLocaleString("zh-CN", { hour12: false })}
            </div>
            {selected.trace_id && (
              <div className="mb-2 text-xs">
                <span className="text-text-muted">trace:</span>{" "}
                <code className="rounded bg-surface-2 px-1">{selected.trace_id}</code>
              </div>
            )}
            {selected.stack_trace && (
              <div className="mb-2">
                <div className="text-xs text-text-muted">堆栈</div>
                <pre className="mt-1 max-h-40 overflow-auto rounded bg-neutral-900 p-2 text-xs text-green-300">
                  {selected.stack_trace}
                </pre>
              </div>
            )}
            {Object.keys(ctxParsed).length > 0 && (
              <div className="mb-2">
                <div className="text-xs text-text-muted">上下文</div>
                <pre className="mt-1 rounded bg-surface-2 p-2 text-xs">
                  {JSON.stringify(ctxParsed, null, 2)}
                </pre>
              </div>
            )}
            <div className="flex gap-2">
              {!selected.resolved && (
                <button
                  onClick={() => {
                    resolve(selected.id);
                    setSelected(null);
                  }}
                  className="rounded bg-accent px-3 py-1 text-sm text-white"
                >
                  标记已处理
                </button>
              )}
              <button
                onClick={() => setSelected(null)}
                className="rounded bg-surface-2 px-3 py-1 text-sm"
              >
                关闭
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

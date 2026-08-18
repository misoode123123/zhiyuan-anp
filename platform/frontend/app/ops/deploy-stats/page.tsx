"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL, getAuthToken } from "@/lib/api";

type ErrFreq = { fragment: string; count: number };
type EngineStats = {
  engine: string;
  success: number;
  failed: number;
  avg_sec: number;
  med_sec: number;
  top_errors: ErrFreq[];
};
type DailyCount = { day: string; engine: string; success: number; failed: number };
type StatsResp = { engines: EngineStats[]; daily: DailyCount[] };

export default function DeployStatsPage() {
  const [days, setDays] = useState(30);
  const [data, setData] = useState<StatsResp | null>(null);
  const [err, setErr] = useState("");

  // 注：重置放在 onChange（事件处理器）而非 effect 内同步 setState，避免
  // react-hooks/set-state-in-effect 告警；行为等价：切换窗口→清旧数据→重新拉取。
  useEffect(() => {
    let stale = false; // 过期响应守卫：days 快速切换时旧请求晚归不得覆盖新窗口数据
    fetch(`${API_BASE_URL}/appdeploy/deploy-stats?days=${days}`, {
      headers: { Authorization: `Bearer ${getAuthToken()}` },
    })
      .then((r) => r.json())
      .then((r) => {
        if (stale) return;
        if (r.code !== 0) throw new Error(r.message || String(r.code));
        setData(r.data);
      })
      .catch((e) => {
        if (!stale) setErr(String(e));
      });
    return () => {
      stale = true;
    };
  }, [days]);

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
        <h1 className="text-lg font-semibold">部署统计</h1>
        <select
          value={days}
          onChange={(e) => {
            setData(null);
            setErr("");
            setDays(Number(e.target.value));
          }}
          className="rounded border border-border bg-surface px-2 py-1 text-sm"
        >
          {[7, 14, 30, 60, 90].map((d) => (
            <option key={d} value={d}>
              近 {d} 天
            </option>
          ))}
        </select>
      </div>

      {err && <div className="text-danger">加载失败：{err}</div>}
      {!data && !err && <div className="text-text-muted">加载中…</div>}

      {data && data.engines.length === 0 && <div className="text-text-muted">窗口内无部署记录</div>}

      {data?.engines.map((e) => {
        const total = e.success + e.failed;
        const rate = total > 0 ? Math.round((e.success / total) * 100) : 0;
        return (
          <div key={e.engine} className="rounded-lg border border-border bg-surface p-4">
            <div className="flex items-baseline gap-3">
              <span className="font-medium">
                {e.engine === "ai" ? "🤖 AI 引擎" : "🔧 固定引擎"}
              </span>
              <span className="text-2xl font-bold text-success">{rate}%</span>
              <span className="text-sm text-text-muted">
                成功率（{e.success}/{total}）
              </span>
            </div>
            <div className="mt-2 text-sm text-text-muted">
              平均耗时 {e.avg_sec}s · 中位 {e.med_sec}s · 失败 {e.failed} 次
            </div>
            {e.top_errors.length > 0 && (
              <div className="mt-2">
                <div className="text-sm text-text-muted">失败 top 原因：</div>
                {e.top_errors.map((t) => (
                  <div key={t.fragment} className="text-sm">
                    <span className="text-danger">{t.count}</span> × {t.fragment}
                  </div>
                ))}
              </div>
            )}
          </div>
        );
      })}

      {data && data.daily.length > 0 && (
        <div className="rounded-lg border border-border bg-surface p-4">
          <div className="mb-2 font-medium">每日部署（新→旧）</div>
          <table className="w-full text-sm">
            <thead>
              <tr className="text-text-muted">
                <th className="text-left">日期</th>
                <th className="text-left">引擎</th>
                <th className="text-right">成功</th>
                <th className="text-right">失败</th>
              </tr>
            </thead>
            <tbody>
              {data.daily.map((d) => (
                <tr key={`${d.day}-${d.engine}`} className="border-t border-border">
                  <td>{d.day}</td>
                  <td>{d.engine === "ai" ? "🤖" : "🔧"}</td>
                  <td className="text-right text-success">{d.success}</td>
                  <td className="text-right text-danger">{d.failed}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

"use client";

import { useEffect, useState } from "react";
import { useSearchParams } from "next/navigation";
import { API_BASE_URL } from "@/lib/api";

// 从 query 取 app/ps/tool，调 /workspace 拿 opencode url 后全屏嵌入。
// 无状态设计：刷新 tab / 点重连重新调接口（后端 Ensure 复用同 app+user 的 opencode 进程，不重复起）。
// query 由应用卡片「编码」按钮注入：/workspace?app=<id>&ps=<psID>&tool=<opencode|...>
//
// 注意：effect 体内不同步 setState（react-hooks/set-state-in-effect 规则）——
// 参数缺失用派生值渲染；loading 重置在「重连」事件处理器里做；其余 setState 都在 fetch 回调中。
export default function WorkspaceFrame() {
  const sp = useSearchParams();
  const appID = sp.get("app") || "";
  const psID = sp.get("ps") || "";
  const tool = sp.get("tool") || "opencode";
  const missingParams = !appID || !psID;
  const [url, setUrl] = useState("");
  const [err, setErr] = useState("");
  const [loading, setLoading] = useState(true);
  const [reloadKey, setReloadKey] = useState(0);

  useEffect(() => {
    if (missingParams) return; // 参数缺失不 fetch；渲染层派生提示文案
    let aborted = false;
    fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/workspace`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ tool }),
    })
      .then((r) => r.json())
      .then((r) => {
        if (aborted) return;
        if (r.code === 0 && r.data?.url) {
          setUrl(r.data.deep_url || r.data.url);
          setErr("");
        } else {
          setErr(r.message || "启动编码工作台失败");
        }
        setLoading(false);
      })
      .catch((e) => {
        if (!aborted) {
          setErr(String(e));
          setLoading(false);
        }
      });
    return () => {
      aborted = true;
    };
  }, [appID, psID, tool, reloadKey, missingParams]);

  const showErr = missingParams ? "缺少 app/ps 参数（请从应用卡片点「编码」进入）" : err;

  return (
    <div className="-m-4 flex h-[calc(100vh-2.25rem)] flex-col md:-m-6">
      <div className="flex items-center justify-between border-b border-neutral-200 bg-neutral-50 px-3 py-1 text-xs">
        <span className="truncate text-neutral-500">
          🧑‍💻 编码工作台 · <span className="font-mono text-neutral-700">{appID || "?"}</span> · {tool}
        </span>
        <span className="flex shrink-0 gap-3">
          <a href="/applications" className="text-blue-600">← 返回应用</a>
          {url && (
            <a href={url} target="_blank" rel="noreferrer" className="text-blue-600">↗ 新窗口</a>
          )}
          <button
            onClick={() => {
              setUrl("");
              setErr("");
              setLoading(true);
              setReloadKey((k) => k + 1);
            }}
            className="text-neutral-500"
          >
            重连
          </button>
        </span>
      </div>
      {loading && !missingParams && (
        <div className="p-4 text-sm text-neutral-500">启动 opencode 工作台…（首次约 3-5 秒）</div>
      )}
      {showErr && <div className="p-4 text-sm text-red-600">{showErr}</div>}
      {url && <iframe src={url} className="min-h-0 w-full flex-1" title="opencode 编码工作台" />}
    </div>
  );
}

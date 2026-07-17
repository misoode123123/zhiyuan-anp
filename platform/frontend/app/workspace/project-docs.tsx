"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL } from "@/lib/api";

type Doc = { path: string; name: string };

// 当前应用 repo 的文档列表(README/.md),点开看正文。
// 让开发者在编码 tab 侧边即可查阅项目文档结构,不必切到 opencode 文件树。
export function ProjectDocs({ psID, appID }: { psID: string; appID: string }) {
  const [docs, setDocs] = useState<Doc[]>([]);
  const [open, setOpen] = useState<string | null>(null);
  const [content, setContent] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!psID || !appID) return;
    let aborted = false;
    fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/repo-docs`)
      .then((r) => r.json())
      .then((r) => { if (!aborted) setDocs(r.data ?? []); })
      .catch(() => {});
    return () => { aborted = true; };
  }, [psID, appID]);

  async function toggle(path: string) {
    if (open === path) { setOpen(null); return; }
    setOpen(path);
    setLoading(true);
    try {
      const r = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/repo-file?path=${encodeURIComponent(path)}`).then((rr) => rr.json());
      setContent(r.data?.content ?? "(空)");
    } catch (e) {
      setContent(String(e));
    }
    setLoading(false);
  }

  if (docs.length === 0) return <div className="text-neutral-400">暂无文档(README/.md)</div>;
  return (
    <div>
      {docs.map((d) => (
        <div key={d.path}>
          <button onClick={() => toggle(d.path)} className="flex w-full gap-1 py-0.5 text-left">
            <span>{open === d.path ? "▾" : "▸"}</span>
            <span className="truncate text-neutral-700" title={d.path}>📄 {d.path}</span>
          </button>
          {open === d.path && (
            <pre className="mb-1 ml-3 max-h-64 overflow-auto whitespace-pre-wrap border-l border-neutral-300 bg-white p-1 text-[11px] text-neutral-700">
              {loading ? "加载中…" : content}
            </pre>
          )}
        </div>
      ))}
    </div>
  );
}

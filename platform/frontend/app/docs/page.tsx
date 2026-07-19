"use client";

import { useEffect, useState } from "react";
import ReactMarkdown from "react-markdown";
import { API_BASE_URL } from "@/lib/api";

type Envelope<T> = { code: number; data: T; message?: string };
type Doc = { path: string; title: string; category: string; mtime: string; summary: string };

// 用 Tailwind 任意子选择器样式化 react-markdown 输出，免装 typography 插件。
const MD =
  "max-w-none text-sm [&_h1]:mt-6 [&_h1]:mb-3 [&_h1]:text-2xl [&_h1]:font-bold [&_h2]:mt-5 [&_h2]:mb-2 [&_h2]:text-xl [&_h2]:font-semibold [&_h3]:mt-4 [&_h3]:mb-2 [&_h3]:font-semibold [&_h4]:mt-3 [&_h4]:mb-1 [&_h4]:font-semibold [&_p]:my-2 [&_p]:leading-7 [&_ul]:my-2 [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:my-2 [&_ol]:list-decimal [&_ol]:pl-5 [&_li]:my-0.5 [&_a]:text-blue-600 [&_a]:underline [&_strong]:font-semibold [&_code]:rounded [&_code]:bg-neutral-100 [&_code]:px-1 [&_code]:py-0.5 [&_code]:text-[0.85em] [&_pre]:my-3 [&_pre]:overflow-auto [&_pre]:rounded-lg [&_pre]:bg-neutral-900 [&_pre]:p-3 [&_pre_code]:bg-transparent [&_pre_code]:p-0 [&_pre_code]:text-green-300 [&_table]:my-3 [&_table]:border-collapse [&_table]:text-xs [&_th]:border [&_th]:border-neutral-300 [&_th]:px-2 [&_th]:py-1 [&_th]:bg-neutral-50 [&_th]:font-semibold [&_td]:border [&_td]:border-neutral-300 [&_td]:px-2 [&_td]:py-1 [&_blockquote]:border-l-4 [&_blockquote]:border-neutral-300 [&_blockquote]:pl-3 [&_blockquote]:text-neutral-600 [&_hr]:my-4 [&_hr]:border-neutral-200";

export default function DocsPage() {
  const [all, setAll] = useState<Doc[]>([]);
  const [q, setQ] = useState("");
  const [sel, setSel] = useState<string | null>(null);
  const [content, setContent] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    fetch(`${API_BASE_URL}/docs`)
      .then((r) => r.json())
      .then((r: Envelope<Doc[]>) => setAll(r.data ?? []));
  }, []);

  const filtered = q.trim()
    ? all.filter((d) =>
        (d.title + " " + d.summary + " " + d.path).toLowerCase().includes(q.toLowerCase())
      )
    : all;
  const grouped = filtered.reduce(
    (acc, d) => {
      (acc[d.category] ||= []).push(d);
      return acc;
    },
    {} as Record<string, Doc[]>
  );

  async function open(d: Doc) {
    setSel(d.path);
    setContent("");
    setLoading(true);
    try {
      const res = await fetch(`${API_BASE_URL}/docs/content?path=${encodeURIComponent(d.path)}`);
      const r = await res.json();
      setContent(r.data?.content ?? "(读取失败)");
    } catch (e) {
      setContent(`(读取失败: ${e})`);
    } finally {
      setLoading(false);
    }
  }

  return (
    <div>
      <h1 className="mb-1 text-xl font-bold">📄 方案文档中心</h1>
      <p className="mb-4 text-sm text-neutral-600">
        系统所有方案与设计文档（docs/），可搜索、可阅读。
      </p>
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[320px_1fr]">
        <div className="rounded-lg border border-neutral-200 bg-white p-3">
          <input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="🔍 搜索标题/摘要/路径"
            className="mb-3 w-full rounded border border-neutral-300 px-2 py-1.5 text-sm"
          />
          <div className="max-h-[72vh] space-y-3 overflow-auto">
            {Object.entries(grouped).map(([cat, list]) => (
              <div key={cat}>
                <div className="mb-1 text-xs font-semibold text-neutral-500">
                  {cat}（{list.length}）
                </div>
                {list.map((d) => (
                  <button
                    key={d.path}
                    onClick={() => open(d)}
                    className={`mb-1 block w-full rounded px-2 py-1.5 text-left text-sm ${sel === d.path ? "bg-blue-50 text-blue-700" : "hover:bg-neutral-100"}`}
                  >
                    <div className="font-medium">{d.title}</div>
                    <div className="truncate text-xs text-neutral-400">{d.summary}</div>
                  </button>
                ))}
              </div>
            ))}
            {filtered.length === 0 && <div className="text-sm text-neutral-400">无匹配文档</div>}
          </div>
        </div>

        <div className="max-h-[78vh] overflow-auto rounded-lg border border-neutral-200 bg-white p-6">
          {sel ? (
            loading ? (
              <div className="text-sm text-neutral-400">加载中…</div>
            ) : (
              <article className={MD}>
                <ReactMarkdown>{content}</ReactMarkdown>
              </article>
            )
          ) : (
            <div className="text-sm text-neutral-400">从左侧选一个文档查看，或在上方搜索。</div>
          )}
        </div>
      </div>
    </div>
  );
}

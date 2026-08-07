"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { API_BASE_URL } from "@/lib/api";

type Envelope<T> = { code: number; data: T; message?: string };
type ProjectSpace = { id: string; name: string; slug: string };
type App = { id: string; name: string; repo_dir: string };
type Requirement = {
  id: string;
  title: string;
  description: string;
  user_story: string;
  acceptance_criteria: string;
  status: string;
  application_id?: string;
  assignee?: string;
};

const STEPS = ["需求", "编码", "审批", "发布"];

export default function RequirementsPage() {
  const [spaces, setSpaces] = useState<ProjectSpace[]>([]);
  const [psID, setPsID] = useState("");
  const [apps, setApps] = useState<App[]>([]);
  const [selApp, setSelApp] = useState(""); // 创建需求时指定的归属应用
  const [desc, setDesc] = useState("");
  const [images, setImages] = useState<string[]>([]);
  const [textFiles, setTextFiles] = useState<{ name: string; content: string }[]>([]);
  const [binFiles, setBinFiles] = useState<{ name: string }[]>([]);
  const [last, setLast] = useState<Requirement | null>(null);
  const [list, setList] = useState<Requirement[]>([]);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState("");
  const [msg, setMsg] = useState("");
  const [dispatching, setDispatching] = useState("");
  const dispatchingRef = useRef(false); // 同步锁：堵住 dispatching state 堵不住的同 tick 连点竞态
  type Member = { user_id: string; name: string; role: string };
  const [members, setMembers] = useState<Member[]>([]);
  const [selAssignee, setSelAssignee] = useState(""); // 派发给谁（name 口径）
  const [me, setMe] = useState(""); // 当前登录用户名（name）

  useEffect(() => {
    fetch(`${API_BASE_URL}/project-spaces`)
      .then((r) => r.json())
      .then((r: Envelope<ProjectSpace[]>) => {
        setSpaces(r.data ?? []);
        const def = (r.data ?? []).find((s) => s.id === "ps_default") ?? (r.data ?? [])[0];
        if (def) setPsID(def.id);
      });
    fetch(`${API_BASE_URL}/auth/me`)
      .then((r) => r.json())
      .then((r: Envelope<{ user: string }>) => setMe(r.data?.user ?? ""))
      .catch(() => {});
  }, []);

  const loadList = (id: string) => {
    if (!id) return;
    fetch(`${API_BASE_URL}/project-spaces/${id}/requirements`)
      .then((r) => r.json())
      .then((r: Envelope<Requirement[]>) => setList(r.data ?? []))
      .catch(() => {});
    fetch(`${API_BASE_URL}/project-spaces/${id}/apps`)
      .then((r) => r.json())
      .then((r: Envelope<App[]>) => setApps(r.data ?? []))
      .catch(() => {});
    fetch(`${API_BASE_URL}/project-spaces/${id}/members`)
      .then((r) => r.json())
      .then((r: Envelope<Member[]>) => setMembers(r.data ?? []))
      .catch(() => {});
  };
  useEffect(() => {
    loadList(psID);
  }, [psID]);

  // 文本类附件（md/txt/markdown）读内容拼入业务描述，参与规格生成；图片走 dataURL 多模态；
  // 二进制（pdf/doc/docx 等）仅留档展示（不参与生成，避免被当图片喂视觉模型报错）。
  const TEXT_EXTS = [".md", ".markdown", ".txt"];
  const IMG_EXTS = [".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp"];
  function fileKind(name: string): "text" | "image" | "file" {
    const low = name.toLowerCase();
    if (TEXT_EXTS.some((e) => low.endsWith(e))) return "text";
    if (IMG_EXTS.some((e) => low.endsWith(e))) return "image";
    return "file";
  }
  function onFiles(files: FileList | null) {
    if (!files) return;
    Array.from(files)
      .slice(0, 8)
      .forEach((f) => {
        const k = fileKind(f.name);
        if (k === "text") {
          const reader = new FileReader();
          reader.onload = () =>
            setTextFiles((p) => [
              ...p,
              { name: f.name, content: String(reader.result).slice(0, 20000) },
            ]);
          reader.readAsText(f);
        } else if (k === "image") {
          const reader = new FileReader();
          reader.onload = () => setImages((p) => [...p, reader.result as string]);
          reader.readAsDataURL(f);
        } else {
          setBinFiles((p) => [...p, { name: f.name }]);
        }
      });
  }

  async function generate() {
    if (!desc.trim() || !psID || loading) return;
    setLoading(true);
    setErr("");
    try {
      // 文本类附件内容拼入业务描述，随规格生成一并交给 AI（实现"附件参与规格生成"）。
      const textPart = textFiles.map((t) => `\n\n【附件 ${t.name}】\n${t.content}`).join("");
      const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/requirements`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          description: desc + textPart,
          images,
          application_id: selApp || undefined,
        }),
      });
      const r = await res.json();
      if (r.data) {
        setLast(r.data);
        setDesc("");
        setImages([]);
        setTextFiles([]);
        setBinFiles([]);
        loadList(psID);
        setMsg(
          selApp
            ? `✅ 需求已生成并归属应用「${apps.find((a) => a.id === selApp)?.name}」`
            : "✅ 需求已生成"
        );
      } else setErr(r.message ?? "生成失败");
    } catch (e) {
      setErr(String(e));
    } finally {
      setLoading(false);
    }
  }

  async function dispatch(rid: string) {
    if (!psID || !selAssignee || dispatching || dispatchingRef.current) return;
    dispatchingRef.current = true;
    setDispatching(rid);
    setMsg("");
    try {
      const res = await fetch(
        `${API_BASE_URL}/project-spaces/${psID}/requirements/${rid}/dispatch-code`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ assignee: selAssignee }),
        }
      );
      const r = await res.json();
      if (r.data?.workspace_url) {
        if (selAssignee === me) {
          // 派给自己：直达该需求工作台
          window.location.href = r.data.workspace_url;
          return;
        }
        setMsg(`✅ 已派发给「${selAssignee}」。其可在「研发工作台」从「派给我的」进入编码工作台`);
      } else {
        setMsg(`✗ ${r.message ?? "派发失败"}`);
      }
    } catch (e) {
      setMsg(`✗ ${e}`);
    } finally {
      setDispatching("");
      dispatchingRef.current = false;
    }
  }

  // 认领需求（人）：POST /assign 空 body=自助认领（互斥，被他人认领会 409）。
  // 认领后去「编码工作台」为此需求开发（工作台会绑定 requirement_id）。
  async function claim(rid: string) {
    if (!psID) return;
    try {
      const r = await fetch(`${API_BASE_URL}/project-spaces/${psID}/requirements/${rid}/assign`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({}),
      }).then((rr) => rr.json());
      if (r.code !== 0) {
        setMsg(`✗ ${r.message ?? "认领失败"}`);
        return;
      }
      setMsg("✅ 已认领，去「编码工作台」为此需求编码");
      loadList(psID);
    } catch (e) {
      setMsg(`✗ ${e}`);
    }
  }

  let ac: string[] = [];
  try {
    ac = last ? JSON.parse(last.acceptance_criteria) : [];
  } catch {
    ac = [];
  }

  return (
    <div>
      <h1 className="mb-1 text-xl font-bold">需求工作台</h1>
      <p className="mb-3 text-sm text-text-muted">
        业务描述 + 截图（可选）→ AI 生成规格 → 派发编码 → 审批 → 发布
      </p>

      <div className="mb-4">
        <Link
          href="/requirements/chat"
          className="rounded-md bg-accent px-3 py-1.5 text-sm text-white"
        >
          💬 对话式梳理需求（AI 引导，推荐）
        </Link>
      </div>

      {/* 流程引导 */}
      <div className="mb-4 flex items-center gap-1 text-xs">
        {STEPS.map((s, i) => (
          <div key={s} className="flex items-center gap-1">
            <span
              className={`rounded-full px-2 py-1 ${i === 0 ? "bg-accent text-white" : "bg-surface-2 text-text-muted"}`}
            >
              {i + 1}. {s}
            </span>
            {i < STEPS.length - 1 && <span className="text-text-muted">→</span>}
          </div>
        ))}
        <span className="ml-2 text-text-muted">您在此</span>
      </div>

      <div className="mb-3 flex flex-wrap items-center gap-3">
        <div>
          <label className="text-xs text-text-muted">项目空间</label>
          <select
            value={psID}
            onChange={(e) => setPsID(e.target.value)}
            className="ml-2 rounded-md border border-border px-2 py-1 text-sm"
          >
            {spaces.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name} ({s.slug})
              </option>
            ))}
          </select>
        </div>
        <div>
          <label className="text-xs text-text-muted">归属应用（需求即为其开发）</label>
          <select
            value={selApp}
            onChange={(e) => setSelApp(e.target.value)}
            className="ml-2 rounded-md border border-border px-2 py-1 text-sm"
          >
            <option value="">— 不指定（手动填仓库） —</option>
            {apps.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name}
              </option>
            ))}
          </select>
          {apps.length === 0 && (
            <span className="ml-2 text-xs text-text-muted">先去「应用部署」创建应用</span>
          )}
        </div>
        <div>
          <label className="text-xs text-text-muted">派发给（开发人员）</label>
          <select
            value={selAssignee}
            onChange={(e) => setSelAssignee(e.target.value)}
            className="ml-2 rounded-md border border-border px-2 py-1 text-sm"
          >
            <option value="">— 选择开发人员 —</option>
            {members.map((m) => (
              <option key={m.user_id} value={m.name}>
                {m.name}（{m.role}）
              </option>
            ))}
          </select>
          {members.length === 0 && (
            <span className="ml-2 text-xs text-text-muted">该项目空间暂无成员</span>
          )}
        </div>
      </div>

      <label className="text-xs text-text-muted">业务描述</label>
      <textarea
        value={desc}
        onChange={(e) => setDesc(e.target.value)}
        rows={3}
        placeholder="例：客服系统登录界面，支持账号密码和短信验证码登录"
        className="mt-1 w-full rounded-md border border-border px-2 py-1.5 text-sm"
      />

      <div className="mt-2">
        <label className="text-xs text-text-muted">
          附件（可选，支持 md/txt/doc/docx/pdf/图片等，最多 8 个；文本类参与规格生成）
        </label>
        <input
          type="file"
          accept=".md,.markdown,.txt,.doc,.docx,.pdf,.png,.jpg,.jpeg,.gif,.webp,.bmp,image/*"
          multiple
          onChange={(e) => onFiles(e.target.files)}
          className="mt-1 block text-sm"
        />
        {(images.length > 0 || textFiles.length > 0 || binFiles.length > 0) && (
          <div className="mt-2 flex flex-wrap gap-2">
            {images.map((img, i) => (
              <img key={`img-${i}`} src={img} alt="" className="h-16 rounded border" />
            ))}
            {textFiles.map((t, i) => (
              <span key={`txt-${i}`} className="rounded bg-accent/10 px-2 py-1 text-xs text-accent">
                📄 {t.name}（{t.content.length} 字，参与生成）
              </span>
            ))}
            {binFiles.map((t, i) => (
              <span
                key={`bin-${i}`}
                className="rounded bg-surface-2 px-2 py-1 text-xs text-text-muted"
                title="二进制附件仅留档，不参与规格生成"
              >
                📎 {t.name}（留档）
              </span>
            ))}
          </div>
        )}
      </div>

      <button
        onClick={generate}
        disabled={loading || !psID}
        className="mt-2 rounded-md bg-accent px-4 py-2 text-sm text-white disabled:opacity-50"
      >
        {loading ? "AI 生成规格中…" : "① 生成需求规格"}
      </button>
      {err && <div className="mt-2 text-sm text-danger">{err}</div>}
      {msg && <div className="mt-2 rounded-md bg-accent/10 p-2 text-sm text-accent">{msg}</div>}

      {last && (
        <div className="mt-4 rounded-lg border border-accent bg-accent/10 p-4">
          <div className="text-xs text-text-muted">最新生成 · {last.id}</div>
          <div className="text-base font-semibold">{last.title}</div>
          <div className="mt-2 text-sm">
            <b>用户故事：</b>
            {last.user_story}
          </div>
          <div className="mt-2 text-sm">
            <b>验收标准：</b>
            <ul className="ml-5 list-disc">
              {ac.map((c, i) => (
                <li key={i}>{c}</li>
              ))}
            </ul>
          </div>
          <div className="mt-3 border-t border-accent pt-3">
            <div className="mb-1 text-xs text-text-muted">下一步：派发给 AI 编码</div>
            {last.application_id ? (
              <div className="mb-2 text-xs text-success">
                📦 将编码到所属应用仓库「
                {apps.find((a) => a.id === last.application_id)?.name ?? last.application_id}
                」（自动）
              </div>
            ) : (
              <div className="mb-2 text-xs text-accent">
                📦 未归属应用：派发时自动创建一个托管应用（代码归属即确立，可在「应用部署」查看 /
                构建 / 版本回滚）
              </div>
            )}
            <button
              onClick={() => dispatch(last.id)}
              disabled={!!dispatching || !selAssignee}
              className="rounded-md bg-success px-3 py-1.5 text-sm text-white disabled:opacity-50"
              title={selAssignee ? `派发给 ${selAssignee}` : "先选开发人员"}
            >
              {dispatching === last.id ? "派发中…" : "👤 派发给开发"}
            </button>
          </div>
        </div>
      )}

      <div className="mt-6">
        <div className="mb-2 text-sm font-semibold">需求列表（{list.length}）— 每项可派发编码</div>
        <div className="space-y-2">
          {list.map((r) => (
            <div key={r.id} className="rounded-md border border-border bg-surface p-3 text-sm">
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                  <span className="font-medium">{r.title}</span>
                  <span
                    className={`rounded px-1.5 py-0.5 text-xs ${r.status === "delivered" ? "bg-success/10 text-success" : r.status === "specified" ? "bg-accent/10 text-accent" : "bg-surface-2 text-text-muted"}`}
                  >
                    {r.status === "delivered"
                      ? "✅ 已交付"
                      : r.status === "specified"
                        ? "已生成"
                        : r.status}
                  </span>
                  {r.application_id && (
                    <span className="rounded bg-warn/10 px-1.5 py-0.5 text-xs text-warn">
                      📦 {apps.find((a) => a.id === r.application_id)?.name ?? "应用"}
                    </span>
                  )}
                  {r.assignee && (
                    <span className="rounded bg-warn/10 px-1.5 py-0.5 text-xs text-warn">
                      👤 {r.assignee}
                    </span>
                  )}
                </div>
                <div className="flex gap-1">
                  <button
                    onClick={() => claim(r.id)}
                    className="rounded bg-accent px-2 py-1 text-xs text-white"
                    title="认领此需求（人），去编码工作台开发"
                  >
                    👤 认领
                  </button>
                  <button
                    onClick={() => dispatch(r.id)}
                    disabled={!!dispatching || !selAssignee}
                    className="rounded bg-success px-2 py-1 text-xs text-white disabled:opacity-50"
                    title={selAssignee ? `派发给 ${selAssignee}` : "先选开发人员"}
                  >
                    {dispatching === r.id ? "派发中…" : "👤 派发给开发"}
                  </button>
                  {r.assignee === me && r.status === "developing" && r.application_id && (
                    <a
                      href={`/workspace?app=${r.application_id}&ps=${psID}&req=${r.id}`}
                      className="rounded bg-accent px-2 py-1 text-xs text-white"
                      title="进入该需求的编码工作台，协同 AI 开发"
                    >
                      💻 进编码工作台
                    </a>
                  )}
                </div>
              </div>
              <div className="mt-1 text-xs text-text-muted">{r.user_story}</div>
            </div>
          ))}
          {list.length === 0 && <div className="text-sm text-text-muted">暂无需求</div>}
        </div>
      </div>
    </div>
  );
}

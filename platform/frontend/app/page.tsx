"use client";

import { useEffect, useState } from "react";
import { apiGet, currentProjectSpace } from "@/lib/api";

type Envelope<T> = { code: number; data: T; message?: string };
type PS = { id: string; name: string; slug: string };
type Overview = {
  space: PS;
  members: number;
  apps: number;
  deployed_apps: number;
  requirements: number;
  changes: number;
  releases: number;
};
type Req = {
  id: string;
  title: string;
  status: string;
  stage?: string; // 后端推导的真实阶段: coding/approving/releasing/stale
  priority?: string;
  application_id?: string;
  assignee?: string;
  created_at?: string;
};
type Chg = {
  id: string;
  kind: string;
  status: string;
  source_id: string;
  output?: string;
  created_at?: string;
  reviewer?: string;
  app_name?: string;
};
type MyTasks = {
  roles: string[];
  toClaim: Req[];
  myDev: Req[];
  toApprove: Chg[];
  toRelease: Chg[];
};

const FLOW = [
  { key: "需求", icon: "💬", path: "/requirements" },
  { key: "认领", icon: "👤", path: "/requirements" },
  { key: "编码", icon: "🧑‍💻", path: "/applications" },
  { key: "测试", icon: "🧪", path: "/applications" },
  { key: "核对", icon: "🔒", path: "/applications" },
  { key: "登记", icon: "📝", path: "/applications" },
  { key: "审批", icon: "✅", path: "/approvals" },
  { key: "上线", icon: "🚀", path: "/applications" },
];

export default function Home() {
  const [spaces, setSpaces] = useState<PS[]>([]);
  const [psID, setPsID] = useState("");
  const [ov, setOv] = useState<Overview | null>(null);
  const [tasks, setTasks] = useState<MyTasks>({
    roles: [],
    toClaim: [],
    myDev: [],
    toApprove: [],
    toRelease: [],
  });
  const [appNames, setAppNames] = useState<Record<string, string>>({});
  const [loadErr, setLoadErr] = useState("");

  useEffect(() => {
    apiGet<Envelope<PS[]>>(`/project-spaces`)
      .then((r) => {
        setSpaces(r.data ?? []);
        const cur = currentProjectSpace();
        const def =
          (r.data ?? []).find((s) => s.id === cur) ??
          (r.data ?? []).find((s) => s.id === "ps_default") ??
          (r.data ?? [])[0];
        if (def) setPsID(def.id);
      })
      .catch((e) => setLoadErr(`加载项目空间失败：${e.message || e}`));
  }, []);

  useEffect(() => {
    if (!psID) return;
    setLoadErr("");
    apiGet<Envelope<Overview>>(`/project-spaces/${psID}/overview`)
      .then((r) => setOv(r.data ?? null))
      .catch((e) => setLoadErr(`加载概览失败：${e.message || e}`));
    apiGet<Envelope<MyTasks>>(`/project-spaces/${psID}/my-tasks`)
      .then((r) => {
        if (r.code !== 0) {
          setLoadErr(`加载我的任务失败：${r.message || "未知错误"}`);
          return;
        }
        setTasks(r.data ?? { roles: [], toClaim: [], myDev: [], toApprove: [], toRelease: [] });
      })
      .catch((e) => setLoadErr(`加载我的任务失败：${e.message || e}`));
    apiGet<Envelope<{ id: string; name: string }[]>>(`/project-spaces/${psID}/apps`)
      .then((r) => {
        const m: Record<string, string> = {};
        (r.data ?? []).forEach((a) => {
          m[a.id] = a.name;
        });
        setAppNames(m);
      })
      .catch((e) => setLoadErr(`加载应用列表失败：${e.message || e}`));
  }, [psID]);

  const { roles, toClaim, myDev, toApprove, toRelease } = tasks;
  const isAdmin = roles.includes("admin") || roles.length === 0;
  const nodeVisible = (i: number) => {
    if (isAdmin) return true;
    if (roles.includes("business") && i === 0) return true;
    if (roles.includes("dev") && i >= 1 && i <= 5) return true;
    if (roles.includes("gatekeeper") && i >= 6) return true;
    return false;
  };
  // myDev 按后端推导的 stage 分桶(真实进度,来自最新变更审批状态——
  // 修复:原来四张卡复制同一份 myDev 假装是编码/测试/核对/登记四阶段,统计失真)
  const byStage = (s: string) => myDev.filter((q) => (q.stage || "coding") === s);
  const coding = byStage("coding");
  const approving = byStage("approving");
  const releasing = byStage("releasing");
  const stale = byStage("stale");
  const allBadges = [
    toClaim.length,
    toClaim.length,
    coding.length,
    approving.length + toApprove.length,
    releasing.length + toRelease.length,
    stale.length,
    toApprove.length,
    toRelease.length,
  ];
  const showClaim = isAdmin || roles.includes("business") || roles.includes("dev");
  const showDev = isAdmin || roles.includes("dev");
  const showApprove = isAdmin || roles.includes("gatekeeper");
  const appName = (id: string) => appNames[id] || "?";
  const fmtDate = (d?: string) =>
    d
      ? new Date(d).toLocaleString("zh-CN", {
          hour12: false,
          month: "2-digit",
          day: "2-digit",
          hour: "2-digit",
          minute: "2-digit",
        })
      : "?";
  const chgLabel = (c: Chg) =>
    (
      (c.output || "").match(/【总结】(.+)/)?.[1] ||
      c.app_name ||
      `变更 ${c.id.slice(0, 12)}`
    ).slice(0, 50);
  // 去「编码工作台」：带上 req + rtitle，使首页"去编码"也绑需求规格（与需求页
  // 「认领并编码」一致），消除断点。req 由后端按 force_new 决定是否注入。
  const ws = (q: Req) =>
    `/workspace?app=${q.application_id || ""}&ps=${psID}&req=${q.id}&rtitle=${encodeURIComponent(q.title)}`;

  return (
    <div>
      <h1 className="mb-1 text-2xl font-bold">智源 ANP 平台</h1>
      <p className="mb-4 text-text-muted">企业 AI 原生研发平台 · 开发流程向导 + 我的任务</p>

      <div className="mb-4 flex items-center gap-2">
        <label className="text-xs text-text-muted">项目空间</label>
        <select
          value={psID}
          onChange={(e) => setPsID(e.target.value)}
          className="rounded-md border border-border px-2 py-1 text-sm"
        >
          {spaces.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name} ({s.slug})
            </option>
          ))}
        </select>
        {roles.length > 0 && (
          <span className="text-xs text-text-muted">角色:{roles.join(",")}</span>
        )}
      </div>

      {loadErr && (
        <div className="mb-4 rounded-md border border-danger bg-danger/10 p-3 text-sm text-danger">
          ⚠️ {loadErr}（请检查登录状态或稍后重试）
        </div>
      )}

      {/* 流程向导(全8步可见,非角色灰显) */}
      <div className="mb-6 rounded-lg border border-border bg-surface p-4">
        <div className="mb-2 text-sm font-medium text-text-muted">
          开发流程向导(高亮=有我的待办,点击进入)
        </div>
        <div className="flex items-center gap-0.5 overflow-x-auto pb-1">
          {FLOW.map((n, i) => {
            const cnt = allBadges[i];
            const relevant = nodeVisible(i);
            const active = cnt > 0 && relevant;
            return (
              <a
                key={n.key}
                href={n.path}
                className={`flex shrink-0 items-center gap-1 rounded px-2 py-1 text-xs ${active ? "bg-accent/10 text-accent" : relevant ? "text-text-muted hover:bg-surface-2" : "text-text-muted hover:bg-bg"}`}
                title={relevant ? n.key : `${n.key}（非你的角色）`}
              >
                <span>{n.icon}</span>
                <span>{n.key}</span>
                {active && (
                  <span className="rounded-full bg-accent px-1.5 text-[10px] text-white">
                    {cnt}
                  </span>
                )}
                {i < FLOW.length - 1 && <span className="ml-1 text-text-muted">→</span>}
              </a>
            );
          })}
        </div>
      </div>

      {/* 我的任务(8步对应卡片;开发阶段按变更状态推导,不再是同一份 myDev 复制四份) */}
      <div className="mb-6 grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
        {showClaim && (
          <TaskGroup
            title="💬 待认领"
            items={toClaim.map((q) => ({
              id: q.id,
              label: q.title,
              sub: `应用: ${appName(q.application_id || "")} · 创建: ${fmtDate(q.created_at)}`,
              tag: q.priority,
              action: "认领",
              path: "/requirements",
            }))}
          />
        )}
        {showDev && (
          <TaskGroup
            title="🧑‍💻 编码中"
            items={coding.map((q) => ({
              id: q.id,
              label: q.title,
              sub: `应用: ${appName(q.application_id || "")} · 认领: ${q.assignee || "?"}`,
              tag: "编码",
              action: "去编码",
              path: ws(q),
            }))}
          />
        )}
        {showDev && (
          <TaskGroup
            title="📝 待登记变更"
            items={approving.map((q) => ({
              id: q.id,
              label: q.title,
              sub: `应用: ${appName(q.application_id || "")} · 认领: ${q.assignee || "?"}`,
              tag: "待审批",
              action: "去查看",
              path: "/approvals",
            }))}
          />
        )}
        {showDev && (
          <TaskGroup
            title="🚀 待上线"
            items={releasing.map((q) => ({
              id: q.id,
              label: q.title,
              sub: `应用: ${appName(q.application_id || "")} · 认领: ${q.assignee || "?"}`,
              tag: "已批待上线",
              action: "去上线",
              path: "/applications",
            }))}
          />
        )}
        {showDev && stale.length > 0 && (
          <TaskGroup
            title="⚠️ 已上线未回写"
            items={stale.map((q) => ({
              id: q.id,
              label: q.title,
              sub: `应用: ${appName(q.application_id || "")} · 变更已上线但需求仍 developing,请回写交付`,
              tag: "异常",
              action: "去处理",
              path: "/requirements",
            }))}
          />
        )}
        {showApprove && (
          <TaskGroup
            title="✅ 待审批"
            items={toApprove.map((c) => ({
              id: c.id,
              label: chgLabel(c),
              sub: `应用: ${c.app_name || appName(c.source_id || "")} · 提交: ${c.reviewer || "?"} · ${fmtDate(c.created_at)}`,
              tag: c.status,
              action: "审批",
              path: "/approvals",
            }))}
          />
        )}
        {showApprove && (
          <TaskGroup
            title="🚀 待上线"
            items={toRelease.map((c) => ({
              id: c.id,
              label: chgLabel(c),
              sub: `应用: ${c.app_name || appName(c.source_id || "")} · 提交: ${c.reviewer || "?"} · ${fmtDate(c.created_at)}`,
              tag: c.status,
              action: "上线",
              path: `/applications`,
            }))}
          />
        )}
      </div>

      {/* 统计 */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
        <Stat label="成员" value={ov?.members} />
        <Stat label="应用" value={ov?.apps} hint={ov ? `运行中 ${ov.deployed_apps}` : undefined} />
        <Stat label="需求" value={ov?.requirements} />
        <Stat label="变更" value={ov?.changes} />
        <Stat label="发布" value={ov?.releases} />
        <Stat label="项目空间" value={spaces.length} />
      </div>
    </div>
  );
}

function TaskGroup({
  title,
  items,
}: {
  title: string;
  items: { id: string; label: string; sub?: string; tag?: string; action: string; path: string }[];
}) {
  return (
    <div className="rounded-lg border border-border bg-surface p-3">
      <div className="mb-2 text-sm font-medium text-text-muted">
        {title}({items.length})
      </div>
      {items.length === 0 ? (
        <div className="text-xs text-text-muted">暂无</div>
      ) : (
        <div className="space-y-1.5">
          {items.map((it) => (
            <div key={it.id} className="text-xs">
              <div className="flex items-center gap-2">
                {it.tag && (
                  <span className="shrink-0 rounded bg-surface-2 px-1 text-text-muted">
                    {it.tag}
                  </span>
                )}
                <span className="flex-1 truncate">{it.label}</span>
                <a href={it.path} className="shrink-0 rounded bg-accent/10 px-2 py-0.5 text-accent">
                  {it.action}
                </a>
              </div>
              {it.sub && <div className="mt-0.5 text-[10px] text-text-muted">{it.sub}</div>}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function Stat({ label, value, hint }: { label: string; value?: number; hint?: string }) {
  return (
    <div className="rounded-lg border border-border bg-surface p-3">
      <div className="text-xs text-text-muted">{label}</div>
      <div className="text-2xl font-bold">{value ?? "—"}</div>
      {hint && <div className="text-[11px] text-text-muted">{hint}</div>}
    </div>
  );
}

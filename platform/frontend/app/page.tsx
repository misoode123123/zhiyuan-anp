"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL, currentProjectSpace } from "@/lib/api";

type Envelope<T> = { code: number; data: T };
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

  useEffect(() => {
    fetch(`${API_BASE_URL}/project-spaces`)
      .then((r) => r.json())
      .then((r: Envelope<PS[]>) => {
        setSpaces(r.data ?? []);
        const cur = currentProjectSpace();
        const def =
          (r.data ?? []).find((s) => s.id === cur) ??
          (r.data ?? []).find((s) => s.id === "ps_default") ??
          (r.data ?? [])[0];
        if (def) setPsID(def.id);
      });
  }, []);

  useEffect(() => {
    if (!psID) return;
    fetch(`${API_BASE_URL}/project-spaces/${psID}/overview`)
      .then((r) => r.json())
      .then((r: Envelope<Overview>) => setOv(r.data ?? null));
    fetch(`${API_BASE_URL}/project-spaces/${psID}/my-tasks`)
      .then((r) => r.json())
      .then((r: Envelope<MyTasks>) =>
        setTasks(r.data ?? { roles: [], toClaim: [], myDev: [], toApprove: [], toRelease: [] })
      );
    fetch(`${API_BASE_URL}/project-spaces/${psID}/apps`)
      .then((r) => r.json())
      .then((r: Envelope<{ id: string; name: string }[]>) => {
        const m: Record<string, string> = {};
        (r.data ?? []).forEach((a) => {
          m[a.id] = a.name;
        });
        setAppNames(m);
      });
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
  const allBadges = [
    toClaim.length,
    toClaim.length,
    myDev.length,
    myDev.length,
    myDev.length,
    myDev.length,
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
  const ws = (q: Req) => `/workspace?app=${q.application_id || ""}&ps=${psID}`;

  return (
    <div>
      <h1 className="mb-1 text-2xl font-bold">智源 ANP 平台</h1>
      <p className="mb-4 text-neutral-600">企业 AI 原生研发平台 · 开发流程向导 + 我的任务</p>

      <div className="mb-4 flex items-center gap-2">
        <label className="text-xs text-neutral-500">项目空间</label>
        <select
          value={psID}
          onChange={(e) => setPsID(e.target.value)}
          className="rounded-md border border-neutral-300 px-2 py-1 text-sm"
        >
          {spaces.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name} ({s.slug})
            </option>
          ))}
        </select>
        {roles.length > 0 && (
          <span className="text-xs text-neutral-400">角色:{roles.join(",")}</span>
        )}
      </div>

      {/* 流程向导(全8步可见,非角色灰显) */}
      <div className="mb-6 rounded-lg border border-neutral-200 bg-white p-4">
        <div className="mb-2 text-sm font-medium text-neutral-600">
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
                className={`flex shrink-0 items-center gap-1 rounded px-2 py-1 text-xs ${active ? "bg-blue-50 text-blue-700" : relevant ? "text-neutral-600 hover:bg-neutral-100" : "text-neutral-300 hover:bg-neutral-50"}`}
                title={relevant ? n.key : `${n.key}（非你的角色）`}
              >
                <span>{n.icon}</span>
                <span>{n.key}</span>
                {active && (
                  <span className="rounded-full bg-blue-600 px-1.5 text-[10px] text-white">
                    {cnt}
                  </span>
                )}
                {i < FLOW.length - 1 && <span className="ml-1 text-neutral-300">→</span>}
              </a>
            );
          })}
        </div>
      </div>

      {/* 我的任务(8步对应卡片) */}
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
            items={myDev.map((q) => ({
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
            title="🧪 待测试"
            items={myDev.map((q) => ({
              id: `t-${q.id}`,
              label: q.title,
              sub: `应用: ${appName(q.application_id || "")}`,
              tag: "测试",
              action: "去测试",
              path: ws(q),
            }))}
          />
        )}
        {showDev && (
          <TaskGroup
            title="🔒 待核对"
            items={myDev.map((q) => ({
              id: `c-${q.id}`,
              label: q.title,
              sub: `应用: ${appName(q.application_id || "")}`,
              tag: "核对",
              action: "去核对",
              path: ws(q),
            }))}
          />
        )}
        {showDev && (
          <TaskGroup
            title="📝 待登记"
            items={myDev.map((q) => ({
              id: `r-${q.id}`,
              label: q.title,
              sub: `应用: ${appName(q.application_id || "")}`,
              tag: "登记",
              action: "去登记",
              path: ws(q),
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
    <div className="rounded-lg border border-neutral-200 bg-white p-3">
      <div className="mb-2 text-sm font-medium text-neutral-600">
        {title}({items.length})
      </div>
      {items.length === 0 ? (
        <div className="text-xs text-neutral-400">暂无</div>
      ) : (
        <div className="space-y-1.5">
          {items.map((it) => (
            <div key={it.id} className="text-xs">
              <div className="flex items-center gap-2">
                {it.tag && (
                  <span className="shrink-0 rounded bg-neutral-100 px-1 text-neutral-500">
                    {it.tag}
                  </span>
                )}
                <span className="flex-1 truncate">{it.label}</span>
                <a
                  href={it.path}
                  className="shrink-0 rounded bg-blue-100 px-2 py-0.5 text-blue-700"
                >
                  {it.action}
                </a>
              </div>
              {it.sub && <div className="mt-0.5 text-[10px] text-neutral-400">{it.sub}</div>}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function Stat({ label, value, hint }: { label: string; value?: number; hint?: string }) {
  return (
    <div className="rounded-lg border border-neutral-200 bg-white p-3">
      <div className="text-xs text-neutral-500">{label}</div>
      <div className="text-2xl font-bold">{value ?? "—"}</div>
      {hint && <div className="text-[11px] text-neutral-400">{hint}</div>}
    </div>
  );
}

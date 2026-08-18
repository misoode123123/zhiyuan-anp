"use client";

// 应用页自包含区块组件（自 page.tsx 原样平移，行为零变化）：
// DevWizard 开发向导 / ArtifactSection 构建产物 / DepsSection 中间件依赖 / NetworkModeSection 网络模式。
import { useEffect, useState } from "react";
import { API_BASE_URL } from "@/lib/api";
import type { Artifact } from "@/lib/api-types-manual";
import { devStep } from "@/lib/devstep";
import { toast } from "@/lib/toast";
import { healthBadge } from "../_lib/predicates";
import type { App, Dep, DepsCatalog, Envelope } from "../_lib/types";

// DevWizard 开发向导：编码→测试→上线 进度条 + 项目上下文 + 引导文案。
// 让开发者一眼看到当前在哪步、下一步做什么（解决"流程不明确"）。
export function DevWizard({ app }: { app: App }) {
  const s = devStep({ image: app.image, instances: app.instances });
  const testIns = app.instances?.find((i) => i.env === "test");
  const prodIns = app.instances?.find((i) => i.env === "prod");
  // 健康徽标只算一次（避免每个 span 调两次 healthBadge）。
  const testHb = testIns ? healthBadge(testIns.status) : null;
  const prodHb = prodIns ? healthBadge(prodIns.status) : null;
  const step = (key: "code" | "test" | "prod", label: string) => {
    const st = s[key];
    const isCur = s.current === key;
    const cls =
      st === "done" ? "text-success" : isCur ? "font-semibold text-accent" : "text-text-muted";
    const mark = st === "done" ? "✅" : isCur ? "●" : "○";
    return (
      <span className={cls}>
        {label} {mark}
      </span>
    );
  };
  return (
    <div className="mb-2 rounded-md bg-accent/60 p-2 text-xs">
      <div className="flex items-center gap-2">
        {step("code", "✏ 编码")}
        <span className="text-text-muted">→</span>
        {step("test", "🧪 测试")}
        <span className="text-text-muted">→</span>
        {step("prod", "🚀 上线")}
      </div>
      <div className="mt-1 flex flex-wrap gap-x-3 text-text-muted">
        <span>
          仓库 <code>{app.repo_dir}</code>
        </span>
        {testIns && testHb && (
          <span>
            test{" "}
            {app.app_kind === "headless" ? (
              <span className={testHb.cls}>{testHb.text}</span>
            ) : (
              <span className={testIns.status === "running" ? "text-success" : ""}>
                :{testIns.host_port} {testIns.status}
              </span>
            )}
          </span>
        )}
        {prodIns && prodHb && (
          <span>
            prod{" "}
            {app.app_kind === "headless" ? (
              <span className={prodHb.cls}>{prodHb.text}</span>
            ) : (
              <span className={prodIns.status === "running" ? "text-success" : ""}>
                :{prodIns.host_port} {prodIns.status}
              </span>
            )}
          </span>
        )}
      </div>
      <div className="mt-1 text-accent">👉 {s.hint}</div>
    </div>
  );
}

// 构建产物区：仅非 web/service 应用显示。
// 调 /artifacts 列产物、/build-artifacts 触发构建、/download 浏览器直接下载（跟随 302/流式）。
export function ArtifactSection({
  psID,
  appID,
  appKind,
}: {
  psID: string;
  appID: string;
  appKind: string;
}) {
  const [arts, setArts] = useState<Artifact[]>([]);
  const [building, setBuilding] = useState(false);
  useEffect(() => {
    // headless 是运行态应用（无产物），不拉产物列表。
    if (appKind === "web" || appKind === "service" || appKind === "headless") return;
    fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/artifacts`)
      .then((r) => r.json())
      .then((r: { data?: { artifacts?: Artifact[] } }) => setArts(r.data?.artifacts ?? []));
  }, [psID, appID, appKind]);
  // headless 是运行态应用（无产物），不显产物区。
  if (appKind === "web" || appKind === "service" || appKind === "headless") return null;
  const build = async () => {
    setBuilding(true);
    try {
      await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/build-artifacts`, {
        method: "POST",
      });
    } finally {
      setBuilding(false);
    }
    // 刷新列表
    const r = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/artifacts`).then(
      (r) => r.json()
    );
    setArts((r as { data?: { artifacts?: Artifact[] } }).data?.artifacts ?? []);
  };
  return (
    <div className="mt-3 rounded border border-border p-3">
      <div className="mb-2 flex items-center justify-between">
        <span className="text-sm font-semibold">构建产物</span>
        <button
          onClick={build}
          disabled={building}
          className="rounded bg-accent px-2 py-0.5 text-xs text-white disabled:cursor-not-allowed disabled:opacity-40"
        >
          {building ? "构建中…" : "构建产物"}
        </button>
      </div>
      {arts.length === 0 ? (
        <div className="text-xs text-text-muted">暂无产物，点「构建产物」生成</div>
      ) : (
        arts.map((a) => (
          <div key={a.id} className="flex items-center justify-between py-1 text-xs">
            <span>
              📦 {a.filename} · {a.platform}/{a.arch} · {(a.size_bytes / 1048576).toFixed(1)}MB ·
              sha: {a.sha256.slice(0, 8)}
            </span>
            <a
              href={`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/artifacts/${a.id}/download`}
              className="text-accent hover:underline"
            >
              下载
            </a>
          </div>
        ))
      )}
    </div>
  );
}

// 依赖声明区：列当前 deps（kind/strategy/status/token），编辑态增删改（catalog 驱动下拉），PUT 整体替换保存。
// 声明与部署解耦：保存只落库，下次部署/重新部署时由 mwsupply 供给 env（REDIS_ADDR/MILVUS_ADDR）注入。
// 自包含组件（同 ArtifactSection 范式）：props 取 psID/appID，mount 时 Promise.all 拉 deps+catalog。
export function DepsSection({ psID, appID }: { psID: string; appID: string }) {
  const [deps, setDeps] = useState<Dep[]>([]);
  const [catalog, setCatalog] = useState<DepsCatalog | null>(null);
  const [editingDeps, setEditingDeps] = useState<Dep[] | null>(null); // null=查看态，非 null=编辑态

  async function loadDeps() {
    try {
      const [d, c] = await Promise.all([
        fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/deps`).then((r) => r.json()),
        fetch(`${API_BASE_URL}/project-spaces/${psID}/deps/catalog`).then((r) => r.json()),
      ]);
      setDeps((d as Envelope<Dep[]>)?.data ?? []);
      setCatalog((c as Envelope<DepsCatalog>)?.data ?? null);
    } catch {
      // 网络抖动忽略，不阻塞面板渲染
    }
  }
  useEffect(() => {
    loadDeps();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [psID, appID]);

  async function saveDeps(next: Dep[]) {
    // PUT 整体替换：body 只发 kind/strategy（status/instance/token 由后端供给回填）。
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/deps`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(next.map((d) => ({ kind: d.kind, strategy: d.strategy }))),
    });
    const j = (await res.json()) as Envelope<unknown>;
    if (res.ok && j.code === 0) {
      toast.success("依赖已保存，下次部署生效");
      setEditingDeps(null);
      await loadDeps();
    } else {
      toast.error(j.message || "保存失败");
    }
  }

  const list = editingDeps ?? deps;
  return (
    <div className="mt-3 rounded border border-border p-3">
      <div className="mb-1 flex items-center justify-between">
        <span className="text-sm font-semibold">🔗 中间件依赖</span>
        {editingDeps ? (
          <div className="flex gap-2">
            <button
              onClick={() => setEditingDeps(null)}
              className="rounded bg-surface-2 px-2 py-0.5 text-xs text-text-muted"
            >
              取消
            </button>
            <button
              onClick={() => saveDeps(editingDeps)}
              className="rounded bg-accent px-2 py-0.5 text-xs text-white"
            >
              保存
            </button>
          </div>
        ) : (
          <button
            onClick={() => setEditingDeps(deps.length ? deps : [])}
            className="rounded bg-surface-2 px-2 py-0.5 text-xs"
          >
            编辑
          </button>
        )}
      </div>
      <p className="mb-2 text-xs text-text-muted">
        依赖在下次部署/重新部署时注入生效（REDIS_ADDR / MILVUS_ADDR 等）。
      </p>
      {list.length === 0 ? (
        <div className="text-xs text-text-muted">
          {editingDeps ? "点「+ 添加依赖」声明" : "暂无依赖"}
        </div>
      ) : (
        list.map((d, i) => {
          const cur = editingDeps ?? deps;
          const set = (patch: Partial<Dep>) =>
            setEditingDeps(cur.map((x, j) => (j === i ? { ...x, ...patch } : x)));
          return (
            <div key={d.kind} className="mb-1 flex flex-wrap items-center gap-2 text-xs">
              {editingDeps ? (
                <>
                  <select
                    value={d.kind}
                    onChange={(e) => set({ kind: e.target.value })}
                    className="rounded border border-border px-1 py-0.5"
                  >
                    {(catalog?.kinds ?? []).map((k) => (
                      <option key={k} value={k}>
                        {k}
                      </option>
                    ))}
                  </select>
                  <select
                    value={d.strategy}
                    onChange={(e) => set({ strategy: e.target.value })}
                    className="rounded border border-border px-1 py-0.5"
                  >
                    {(catalog?.strategies ?? []).map((s) => (
                      <option key={s.name} value={s.name}>
                        {s.name}
                      </option>
                    ))}
                  </select>
                  <button
                    onClick={() => setEditingDeps(cur.filter((_, j) => j !== i))}
                    className="rounded bg-danger/10 px-1.5 py-0.5 text-danger"
                  >
                    删除
                  </button>
                </>
              ) : (
                <>
                  <code className="text-text">{d.kind}</code>
                  <span className="text-text-muted">·</span>
                  <span className="text-text-muted">{d.strategy}</span>
                  <span
                    className={`rounded px-1.5 py-0.5 ${
                      d.status === "bound"
                        ? "bg-success/10 text-success"
                        : d.status === "failed"
                          ? "bg-danger/10 text-danger"
                          : "bg-surface-2 text-text-muted"
                    }`}
                  >
                    {d.status}
                  </span>
                  {d.token && <span className="text-text-muted">🔑 {d.token}</span>}
                  {d.error && <span className="text-danger">{d.error}</span>}
                </>
              )}
            </div>
          );
        })
      )}
      {editingDeps && (
        <button
          onClick={() =>
            setEditingDeps([
              ...editingDeps,
              {
                kind: catalog?.kinds?.[0] ?? "redis",
                strategy: catalog?.strategies?.[0]?.name ?? "bind_existing",
                status: "declared",
              },
            ])
          }
          className="mt-1 rounded bg-warn/10 px-2 py-0.5 text-xs text-warn"
        >
          + 添加依赖
        </button>
      )}
    </div>
  );
}

// 网络模式区：bridge(默认)/host 选择器，改选即 PUT。host 削弱隔离需 gatekeeper/admin；
// applications 页无 roles 上下文 → 不前置置灰，靠 403 toast 提示（服务端是安全真相）。
export function NetworkModeSection({
  psID,
  appID,
  mode,
}: {
  psID: string;
  appID: string;
  mode: string;
}) {
  const [m, setM] = useState(mode || "bridge");
  const [saving, setSaving] = useState(false);
  useEffect(() => setM(mode || "bridge"), [mode]);

  async function save(next: string) {
    setSaving(true);
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/network-mode`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ mode: next }),
    });
    const j = (await res.json()) as Envelope<unknown>;
    setSaving(false);
    if (res.ok && j.code === 0) {
      toast.success("网络模式已改为 " + next + "，下次部署生效");
      setM(next);
    } else {
      toast.error(j.message || "保存失败（host 需 gatekeeper/admin）");
    }
  }

  return (
    <div className="mt-3 rounded border border-border p-3">
      <div className="mb-1 text-sm font-semibold">🌐 网络模式</div>
      <p className="mb-2 text-xs text-text-muted">
        host 模式容器共享宿主网络（直接占宿主端口、直达 host-LAN），削弱隔离。默认 bridge
        更安全；host 需 gatekeeper/admin。
      </p>
      <div className="flex items-center gap-2 text-xs">
        <select
          value={m}
          onChange={(e) => save(e.target.value)}
          disabled={saving}
          className="rounded border border-border px-1 py-0.5"
        >
          <option value="bridge">bridge（默认）</option>
          <option value="host">host</option>
        </select>
        {saving && <span className="text-text-muted">保存中…</span>}
      </div>
    </div>
  );
}

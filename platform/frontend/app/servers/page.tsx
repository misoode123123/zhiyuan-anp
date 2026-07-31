"use client";

// 服务器管理看板：展示部署节点（.28 本地 / .30 远程）状态 + 资源指标，
// 支持新增节点、provision 环境搭建、手动采集、连通测试、删除、查看指标趋势。
// 消费 Task 8 扩展的 /deploy-nodes 接口（os_type/env/connect_type/latest_metric）。
import { useEffect, useState } from "react";
import { API_BASE_URL } from "@/lib/api";
import type { DeployNodeListItem, ServerMetric } from "@/lib/api-types";
import { toast } from "@/lib/toast";

type Envelope<T> = { code: number; data: T; message?: string };

const ENV_BADGE: Record<string, string> = {
  prod: "bg-danger/10 text-danger",
  test: "bg-warn/10 text-warn",
  dev: "bg-accent/10 text-accent",
};

const STATUS_DOT: Record<string, string> = {
  ready: "bg-success",
  online: "bg-success",
  provisioning: "bg-warn animate-pulse",
  provision_failed: "bg-danger",
  offline: "bg-danger",
  fail: "bg-danger",
};

function osIcon(os: string) {
  return os === "windows" ? "🪟" : "🐧";
}

function fmtBytes(bytes: number) {
  if (!bytes) return "0";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let v = bytes;
  let i = 0;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v >= 100 ? 0 : 1)} ${u[i]}`;
}

// 后端 metric 的 mem/disk 存的是 KB（free -k / df -kP / Win32_OperatingSystem 均 KB），
// 显示前 ×1024 转字节，否则 fmtBytes 把 KB 当字节、数值缩小 1024 倍（8GB 显示成 8MB）。
function fmtKB(kb: number) {
  return fmtBytes(kb * 1024);
}

function fmtPct(v: number) {
  return `${v.toFixed(1)}%`;
}

function barColor(pct: number) {
  if (pct >= 85) return "bg-danger";
  if (pct >= 60) return "bg-warn";
  return "bg-success";
}

function MetricBar({
  label,
  pct,
  used,
  total,
}: {
  label: string;
  pct: number;
  used: string;
  total: string;
}) {
  return (
    <div>
      <div className="mb-0.5 flex justify-between text-xs text-text-muted">
        <span>{label}</span>
        <span>
          {used} / {total} ({fmtPct(pct)})
        </span>
      </div>
      <div className="h-1.5 w-full overflow-hidden rounded bg-surface-2">
        <div className={`h-full ${barColor(pct)}`} style={{ width: `${Math.min(100, pct)}%` }} />
      </div>
    </div>
  );
}

export default function ServersPage() {
  const [nodes, setNodes] = useState<DeployNodeListItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [showAdd, setShowAdd] = useState(false);
  const [editing, setEditing] = useState<DeployNodeListItem | null>(null);
  const [busy, setBusy] = useState<Record<string, string>>({});
  const [metricsFor, setMetricsFor] = useState<string>("");
  const [metrics, setMetrics] = useState<ServerMetric[]>([]);

  const load = async () => {
    setLoading(true);
    setError("");
    try {
      const r = await fetch(`${API_BASE_URL}/deploy-nodes`, { cache: "no-store" });
      const j: Envelope<DeployNodeListItem[]> = await r.json();
      setNodes(j.data ?? []);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "加载失败");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
  }, []);

  // 节点状态轮询：有 provisioning 中节点时每 5s 刷新
  useEffect(() => {
    if (!nodes.some((n) => n.status === "provisioning")) return;
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, [nodes]);

  async function act(nid: string, key: string, fn: () => Promise<Response>) {
    setBusy((p) => ({ ...p, [nid]: key }));
    try {
      const res = await fn();
      const j = await res.json();
      if (j.code !== 0 && j.code !== undefined) {
        toast.error(j.message || `${key} 失败`);
      } else {
        toast.success(`${key} 完成`);
      }
      if (key !== "采集") load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : `${key} 失败`);
    } finally {
      setBusy((p) => {
        const n = { ...p };
        delete n[nid];
        return n;
      });
    }
  }

  const provision = (n: DeployNodeListItem) =>
    act(n.id, "Provision", () =>
      fetch(`${API_BASE_URL}/deploy-nodes/${n.id}/provision`, { method: "POST" })
    );
  const collect = (n: DeployNodeListItem) =>
    act(n.id, "采集", () =>
      fetch(`${API_BASE_URL}/deploy-nodes/${n.id}/collect`, { method: "POST" })
    );
  const test = (n: DeployNodeListItem) =>
    act(n.id, "测试", () => fetch(`${API_BASE_URL}/deploy-nodes/${n.id}/test`, { method: "POST" }));
  const remove = async (n: DeployNodeListItem) => {
    if (!confirm(`删除节点 ${n.name}？`)) return;
    await act(n.id, "删除", () =>
      fetch(`${API_BASE_URL}/deploy-nodes/${n.id}`, { method: "DELETE" })
    );
  };

  async function showMetrics(n: DeployNodeListItem) {
    if (metricsFor === n.id) {
      setMetricsFor("");
      setMetrics([]);
      return;
    }
    setMetricsFor(n.id);
    setMetrics([]);
    try {
      const res = await fetch(`${API_BASE_URL}/deploy-nodes/${n.id}/metrics`, {
        cache: "no-store",
      });
      const j = await res.json();
      setMetrics((j.data?.metrics as ServerMetric[]) ?? []);
    } catch {
      toast.error("指标加载失败");
    }
  }

  return (
    <div className="mx-auto max-w-6xl space-y-6 p-6">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">🖥 服务器管理</h1>
        <div className="flex gap-2">
          <button onClick={load} className="rounded bg-surface-2 px-3 py-1.5 text-sm text-text">
            刷新
          </button>
          <button
            onClick={() => setShowAdd((v) => !v)}
            className="rounded bg-accent px-3 py-1.5 text-sm text-white"
          >
            {showAdd ? "收起" : "+ 添加节点"}
          </button>
        </div>
      </div>

      {error && <div className="rounded bg-danger/10 p-2 text-sm text-danger">{error}</div>}
      {loading && <div className="text-sm text-text-muted">加载中...</div>}

      {showAdd && <NodeForm onDone={load} onCancel={() => setShowAdd(false)} />}
      {editing && (
        <NodeForm
          initial={editing}
          onDone={() => {
            setEditing(null);
            load();
          }}
          onCancel={() => setEditing(null)}
        />
      )}

      {!loading && nodes.length === 0 && (
        <div className="rounded border border-border bg-surface p-8 text-center text-sm text-text-muted">
          暂无节点，点「添加节点」纳入管理。
        </div>
      )}

      <div className="grid gap-4 md:grid-cols-2">
        {nodes.map((n) => {
          const m = n.latest_metric;
          const cpuPct = m?.cpu_percent ?? 0;
          const memPct = m && m.mem_total ? (m.mem_used / m.mem_total) * 100 : 0;
          const diskPct = m && m.disk_total ? (m.disk_used / m.disk_total) * 100 : 0;
          return (
            <div key={n.id} className="space-y-3 rounded-lg border border-border bg-surface p-4">
              {/* 头部 */}
              <div className="flex items-start justify-between gap-2">
                <div className="flex items-center gap-2">
                  <span className="text-lg">{osIcon(n.os_type)}</span>
                  <div>
                    <div className="flex items-center gap-2">
                      <span className="font-semibold text-text">{n.name}</span>
                      {n.env && (
                        <span
                          className={`rounded px-1.5 py-0.5 text-xs ${ENV_BADGE[n.env] ?? "bg-surface-2 text-text-muted"}`}
                        >
                          {n.env}
                        </span>
                      )}
                    </div>
                    <div className="text-xs text-text-muted">
                      {n.host}
                      {n.connect_type && (
                        <span className="ml-1">
                          · {n.connect_type}
                          {n.connect_type !== "docker_tcp" && `:${n.ssh_port || "-"}`}
                        </span>
                      )}
                    </div>
                  </div>
                </div>
                <div className="flex items-center gap-1.5">
                  <span
                    className={`h-2 w-2 rounded-full ${STATUS_DOT[n.status] ?? "bg-text-muted"}`}
                  />
                  <span className="text-xs text-text-muted">{n.status}</span>
                </div>
              </div>

              {/* 指标 */}
              {m ? (
                <div className="space-y-1.5">
                  <MetricBar label="CPU" pct={cpuPct} used={fmtPct(cpuPct)} total="100%" />
                  <MetricBar
                    label="内存"
                    pct={memPct}
                    used={fmtKB(m.mem_used)}
                    total={fmtKB(m.mem_total)}
                  />
                  <MetricBar
                    label="磁盘"
                    pct={diskPct}
                    used={fmtKB(m.disk_used)}
                    total={fmtKB(m.disk_total)}
                  />
                </div>
              ) : (
                <div className="text-xs text-text-muted">
                  {n.connect_type === "docker_tcp" && !n.has_os_creds && n.id !== "node_local"
                    ? "docker_tcp 节点未配置 SSH 凭证，仅走 Docker 部署（填 SSH 凭证后可采 OS 指标）"
                    : "暂无指标，点「采集」获取"}
                </div>
              )}

              {/* 元信息 */}
              <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-text-muted">
                <span>应用数：{n.app_count}</span>
                {n.connect_type === "docker_tcp" && (
                  <span>容器数：{n.latest_metric?.container_count ?? 0}</span>
                )}
                {n.last_seen && <span>最近采集：{new Date(n.last_seen).toLocaleString()}</span>}
                {n.max_apps > 0 && <span>上限：{n.max_apps}</span>}
              </div>

              {/* 操作 */}
              <div className="flex flex-wrap gap-1.5 border-t border-border pt-3">
                {n.connect_type && n.connect_type !== "docker_tcp" && (
                  <button
                    onClick={() => provision(n)}
                    disabled={!!busy[n.id]}
                    className="rounded bg-accent px-2 py-1 text-xs text-white disabled:opacity-50"
                  >
                    {busy[n.id] === "Provision" ? "..." : "Provision"}
                  </button>
                )}
                <button
                  onClick={() => collect(n)}
                  disabled={!!busy[n.id] || (!n.has_os_creds && n.id !== "node_local")}
                  title={
                    n.has_os_creds || n.id === "node_local"
                      ? "手动采集一次"
                      : "未配置 OS 凭证（SSH/WinRM），无法采集"
                  }
                  className="rounded bg-surface-2 px-2 py-1 text-xs text-text disabled:opacity-50"
                >
                  {busy[n.id] === "采集" ? "..." : "采集"}
                </button>
                <button
                  onClick={() => test(n)}
                  disabled={!!busy[n.id]}
                  className="rounded bg-surface-2 px-2 py-1 text-xs text-text disabled:opacity-50"
                >
                  {busy[n.id] === "测试" ? "..." : "测试"}
                </button>
                <button
                  onClick={() => showMetrics(n)}
                  className="rounded bg-surface-2 px-2 py-1 text-xs text-text"
                >
                  详情
                </button>
                <button
                  onClick={() => setEditing(n)}
                  disabled={!!busy[n.id]}
                  className="rounded bg-surface-2 px-2 py-1 text-xs text-text disabled:opacity-50"
                >
                  编辑
                </button>
                <button
                  onClick={() => remove(n)}
                  disabled={!!busy[n.id] || n.id === "node_local"}
                  className="ml-auto rounded bg-danger/10 px-2 py-1 text-xs text-danger disabled:opacity-50"
                  title={n.id === "node_local" ? "本地节点不可删除" : ""}
                >
                  删除
                </button>
              </div>

              {/* 指标趋势 */}
              {metricsFor === n.id && (
                <div className="max-h-48 overflow-y-auto rounded bg-surface-2 p-2 text-xs">
                  <div className="mb-1 font-semibold text-text">
                    最近指标（{metrics.length} 条）
                  </div>
                  {metrics.length === 0 ? (
                    <div className="text-text-muted">无历史数据</div>
                  ) : (
                    <table className="w-full text-left">
                      <thead className="text-text-muted">
                        <tr>
                          <th className="py-0.5">时间</th>
                          <th className="py-0.5">CPU</th>
                          <th className="py-0.5">内存</th>
                          <th className="py-0.5">磁盘</th>
                        </tr>
                      </thead>
                      <tbody>
                        {metrics.map((mm) => {
                          const mp = mm.mem_total ? (mm.mem_used / mm.mem_total) * 100 : 0;
                          const dp = mm.disk_total ? (mm.disk_used / mm.disk_total) * 100 : 0;
                          return (
                            <tr key={mm.captured_at} className="text-text">
                              <td className="py-0.5">
                                {new Date(mm.captured_at).toLocaleString()}
                              </td>
                              <td className="py-0.5">{fmtPct(mm.cpu_percent)}</td>
                              <td className="py-0.5">{fmtPct(mp)}</td>
                              <td className="py-0.5">{fmtPct(dp)}</td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  )}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function NodeForm({
  initial,
  onDone,
  onCancel,
}: {
  initial?: DeployNodeListItem;
  onDone: () => void;
  onCancel: () => void;
}) {
  const isEdit = !!initial;
  const [form, setForm] = useState({
    name: initial?.name ?? "",
    host: initial?.host ?? "",
    os_type: initial?.os_type ?? "linux",
    env: initial?.env ?? "prod",
    connect_type: initial?.connect_type ?? "ssh",
    ssh_port: initial?.ssh_port ?? 22,
    ssh_user: initial?.ssh_user ?? "root",
    ssh_key: "",
    ssh_password: "",
    winrm_user: initial?.winrm_user ?? "",
    winrm_password: "",
    winrm_port: initial?.winrm_port ?? 5985,
    docker_url: initial?.docker_url ?? "",
    max_apps: initial?.max_apps ?? 0,
    description: initial?.description ?? "",
  });
  const [saving, setSaving] = useState(false);

  const set = (k: string, v: string | number) => setForm((p) => ({ ...p, [k]: v }));

  const submit = async () => {
    if (!form.name || !form.host) {
      toast.error("名称和主机必填");
      return;
    }
    setSaving(true);
    try {
      const url = isEdit
        ? `${API_BASE_URL}/deploy-nodes/${initial!.id}`
        : `${API_BASE_URL}/deploy-nodes`;
      const method = isEdit ? "PUT" : "POST";
      // 编辑时若未填 winrm 密码则不覆盖（后端空字符串会覆盖；前端传空即清空——符合预期，用户改别的字段无需重填密码仅在新增时要求）。
      const res = await fetch(url, {
        method,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(form),
      });
      const j = await res.json();
      if (j.code !== 0 && j.code !== undefined) {
        toast.error(j.message || "保存失败");
        return;
      }
      toast.success(isEdit ? "节点已更新" : "节点已创建");
      onDone();
      onCancel();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : "保存失败");
    } finally {
      setSaving(false);
    }
  };

  const inputCls = "w-full rounded border border-border bg-surface px-2 py-1 text-sm text-text";
  const labelCls = "text-xs text-text-muted";
  const isWin = form.os_type === "windows";
  const ct = form.connect_type;
  // 按 connect_type 显示对应端口字段：ssh→ssh_port(默认 22)，winrm→winrm_port(默认 5985)，
  // docker_tcp→默认无远程端口（展示 ssh_port 兜底，不影响逻辑）。
  const showSSHPort = ct === "ssh" || ct === "docker_tcp";
  const showWinRMPort = ct === "winrm";

  return (
    <div className="space-y-3 rounded-lg border border-border bg-surface p-4">
      <div className="font-semibold text-text">{isEdit ? "编辑部署节点" : "添加部署节点"}</div>
      <div className="grid gap-3 md:grid-cols-3">
        <div>
          <div className={labelCls}>名称 *</div>
          <input
            className={inputCls}
            value={form.name}
            onChange={(e) => set("name", e.target.value)}
            placeholder="node-30"
          />
        </div>
        <div>
          <div className={labelCls}>主机 *</div>
          <input
            className={inputCls}
            value={form.host}
            onChange={(e) => set("host", e.target.value)}
            placeholder="10.10.0.30"
          />
        </div>
        <div>
          <div className={labelCls}>OS</div>
          <select
            className={inputCls}
            value={form.os_type}
            onChange={(e) => set("os_type", e.target.value)}
          >
            <option value="linux">linux</option>
            <option value="windows">windows</option>
          </select>
        </div>
        <div>
          <div className={labelCls}>环境</div>
          <select
            className={inputCls}
            value={form.env}
            onChange={(e) => set("env", e.target.value)}
          >
            <option value="prod">prod</option>
            <option value="test">test</option>
            <option value="dev">dev</option>
          </select>
        </div>
        <div>
          <div className={labelCls}>连接方式</div>
          <select
            className={inputCls}
            value={form.connect_type}
            onChange={(e) => set("connect_type", e.target.value)}
          >
            <option value="ssh">ssh</option>
            <option value="winrm">winrm</option>
            <option value="docker_tcp">docker_tcp</option>
          </select>
        </div>
        {showSSHPort && (
          <div>
            <div className={labelCls}>SSH 端口</div>
            <input
              type="number"
              className={inputCls}
              value={form.ssh_port}
              onChange={(e) => set("ssh_port", Number(e.target.value))}
            />
          </div>
        )}
        {showWinRMPort && (
          <div>
            <div className={labelCls}>WinRM 端口</div>
            <input
              type="number"
              className={inputCls}
              value={form.winrm_port}
              onChange={(e) => set("winrm_port", Number(e.target.value))}
            />
          </div>
        )}
        {(ct === "ssh" || ct === "docker_tcp") && (
          <>
            {ct === "docker_tcp" && (
              <div className="md:col-span-3 text-xs text-text-muted">
                docker_tcp 节点：以下 SSH 凭证仅用于 OS 指标采集，部署仍走 Docker URL。
              </div>
            )}
            <div>
              <div className={labelCls}>SSH 用户</div>
              <input
                className={inputCls}
                value={form.ssh_user}
                onChange={(e) => set("ssh_user", e.target.value)}
              />
            </div>
            <div>
              <div className={labelCls}>SSH 密码</div>
              <input
                className={inputCls}
                type="password"
                value={form.ssh_password}
                onChange={(e) => set("ssh_password", e.target.value)}
                placeholder={isEdit ? "编辑时留空不修改密码" : ""}
              />
            </div>
            <div className="md:col-span-2">
              <div className={labelCls}>SSH 私钥（PEM）</div>
              <textarea
                className={inputCls}
                rows={2}
                value={form.ssh_key}
                onChange={(e) => set("ssh_key", e.target.value)}
                placeholder={
                  isEdit ? "编辑时留空不修改私钥" : "-----BEGIN OPENSSH PRIVATE KEY-----"
                }
              />
            </div>
          </>
        )}
        {ct === "winrm" && (
          <>
            <div>
              <div className={labelCls}>WinRM 用户</div>
              <input
                className={inputCls}
                value={form.winrm_user}
                onChange={(e) => set("winrm_user", e.target.value)}
              />
            </div>
            <div>
              <div className={labelCls}>WinRM 密码</div>
              <input
                className={inputCls}
                type="password"
                value={form.winrm_password}
                onChange={(e) => set("winrm_password", e.target.value)}
                placeholder={isEdit ? "编辑时留空不修改密码" : ""}
              />
            </div>
          </>
        )}
        <div className="md:col-span-2">
          <div className={labelCls}>Docker URL（docker_tcp 必填，其他可空走默认）</div>
          <input
            className={inputCls}
            value={form.docker_url}
            onChange={(e) => set("docker_url", e.target.value)}
            placeholder="tcp://10.10.0.30:2375"
          />
        </div>
        <div>
          <div className={labelCls}>应用上限（0=不限）</div>
          <input
            type="number"
            className={inputCls}
            value={form.max_apps}
            onChange={(e) => set("max_apps", Number(e.target.value))}
          />
        </div>
        <div className="md:col-span-3">
          <div className={labelCls}>描述</div>
          <input
            className={inputCls}
            value={form.description}
            onChange={(e) => set("description", e.target.value)}
          />
        </div>
      </div>
      <div className="flex gap-2">
        <button
          onClick={submit}
          disabled={saving}
          className="rounded bg-accent px-3 py-1.5 text-sm text-white disabled:opacity-50"
        >
          {saving ? "保存中..." : "保存"}
        </button>
        <button onClick={onCancel} className="rounded bg-surface-2 px-3 py-1.5 text-sm text-text">
          取消
        </button>
      </div>
    </div>
  );
}

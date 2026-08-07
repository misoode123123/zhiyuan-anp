"use client";

// 数据库管理：展示 pgsupply 供给的 PG 实例 + 应用库（database-per-app）。
// 只读查看（建库/清理由应用部署流程触发）。点应用库展开看 DATABASE_URL（mask）
// + 数据库工具（类 DBeaver）：表结构 / SQL 执行 / 操作日志。
import { useEffect, useState } from "react";
import { apiGet, API_BASE_URL } from "@/lib/api";

type Envelope<T> = { code: number; data: T; message?: string };
type PGInstance = {
  id: string;
  project_space_id: string;
  host: string;
  port: number;
  deploy_mode: string;
  status: string;
  created_at: string;
};
type AppDatabase = {
  id: string;
  app_id: string;
  project_space_id: string;
  db_name: string;
  db_role: string;
  pg_instance_id: string;
  db_host: string;
  db_port: number;
  status: string;
  backup_enabled: boolean;
  last_backup_at?: string;
  size_bytes: number; // 3b：库大小定时采集（0=未采）
  created_at: string;
  updated_at: string;
};
type DatabaseDetail = {
  database: AppDatabase;
  database_url: string; // mask 后（密码隐藏）
};
// 数据库工具相关类型
type TableInfo = { name: string; table_type: string };
type ColumnInfo = {
  name: string;
  data_type: string;
  is_nullable: string;
  column_default: string;
  comment: string;
};
type QueryResult = {
  action_type: string;
  columns?: string[];
  rows?: Record<string, unknown>[];
  row_count: number;
};
type ActionLog = {
  id: string;
  app_id: string;
  db_name: string;
  actor: string;
  action_type: string;
  statement: string;
  row_count: number;
  status: string;
  error?: string;
  created_at: string;
};

const STATUS_COLOR: Record<string, string> = {
  active: "bg-success/10 text-success",
  ready: "bg-success/10 text-success",
  provisioning: "bg-warn/10 text-warn",
  draining: "bg-warn/10 text-warn",
  failed: "bg-danger/10 text-danger",
  maintenance: "bg-surface-2 text-text-muted",
};

const ACTION_COLOR: Record<string, string> = {
  SELECT: "bg-accent/10 text-accent",
  INSERT: "bg-success/10 text-success",
  UPDATE: "bg-warn/10 text-warn",
  DELETE: "bg-danger/10 text-danger",
  DDL: "bg-warn/10 text-warn",
  OTHER: "bg-surface-2 text-text-muted",
};

// formatBytes 字节 → 自适应单位（KB/MB/GB），保留 2 位小数；0 显示「—」。
function formatBytes(bytes: number): string {
  if (!bytes || bytes <= 0) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let v = bytes;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(i === 0 ? 0 : 2)} ${units[i]}`;
}

export default function DatabasesPage() {
  const [instances, setInstances] = useState<PGInstance[]>([]);
  const [databases, setDatabases] = useState<AppDatabase[]>([]);
  const [detailFor, setDetailFor] = useState<string | null>(null);
  const [detail, setDetail] = useState<DatabaseDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const load = async () => {
    setLoading(true);
    setError("");
    try {
      const [ins, dbs] = await Promise.all([
        apiGet<Envelope<PGInstance[]>>("/pgsupply/instances"),
        apiGet<Envelope<AppDatabase[]>>("/pgsupply/databases"),
      ]);
      setInstances(ins.data ?? []);
      setDatabases(dbs.data ?? []);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "加载失败");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
  }, []);

  const showDetail = async (psID: string, appID: string) => {
    if (detailFor === appID) {
      setDetailFor(null);
      setDetail(null);
      return;
    }
    setDetailFor(appID);
    setDetail(null);
    try {
      const d = await apiGet<Envelope<DatabaseDetail>>(
        `/project-spaces/${psID}/apps/${appID}/database`
      );
      setDetail(d.data);
    } catch {
      setDetail(null);
    }
  };

  return (
    <div className="mx-auto max-w-5xl space-y-6 p-6">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">🗄️ 数据库管理</h1>
        <button onClick={load} className="rounded bg-accent px-3 py-1.5 text-sm text-white">
          刷新
        </button>
      </div>
      {error && <div className="rounded bg-danger/10 p-2 text-sm text-danger">{error}</div>}
      {loading && <div className="text-sm text-text-muted">加载中...</div>}

      {/* PG 实例 */}
      <section>
        <h2 className="mb-2 text-sm font-semibold text-text">
          PG 实例（每项目一个独立实例，无 pgbouncer）
        </h2>
        {!loading && instances.length === 0 && (
          <div className="text-sm text-text-muted">暂无实例</div>
        )}
        <div className="space-y-2">
          {instances.map((ins) => (
            <div key={ins.id} className="rounded-lg border border-border bg-surface p-3 text-sm">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-mono">
                  {ins.host}:{ins.port}
                </span>
                <span
                  className={`rounded px-1.5 py-0.5 text-xs ${STATUS_COLOR[ins.status] ?? "bg-surface-2 text-text-muted"}`}
                >
                  {ins.status}
                </span>
                <span className="rounded bg-accent/10 px-1.5 py-0.5 text-xs text-accent">
                  {ins.deploy_mode}
                </span>
              </div>
              <div className="mt-1 text-xs text-text-muted">
                项目 {ins.project_space_id} · 实例 {ins.id} · 创建 {ins.created_at}
              </div>
            </div>
          ))}
        </div>
      </section>

      {/* 应用库 */}
      <section>
        <h2 className="mb-2 text-sm font-semibold text-text">
          应用库（database-per-app，应用直连）
        </h2>
        {!loading && databases.length === 0 && (
          <div className="text-sm text-text-muted">
            暂无应用库（建应用后自动供给独立库 + DATABASE_URL）
          </div>
        )}
        <div className="space-y-2">
          {databases.map((db) => (
            <div key={db.id} className="rounded-lg border border-border bg-surface p-3 text-sm">
              <button
                onClick={() => showDetail(db.project_space_id, db.app_id)}
                className="flex w-full items-center justify-between text-left"
              >
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-mono">{db.db_name}</span>
                  <span
                    className={`rounded px-1.5 py-0.5 text-xs ${STATUS_COLOR[db.status] ?? "bg-surface-2 text-text-muted"}`}
                  >
                    {db.status}
                  </span>
                  {db.backup_enabled && (
                    <span className="rounded bg-success/10 px-1.5 py-0.5 text-xs text-success">
                      备份开
                    </span>
                  )}
                </div>
                <span className="text-xs text-text-muted">
                  {detailFor === db.app_id ? "▲" : "▼"}
                </span>
              </button>
              <div className="mt-1 text-xs text-text-muted">
                应用 {db.app_id} · 项目 {db.project_space_id} · {db.db_host}:{db.db_port} · 大小{" "}
                {formatBytes(db.size_bytes)} ·{" "}
                {db.last_backup_at ? `备份 ${db.last_backup_at}` : "未备份"}
              </div>
              {detailFor === db.app_id && (
                <div className="mt-2 space-y-2 rounded bg-bg p-2 text-xs">
                  {detail ? (
                    <>
                      <div>
                        <span className="text-text-muted">DATABASE_URL：</span>
                        <code className="break-all font-mono">{detail.database_url}</code>
                      </div>
                      <div>
                        <span className="text-text-muted">role：</span>
                        {db.db_role}
                      </div>
                      <div>
                        <span className="text-text-muted">实例：</span>
                        {db.pg_instance_id}
                      </div>
                      {/* 数据库工具（类 DBeaver）：表结构 / SQL 执行 / 操作日志 */}
                      <DatabaseTools psID={db.project_space_id} appID={db.app_id} />
                    </>
                  ) : (
                    <div className="text-text-muted">加载详情...</div>
                  )}
                </div>
              )}
            </div>
          ))}
        </div>
      </section>
    </div>
  );
}

// TabBtn 库工具面板的小 tab 按钮（模块级组件，避免在组件内定义组件触发 react-hooks 规则）。
function TabBtn({
  label,
  active,
  onClick,
}: {
  label: string;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      className={`rounded px-2 py-1 text-xs ${
        active ? "bg-accent text-white" : "bg-surface text-text-muted hover:bg-surface-2"
      }`}
    >
      {label}
    </button>
  );
}

// DatabaseTools 应用库工具面板：3 个 tab（表结构 / SQL 执行 / 操作日志）。
// 每个应用库展开后独立挂一份，状态互不干扰。
function DatabaseTools({ psID, appID }: { psID: string; appID: string }) {
  const [tab, setTab] = useState<"tables" | "sql" | "actions">("tables");
  // 表结构
  const [tables, setTables] = useState<TableInfo[]>([]);
  const [selTable, setSelTable] = useState<string>("");
  const [columns, setColumns] = useState<ColumnInfo[]>([]);
  const [tblLoading, setTblLoading] = useState(false);
  const [tblError, setTblError] = useState("");
  // SQL 执行
  const [sql, setSql] = useState("SELECT 1;");
  const [result, setResult] = useState<QueryResult | null>(null);
  const [sqlError, setSqlError] = useState("");
  const [running, setRunning] = useState(false);
  // 操作日志
  const [actions, setActions] = useState<ActionLog[]>([]);
  const [actLoading, setActLoading] = useState(false);

  const loadTables = async () => {
    setTblLoading(true);
    setTblError("");
    try {
      const r = await apiGet<Envelope<TableInfo[]>>(
        `/project-spaces/${psID}/apps/${appID}/database/tables`
      );
      setTables(r.data ?? []);
    } catch (e: unknown) {
      setTblError(e instanceof Error ? e.message : "加载表失败");
    } finally {
      setTblLoading(false);
    }
  };

  const loadColumns = async (table: string) => {
    setSelTable(table);
    setTblError("");
    setColumns([]);
    try {
      const r = await apiGet<Envelope<ColumnInfo[]>>(
        `/project-spaces/${psID}/apps/${appID}/database/tables/${encodeURIComponent(table)}/columns`
      );
      setColumns(r.data ?? []);
    } catch (e: unknown) {
      setTblError(e instanceof Error ? e.message : "加载列失败");
    }
  };

  const runSQL = async () => {
    setRunning(true);
    setSqlError("");
    setResult(null);
    try {
      const res = await fetch(
        `${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/database/query`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ sql }),
        }
      );
      const r: Envelope<QueryResult> = await res.json();
      if (r.code !== 0) {
        setSqlError(r.message ?? "执行失败");
      } else {
        setResult(r.data);
      }
      // 执行后刷新操作日志（若当前在日志 tab）
      if (tab === "actions") loadActions();
    } catch (e: unknown) {
      setSqlError(e instanceof Error ? e.message : "执行失败");
    } finally {
      setRunning(false);
    }
  };

  const loadActions = async () => {
    setActLoading(true);
    try {
      const r = await apiGet<Envelope<ActionLog[]>>(
        `/project-spaces/${psID}/apps/${appID}/database/actions`
      );
      setActions(r.data ?? []);
    } catch {
      // 忽略：tab 切换容错
    } finally {
      setActLoading(false);
    }
  };

  // 首次挂载拉表列表；切到 actions tab 拉日志
  useEffect(() => {
    loadTables();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  useEffect(() => {
    if (tab === "actions") loadActions();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab]);

  return (
    <div className="mt-2 border-t border-border pt-2">
      <div className="flex items-center gap-1">
        <TabBtn label="📋 表结构" active={tab === "tables"} onClick={() => setTab("tables")} />
        <TabBtn label="▶ SQL 执行" active={tab === "sql"} onClick={() => setTab("sql")} />
        <TabBtn label="📝 操作日志" active={tab === "actions"} onClick={() => setTab("actions")} />
      </div>

      {tab === "tables" && (
        <div className="mt-2">
          {tblLoading && <div className="text-text-muted">加载表...</div>}
          {tblError && <div className="text-danger break-all">表结构加载失败：{tblError}</div>}
          {!tblLoading && tables.length === 0 && !tblError && (
            <div className="text-text-muted">暂无表（public schema）</div>
          )}
          {tables.length > 0 && (
            <div className="grid grid-cols-1 gap-2 md:grid-cols-3">
              {/* 左：表列表 */}
              <div className="space-y-0.5">
                {tables.map((t) => (
                  <button
                    key={t.name}
                    onClick={() => loadColumns(t.name)}
                    className={`block w-full truncate rounded px-1.5 py-0.5 text-left font-mono ${
                      selTable === t.name ? "bg-accent/10 text-accent" : "hover:bg-surface-2"
                    }`}
                    title={t.name}
                  >
                    {t.table_type === "VIEW" ? "👁 " : "▤ "}
                    {t.name}
                  </button>
                ))}
              </div>
              {/* 右：列详情 */}
              <div className="md:col-span-2">
                {!selTable && <div className="text-text-muted">点左侧表名查看列</div>}
                {selTable && (
                  <table className="w-full border-collapse text-[11px]">
                    <thead>
                      <tr className="bg-surface-2 text-left">
                        <th className="border px-1 py-0.5">列名</th>
                        <th className="border px-1 py-0.5">类型</th>
                        <th className="border px-1 py-0.5">可空</th>
                        <th className="border px-1 py-0.5">默认</th>
                        <th className="border px-1 py-0.5">注释</th>
                      </tr>
                    </thead>
                    <tbody>
                      {columns.map((c) => (
                        <tr key={c.name} className="align-top">
                          <td className="border px-1 py-0.5 font-mono">{c.name}</td>
                          <td className="border px-1 py-0.5 font-mono text-accent">
                            {c.data_type}
                          </td>
                          <td
                            className={`border px-1 py-0.5 ${c.is_nullable === "NO" ? "text-danger" : "text-text-muted"}`}
                          >
                            {c.is_nullable === "NO" ? "NOT NULL" : "NULL"}
                          </td>
                          <td className="border px-1 py-0.5 font-mono text-text-muted">
                            {c.column_default || "—"}
                          </td>
                          <td className="border px-1 py-0.5 text-text-muted">{c.comment || "—"}</td>
                        </tr>
                      ))}
                      {columns.length === 0 && (
                        <tr>
                          <td colSpan={5} className="border px-1 py-0.5 text-text-muted">
                            加载列...
                          </td>
                        </tr>
                      )}
                    </tbody>
                  </table>
                )}
              </div>
            </div>
          )}
        </div>
      )}

      {tab === "sql" && (
        <div className="mt-2 space-y-1">
          <textarea
            value={sql}
            onChange={(e) => setSql(e.target.value)}
            rows={4}
            spellCheck={false}
            className="w-full rounded border border-border p-1 font-mono text-[11px]"
            placeholder="输入 SQL（SELECT 返回结果集；DDL/DML 返回影响行数）"
          />
          <div className="flex items-center gap-2">
            <button
              onClick={runSQL}
              disabled={running || !sql.trim()}
              className="rounded bg-success px-2 py-0.5 text-xs text-white disabled:bg-surface-2"
            >
              {running ? "执行中..." : "▶ 执行"}
            </button>
            <span className="text-[11px] text-text-muted">
              以应用 role 执行（仅本库权限）；每次执行均记审计
            </span>
          </div>
          {sqlError && (
            <div className="rounded bg-danger/10 p-1 text-[11px] text-danger break-all">
              {sqlError}
            </div>
          )}
          {result && (
            <div className="rounded bg-surface-2 p-1">
              <div className="mb-1 text-[11px] text-text-muted">
                <span
                  className={`rounded px-1 ${ACTION_COLOR[result.action_type] ?? "bg-surface-2"}`}
                >
                  {result.action_type}
                </span>{" "}
                · {result.row_count} 行
              </div>
              {result.action_type === "SELECT" && result.columns && result.columns.length > 0 && (
                <div className="max-h-64 overflow-auto">
                  <table className="w-full border-collapse text-[11px]">
                    <thead>
                      <tr className="bg-surface-2 text-left">
                        {result.columns.map((c) => (
                          <th key={c} className="border px-1 py-0.5 font-mono whitespace-nowrap">
                            {c}
                          </th>
                        ))}
                      </tr>
                    </thead>
                    <tbody>
                      {(result.rows ?? []).map((row, i) => (
                        <tr key={i}>
                          {result.columns!.map((c) => (
                            <td key={c} className="border px-1 py-0.5 font-mono whitespace-nowrap">
                              {String(row[c] ?? "")}
                            </td>
                          ))}
                        </tr>
                      ))}
                      {(result.rows ?? []).length === 0 && (
                        <tr>
                          <td
                            colSpan={result.columns.length}
                            className="border px-1 py-0.5 text-text-muted"
                          >
                            （空结果集）
                          </td>
                        </tr>
                      )}
                    </tbody>
                  </table>
                </div>
              )}
            </div>
          )}
        </div>
      )}

      {tab === "actions" && (
        <div className="mt-2">
          <div className="mb-1 flex items-center gap-2">
            <span className="text-text-muted">最近 SQL 操作（倒序）</span>
            <button
              onClick={loadActions}
              className="rounded bg-surface-2 px-1.5 py-0.5 text-[11px]"
            >
              ↻ 刷新
            </button>
          </div>
          {actLoading && <div className="text-text-muted">加载...</div>}
          {!actLoading && actions.length === 0 && (
            <div className="text-text-muted">暂无操作日志</div>
          )}
          {actions.length > 0 && (
            <div className="space-y-0.5">
              {actions.map((a) => (
                <div key={a.id} className="rounded bg-surface p-1 text-[11px] border border-border">
                  <div className="flex flex-wrap items-center gap-1">
                    <span
                      className={`rounded px-1 ${ACTION_COLOR[a.action_type] ?? "bg-surface-2"}`}
                    >
                      {a.action_type}
                    </span>
                    <span
                      className={`rounded px-1 ${a.status === "success" ? "bg-success/10 text-success" : "bg-danger/10 text-danger"}`}
                    >
                      {a.status}
                    </span>
                    {a.status === "success" && (
                      <span className="text-text-muted">{a.row_count} 行</span>
                    )}
                    <span className="text-text-muted">操作人 {a.actor}</span>
                    <span className="text-text-muted">
                      {new Date(a.created_at).toLocaleString("zh-CN", {
                        hour12: false,
                      })}
                    </span>
                  </div>
                  <code className="mt-0.5 block truncate font-mono text-text">{a.statement}</code>
                  {a.error && <div className="mt-0.5 text-danger break-all">{a.error}</div>}
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

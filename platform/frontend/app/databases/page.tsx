"use client";

// 数据库管理：展示 pgsupply 供给的 PG 实例 + 应用库（database-per-app）。
// 只读查看（建库/清理由应用部署流程触发）。点应用库展开看 DATABASE_URL（mask）。
import { useEffect, useState } from "react";
import { apiGet } from "@/lib/api";

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
  created_at: string;
  updated_at: string;
};
type DatabaseDetail = {
  database: AppDatabase;
  database_url: string; // mask 后（密码隐藏）
};

const STATUS_COLOR: Record<string, string> = {
  active: "bg-emerald-100 text-emerald-700",
  ready: "bg-emerald-100 text-emerald-700",
  provisioning: "bg-amber-100 text-amber-700",
  draining: "bg-amber-100 text-amber-700",
  failed: "bg-red-100 text-red-700",
  maintenance: "bg-neutral-100 text-neutral-500",
};

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
        `/project-spaces/${psID}/apps/${appID}/database`,
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
        <button
          onClick={load}
          className="rounded bg-blue-600 px-3 py-1.5 text-sm text-white"
        >
          刷新
        </button>
      </div>
      {error && (
        <div className="rounded bg-red-50 p-2 text-sm text-red-700">{error}</div>
      )}
      {loading && <div className="text-sm text-neutral-400">加载中...</div>}

      {/* PG 实例 */}
      <section>
        <h2 className="mb-2 text-sm font-semibold text-neutral-700">
          PG 实例（每项目一个独立实例，无 pgbouncer）
        </h2>
        {!loading && instances.length === 0 && (
          <div className="text-sm text-neutral-400">暂无实例</div>
        )}
        <div className="space-y-2">
          {instances.map((ins) => (
            <div
              key={ins.id}
              className="rounded-lg border border-neutral-200 bg-white p-3 text-sm"
            >
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-mono">
                  {ins.host}:{ins.port}
                </span>
                <span
                  className={`rounded px-1.5 py-0.5 text-xs ${STATUS_COLOR[ins.status] ?? "bg-neutral-100 text-neutral-500"}`}
                >
                  {ins.status}
                </span>
                <span className="rounded bg-blue-100 px-1.5 py-0.5 text-xs text-blue-700">
                  {ins.deploy_mode}
                </span>
              </div>
              <div className="mt-1 text-xs text-neutral-500">
                项目 {ins.project_space_id} · 实例 {ins.id} · 创建{" "}
                {ins.created_at}
              </div>
            </div>
          ))}
        </div>
      </section>

      {/* 应用库 */}
      <section>
        <h2 className="mb-2 text-sm font-semibold text-neutral-700">
          应用库（database-per-app，应用直连）
        </h2>
        {!loading && databases.length === 0 && (
          <div className="text-sm text-neutral-400">
            暂无应用库（建应用后自动供给独立库 + DATABASE_URL）
          </div>
        )}
        <div className="space-y-2">
          {databases.map((db) => (
            <div
              key={db.id}
              className="rounded-lg border border-neutral-200 bg-white p-3 text-sm"
            >
              <button
                onClick={() => showDetail(db.project_space_id, db.app_id)}
                className="flex w-full items-center justify-between text-left"
              >
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-mono">{db.db_name}</span>
                  <span
                    className={`rounded px-1.5 py-0.5 text-xs ${STATUS_COLOR[db.status] ?? "bg-neutral-100 text-neutral-500"}`}
                  >
                    {db.status}
                  </span>
                  {db.backup_enabled && (
                    <span className="rounded bg-emerald-50 px-1.5 py-0.5 text-xs text-emerald-600">
                      备份开
                    </span>
                  )}
                </div>
                <span className="text-xs text-neutral-400">
                  {detailFor === db.app_id ? "▲" : "▼"}
                </span>
              </button>
              <div className="mt-1 text-xs text-neutral-500">
                应用 {db.app_id} · 项目 {db.project_space_id} · {db.db_host}:
                {db.db_port} ·{" "}
                {db.last_backup_at ? `备份 ${db.last_backup_at}` : "未备份"}
              </div>
              {detailFor === db.app_id && (
                <div className="mt-2 space-y-1 rounded bg-neutral-50 p-2 text-xs">
                  {detail ? (
                    <>
                      <div>
                        <span className="text-neutral-500">DATABASE_URL：</span>
                        <code className="break-all font-mono">
                          {detail.database_url}
                        </code>
                      </div>
                      <div>
                        <span className="text-neutral-500">role：</span>
                        {db.db_role}
                      </div>
                      <div>
                        <span className="text-neutral-500">实例：</span>
                        {db.pg_instance_id}
                      </div>
                    </>
                  ) : (
                    <div className="text-neutral-400">加载详情...</div>
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

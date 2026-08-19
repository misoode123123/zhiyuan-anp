"use client";

// 单应用 tab 工作区（spec §2）：编排组件。横幅/状态头/探活卡/工具行自 page.tsx 等价平移；
// 环境实例区换 T6 EnvDeployCard；Detail 自管（spec §4③）；reg 四态局部；wsTool props 提升壳。
import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { API_BASE_URL } from "@/lib/api";
import { toast } from "@/lib/toast";
import { STATUS_COLOR, deployFinished } from "../_lib/predicates";
import type { App, AppStats, ChangeSummary, Detail, Envelope } from "../_lib/types";
import type { useAppData } from "../_lib/use-app-data";
import type { useAppActions } from "../_lib/use-app-actions";
import { EnvDeployCard } from "./env-deploy-card";
import { DetailPanels } from "./detail-panels";
import { ExternalProbeCard, StatusBanners } from "./panel-banners";
import { ArtifactSection, DepsSection, DevWizard, NetworkModeSection } from "./sections";

export function AppTabPanel(props: {
  app: App;
  psID: string;
  selectedNode: string;
  wsTool: string;
  setWsTool: (t: string) => void;
  appStats?: AppStats;
  appChanges: ChangeSummary[];
  data: ReturnType<typeof useAppData>;
  actions: ReturnType<typeof useAppActions>;
  onClose: () => void; // ✕ 关闭 tab
}) {
  const { app, psID, selectedNode, data, actions, onClose } = props;
  const { wsTool, setWsTool, appStats, appChanges } = props;
  const isExternal = app.deploy_mode === "external";
  // 导入进行中：status===importing，编码/部署按钮禁用
  const isImporting = app.status === "importing";
  const router = useRouter();
  // Detail 自管（spec §4）：挂载即拉；失败 toast + 内容区重试按钮（detailErr 态）。
  const [detail, setDetail] = useState<Detail | null>(null);
  const [detailErr, setDetailErr] = useState(false);
  async function loadDetail() {
    setDetailErr(false);
    try {
      const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${app.id}/detail`);
      const r: Envelope<Detail> = await res.json();
      setDetail(r.data ?? null);
    } catch {
      toast.error(`加载应用详情失败：${app.name}`);
      setDetailErr(true);
    }
  }
  useEffect(() => {
    setDetail(null);
    loadDetail();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [psID, app.id]);
  // spec §4（终审 Important #1）：detail 原本仅挂载/重试/闭环动作时拉取，部署完成后
  // 不刷新——卡内时间线看不到刚完成的部署。app prop 随壳 3s 轮询更新，这里用 ref 记
  // 上一渲染的原始值（引用每轮都变，必须按值比较）；deployFinished 谓词在 _lib
  // （离开 building/preparing 到终态，或 version 增大）→ 重拉 detail，时间线即时
  // 可见。ref 初值 null：首渲染只记录不拉取（挂载 effect 已拉，防双请求）。
  const prevSV = useRef<{ status: string; version: number } | null>(null);
  useEffect(() => {
    const prev = prevSV.current;
    prevSV.current = { status: app.status, version: app.version };
    if (prev && deployFinished(prev, { status: app.status, version: app.version })) loadDetail();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [app.status, app.version]);
  // 登记变更四态（面板局部）：成功后清输入 + 刷新 detail。
  const [regFor, setRegFor] = useState("");
  const [regReq, setRegReq] = useState("");
  const [regNote, setRegNote] = useState("");
  const [regBusy, setRegBusy] = useState(false);
  // app 级失败兜底重试 env：最近 failed 实例 env，否则 "test"（原 page.tsx 逻辑平移）
  const retryEnv =
    app.instances
      ?.filter((i) => i.status === "failed")
      .sort((x, y) => (y.updated_at || "").localeCompare(x.updated_at || ""))[0]?.env || "test";
  // 闭环动作包装：壳给 hook 注入 detailFor:""（detail 分支不触发，仅 loadChanges），
  // 面板自有 detail 由这里的 afterDetail 单通道刷新（归属=本应用恒真，无双请求）。
  const afterDetail =
    (fn: (a: string, b: string, c?: string) => Promise<unknown>) =>
    async (a: string, b: string, c?: string) => {
      await fn(a, b, c);
      data.refreshClosedLoop(app.id, app.id, setDetail);
    };
  async function registerChange(appID: string, reqID: string, note: string): Promise<void> {
    setRegBusy(true);
    try {
      if (await actions.registerChange(appID, reqID, note)) {
        setRegFor("");
        setRegReq("");
        setRegNote("");
        data.refreshClosedLoop(app.id, app.id, setDetail);
      }
    } finally {
      setRegBusy(false);
    }
  }

  return (
    <div className="rounded-lg border border-border bg-surface p-3" data-testid="app-tab-panel">
      <StatusBanners
        app={app}
        isImporting={isImporting}
        retryEnv={retryEnv}
        onRetryFixed={() => actions.act(app.id, "deploy", retryEnv, selectedNode, "fixed")}
      />
      {/* 状态头：名字/徽章/导入来源/repo 行；✕ 关 tab */}
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-mono font-medium">{app.name}</span>
        {isExternal && (
          <span className="rounded bg-accent/10 px-1.5 py-0.5 text-xs text-accent">
            external · 纳管
          </span>
        )}
        {/* 导入来源徽章：git=远程仓 / dir=本机zip或服务器目录；空=平台建仓（不显示） */}
        {app.import_source === "git" && (
          <span
            className="rounded bg-success/10 px-1.5 py-0.5 text-xs text-success"
            title={"导入自 git 仓库: " + (app.import_ref || "")}
          >
            📥 git
          </span>
        )}
        {app.import_source === "dir" && (
          <span
            className="rounded bg-warn/10 px-1.5 py-0.5 text-xs text-warn"
            title={"导入自 目录/zip: " + (app.import_ref || "")}
          >
            📁 目录
          </span>
        )}
        <span
          className={`rounded px-1.5 py-0.5 text-xs ${STATUS_COLOR[app.status] ?? "bg-surface-2"}`}
        >
          {app.status}
        </span>
        {(app.status === "building" || app.status === "preparing" || app.status === "running") &&
          app.instances &&
          app.instances.some((i) => i.status === "building" || i.status === "preparing") && (
            <span className="flex items-center gap-1 text-xs text-warn">
              <span className="inline-block h-3 w-3 animate-spin rounded-full border-2 border-warn border-t-transparent"></span>
              构建中...
            </span>
          )}
        {(app.status === "building" || app.status === "preparing") &&
          app.instances &&
          app.instances.some((i) => i.status === "failed") && (
            <span className="text-xs text-danger">构建失败</span>
          )}
        {!isExternal && app.image && (
          <span className="text-xs text-text-muted">
            v{app.version} · {app.image}
          </span>
        )}
        <button
          onClick={onClose}
          className="ml-auto rounded bg-surface-2 px-2 py-0.5 text-xs"
          title="关闭此应用 tab（回总览）"
        >
          ✕ 关闭
        </button>
      </div>
      <div className="mt-1 text-xs text-text-muted">
        {isExternal ? (
          <>
            external_url:{" "}
            <a
              href={app.external_url}
              target="_blank"
              rel="noreferrer"
              className="text-accent hover:underline"
            >
              {app.external_url}
            </a>{" "}
            · 统一入口 <code>/apps/{app.id}/</code>
          </>
        ) : (
          <>
            repo: <code>{app.repo_dir}</code> · 内部端口 {app.internal_port}
            {app.host_port ? ` · 宿主端口 ${app.host_port}` : ""}
          </>
        )}
      </div>
      {app.updated_at && (
        <div className="text-xs text-text-muted">
          {app.status === "running" ? "部署于" : "更新于"}：
          {new Date(app.updated_at).toLocaleString("zh-CN", { hour12: false })}
        </div>
      )}

      {/* 环境并排区：external=探活卡；managed=2×EnvDeployCard */}
      {isExternal ? (
        <ExternalProbeCard appStats={appStats} />
      ) : (
        <div className="mt-2 grid grid-cols-1 gap-2 sm:grid-cols-2">
          {(["test", "prod"] as const).map((env) => (
            <EnvDeployCard
              key={env}
              app={app}
              env={env}
              instance={app.instances?.find((i) => i.env === env)}
              stats={appStats}
              history={detail?.deploy_history ?? []}
              pendingChanges={appChanges.filter((c) => c.status === "approved")}
              deployMsg={data.deployMsg[app.id]}
              busy={isImporting}
              onDeploy={() => actions.act(app.id, "deploy", "test", selectedNode)}
              onPromote={() => actions.promoteWithNode(app.id, selectedNode)}
              onRetryFixed={() => actions.act(app.id, "deploy", env, selectedNode, "fixed")}
              onLogs={() => actions.showLogs(app.id)}
            />
          ))}
        </div>
      )}

      {/* 工具行：编码工具+编码+变量+停止/启动+需求+日志+删除（构建部署/上线/重试吸附环境卡；「详情」删除——面板常驻 Detail） */}
      <div className="mt-2 flex flex-wrap items-center gap-1">
        {isExternal ? (
          <a
            href={app.external_url}
            target="_blank"
            rel="noreferrer"
            className="rounded bg-accent/10 px-2 py-0.5 text-xs text-accent"
            title="打开外部应用地址"
          >
            🔗 访问
          </a>
        ) : (
          <>
            <select
              value={wsTool}
              onChange={(e) => setWsTool(e.target.value)}
              className="rounded border border-border px-1 py-0.5 text-xs disabled:opacity-40"
              title="选择交互编码工具"
              disabled={isImporting}
            >
              <option value="opencode">opencode</option>
              <option value="claude">claude</option>
              <option value="codex">codex</option>
            </select>
            <button
              onClick={() =>
                router.push(
                  `/workspace?app=${app.id}&ps=${psID}&tool=${encodeURIComponent(wsTool)}`
                )
              }
              className="rounded bg-warn/10 px-2 py-0.5 text-xs text-warn disabled:cursor-not-allowed disabled:opacity-40"
              disabled={isImporting}
              title={
                isImporting ? "导入完成前不可编码（仓库尚未就绪）" : "打开该工具的官方交互编码界面"
              }
            >
              🧑‍💻 编码
            </button>
            <button
              onClick={() => actions.showEnv(app.id)}
              className="rounded bg-surface-2 px-2 py-0.5 text-xs"
            >
              ⚙️变量
            </button>
            {app.status === "running" && (
              <button
                onClick={() => actions.act(app.id, "stop")}
                className="rounded bg-surface-2 px-2 py-0.5 text-xs"
              >
                停止
              </button>
            )}
            {app.status === "stopped" && (
              <button
                onClick={() => actions.act(app.id, "start")}
                className="rounded bg-success/10 px-2 py-0.5 text-xs text-success"
              >
                启动
              </button>
            )}
            <button
              onClick={() => actions.showReqs(app.id)}
              className="rounded bg-surface-2 px-2 py-0.5 text-xs"
            >
              需求
            </button>
            <button
              onClick={() => actions.showLogs(app.id)}
              className="rounded bg-surface-2 px-2 py-0.5 text-xs"
            >
              日志
            </button>
          </>
        )}
        <button
          onClick={() => actions.remove(app.id)}
          className="rounded bg-danger/10 px-2 py-0.5 text-xs text-danger"
        >
          删除
        </button>
      </div>

      {app.last_error && (
        <div className="mt-1 rounded bg-danger/10 p-1 text-xs text-danger">{app.last_error}</div>
      )}
      {!isExternal && !isImporting && <DevWizard app={app} />}
      {detailErr && (
        <div className="mt-2 rounded bg-danger/10 p-2 text-xs text-danger">
          详情加载失败
          <button onClick={() => loadDetail()} className="ml-2 rounded bg-surface-2 px-2 py-0.5">
            重试
          </button>
        </div>
      )}
      <DetailPanels
        app={app}
        detail={detail}
        appStats={appStats}
        regFor={regFor}
        setRegFor={setRegFor}
        regReq={regReq}
        setRegReq={setRegReq}
        regNote={regNote}
        setRegNote={setRegNote}
        regBusy={regBusy}
        envList={actions.appEnvs}
        envForm={actions.envForm}
        setEnvForm={actions.setEnvForm}
        logs={actions.logs}
        appReqs={actions.appReqs}
        openPanel={actions.openPanel}
        openFor={app.id}
        onApprove={afterDetail(actions.approveChange)}
        onReject={afterDetail(actions.rejectChange)}
        onRelease={afterDetail(actions.releaseChange)}
        onMerge={afterDetail(actions.mergeChange)}
        onRegisterChange={registerChange}
        onDeployCommit={afterDetail(actions.deployCommit)}
        onSaveEnv={actions.saveEnv}
        onRemoveEnv={actions.removeEnv}
      />
      <ArtifactSection psID={psID} appID={app.id} appKind={app.app_kind} />
      {/* external 应用不经 buildAndDeploy→Reconcile 从不调→声明的依赖永不供给，不渲染依赖 section */}
      {app.deploy_mode !== "external" && (
        <>
          <DepsSection psID={psID} appID={app.id} />
          <NetworkModeSection psID={psID} appID={app.id} mode={app.network_mode} />
        </>
      )}
    </div>
  );
}

"use client";

import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { API_BASE_URL } from "@/lib/api";
import type { Detail, Envelope, PS } from "./_lib/types";
import { STATUS_COLOR, isDockerKind } from "./_lib/predicates";
import { useAppData } from "./_lib/use-app-data";
import { useAppActions } from "./_lib/use-app-actions";
import {
  ArtifactSection,
  DepsSection,
  DevWizard,
  NetworkModeSection,
} from "./_components/sections";
import { ImportWizard, type ImportWizardHandle } from "./_components/import-wizard";
import { DetailPanels } from "./_components/detail-panels";

export default function ApplicationsPage() {
  const [spaces, setSpaces] = useState<PS[]>([]);
  const [psID, setPsID] = useState("");
  const [selectedNode, setSelectedNode] = useState(""); // 部署目标节点
  const [form, setForm] = useState({
    name: "",
    internal_port: 8080,
    deploy_mode: "managed" as "managed" | "external",
    external_url: "",
  });
  const [appKind, setAppKind] = useState<string>("web"); // 应用类型 web/desktop/mobile/cli/service/headless
  const [wsTool, setWsTool] = useState("opencode"); // 交互编码工具（开发者可选，不同人选不同）
  // detail 三态暂留壳（T7/T8 迁 tab 面板，本任务不展开态收敛 detail）
  const [detailFor, setDetailFor] = useState<string>("");
  const [detail, setDetail] = useState<Detail | null>(null);
  // 登记变更面板（自由编码产出 → 变更，可选关联需求）：登记输入是面板局部 state，留壳
  const [regFor, setRegFor] = useState<string>("");
  const [regReq, setRegReq] = useState<string>("");
  const [regNote, setRegNote] = useState<string>("");
  const [regBusy, setRegBusy] = useState(false);
  // 导入已有项目向导（自包含组件）：ref 句柄触发 open()，终态 onDone 刷新列表
  const wizRef = useRef<ImportWizardHandle>(null);
  const router = useRouter();

  useEffect(() => {
    fetch(`${API_BASE_URL}/project-spaces`)
      .then((r) => r.json())
      .then((r: Envelope<PS[]>) => {
        setSpaces(r.data ?? []);
        const def = (r.data ?? []).find((s) => s.id === "ps_default") ?? (r.data ?? [])[0];
        if (def) setPsID(def.id);
      });
  }, []);

  // 数据 hook：apps/nodes/appStats/appChanges/deployMsg + 双轮询（自本文件平移，行为零变化）
  const data = useAppData(psID);
  const { apps, nodes, appStats, appChanges, deployMsg, reload, refreshClosedLoop } = data;
  // 动作 hook：展开态（openPanel 收敛单值）+ 部署/启停/变量/日志/需求/闭环操作
  const actions = useAppActions({
    psID,
    apps,
    nodes,
    appChanges,
    deployMsg,
    setDeployMsg: data.setDeployMsg,
    reload,
    loadChanges: data.loadChanges,
    refreshClosedLoop,
    detailFor,
    setDetail,
  });

  async function register() {
    if (!form.name.trim()) return;
    // external 模式只发 name+deploy_mode+external_url；managed 发 name+internal_port(+repo_dir)
    const body =
      form.deploy_mode === "external"
        ? {
            name: form.name,
            deploy_mode: "external",
            external_url: form.external_url.trim(),
            app_kind: appKind,
          }
        : { name: form.name, internal_port: form.internal_port, app_kind: appKind };
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const r = await res.json();
    if (r.code !== 0) {
      alert(r.message);
      return;
    }
    setForm({
      name: "",
      internal_port: form.internal_port,
      deploy_mode: form.deploy_mode,
      external_url: "",
    });
    reload(psID);
  }

  // 跳转到「编码工作台」tab(/workspace):由该页自己调 /workspace 接口拉起 opencode 并全屏加载。
  // 走 tab 系统而非弹窗/页内嵌入——与平台其他功能一致,可在 tab 间切换、不离开平台。
  function openWorkspace(id: string, tool: string) {
    router.push(`/workspace?app=${id}&ps=${psID}&tool=${encodeURIComponent(tool)}`);
  }

  // detail 展开态暂留壳（T7/T8 迁 tab 面板）：toggle 语义与原 page.tsx 原样。
  async function showDetail(id: string) {
    if (detailFor === id) {
      setDetailFor("");
      setDetail(null);
      return;
    }
    setDetailFor(id);
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${id}/detail`);
    const r = await res.json();
    setDetail(r.data ?? null);
  }

  // 登记变更壳层包装：regBusy 与登记输入重置留壳（regReq/regNote 是登记面板局部 state）；
  // 提交与闭环刷新在 actions.registerChange（三参注入，组件经 props 传面板当前输入）。
  async function registerChange(appID: string, reqID: string, note: string): Promise<void> {
    setRegBusy(true);
    try {
      const ok = await actions.registerChange(appID, reqID, note);
      if (ok) {
        setRegFor("");
        setRegReq("");
        setRegNote("");
      }
    } finally {
      setRegBusy(false);
    }
  }

  return (
    <div>
      <div className="mb-4 flex items-center gap-3">
        <h1 className="text-xl font-bold">📦 应用部署</h1>
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
        {nodes.length > 1 && (
          <div className="flex items-center gap-1 text-sm">
            <span className="text-xs text-text-muted">默认部署节点</span>
            <select
              value={selectedNode}
              onChange={(e) => setSelectedNode(e.target.value)}
              className="rounded-md border border-border px-2 py-1 text-sm"
              title={
                isDockerKind(appKind)
                  ? "新增应用的默认部署节点。ssh/winrm 节点为原生部署（需仓库含 deploy.yaml）；docker 形态下 Windows 节点不可选（无 docker 守护进程）"
                  : "新增应用的默认部署节点"
              }
            >
              <option value="">
                本地（{nodes.find((n) => n.id === "node_local")?.host || ".28"}）
              </option>
              {nodes
                .filter((n) => n.id !== "node_local")
                // 全部节点都显示（与服务器管理一致，不再"消失"）；docker 形态下 Windows
                // 不可容器部署的节点用 disabled+标注呈现，用户知道为什么不可选而非找不到。
                .map((n) => {
                  const blocked =
                    isDockerKind(appKind) &&
                    n.os_type === "windows" &&
                    n.connect_type !== "ssh" &&
                    n.connect_type !== "winrm";
                  return (
                    <option key={n.id} value={n.id} disabled={blocked}>
                      {n.name} ({n.host}) · env={n.env || "?"}
                      {n.os_type && n.os_type !== "linux" ? ` · ${n.os_type}` : ""}
                      {n.app_count != null ? ` · ${n.app_count}应用` : ""}
                      {blocked ? " ·不可容器部署" : ""}
                    </option>
                  );
                })}
            </select>
          </div>
        )}
      </div>
      <p className="mb-4 text-sm text-text-muted">
        把研发产出的应用（含 Dockerfile 的源码目录）自动{" "}
        <b>docker build → docker run → 分配端口 → 暴露 URL</b>。 repo_dir 填{" "}
        <b>docker 守护进程可见的路径</b>（生产 .28 上形如 <code>/data/repos/myapp</code>）。
      </p>

      {/* 注册 */}
      <div className="mb-4 flex flex-wrap items-end gap-2 rounded-lg border border-border bg-surface p-3 text-sm">
        <button
          onClick={() => wizRef.current?.open()}
          className="rounded bg-success px-3 py-1.5 text-white"
          title="把已有代码项目（git仓库/zip/服务器目录）导入 ANP 托管，后续走 AI 全流程"
        >
          📥 导入已有项目
        </button>
        <div>
          <label className="block text-xs text-text-muted">接入模式</label>
          <select
            value={form.deploy_mode}
            onChange={(e) =>
              setForm({
                ...form,
                deploy_mode: e.target.value as "managed" | "external",
                external_url: "",
              })
            }
            className="rounded border border-border px-2 py-1"
            title="managed=A 类平台托管(编码/部署);external=B 类纳管已在运行的外部应用(代码不动)"
          >
            <option value="managed">managed · 平台托管（A 类）</option>
            <option value="external">external · 纳管外部应用（B 类）</option>
          </select>
        </div>
        <div>
          <label className="block text-xs text-text-muted">应用类型</label>
          <select
            value={appKind}
            onChange={(e) => setAppKind(e.target.value)}
            className="rounded border border-border px-2 py-1"
            title="应用产物形态：web/service 走容器部署；desktop/mobile/cli 走构建产物下载"
          >
            <option value="web">Web 应用</option>
            <option value="desktop">桌面应用</option>
            <option value="mobile">移动应用</option>
            <option value="cli">命令行工具</option>
            <option value="service">后端服务</option>
            <option value="headless">headless（无端口进程）</option>
          </select>
        </div>
        <div>
          <label className="block text-xs text-text-muted">应用名</label>
          <input
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
            placeholder={form.deploy_mode === "external" ? "如 存量ERP" : "如 hello-go"}
            className="rounded border border-border px-2 py-1"
          />
        </div>
        {form.deploy_mode === "external" ? (
          <div className="min-w-[16rem] flex-1">
            <label className="block text-xs text-text-muted">
              外部应用地址 external_url（必填）
            </label>
            <input
              value={form.external_url}
              onChange={(e) => setForm({ ...form, external_url: e.target.value })}
              placeholder="http://host:port 或 https://domain/path"
              className="w-full rounded border border-border px-2 py-1"
            />
          </div>
        ) : (
          <div>
            <label className="block text-xs text-text-muted">容器内端口（可选）</label>
            <input
              type="number"
              value={form.internal_port}
              onChange={(e) => setForm({ ...form, internal_port: Number(e.target.value) })}
              className="w-24 rounded border border-border px-2 py-1"
            />
          </div>
        )}
        <button onClick={register} className="rounded bg-accent px-3 py-1.5 text-white">
          {form.deploy_mode === "external" ? "接入外部应用" : "创建应用"}
        </button>
        <span className="text-xs text-text-muted">
          {form.deploy_mode === "external"
            ? "B 类轻接入：仅注册 + appgw 统一入口 /apps/&lt;app_id&gt;/ + ops 按 external_url 探活。不动外部代码。"
            : "仓库自动托管到 /data/repos/<应用名>（git），opencode 编码即提交到此"}
        </span>
      </div>

      {/* 导入已有项目向导（自包含组件）：3 步 ①选来源 ②填信息 ③执行/进度；终态 onDone 刷新列表 */}
      <ImportWizard ref={wizRef} psID={psID} onDone={() => reload(psID)} />

      {/* 应用列表 */}
      <div className="space-y-3">
        {apps.map((a) => {
          const isExternal = a.deploy_mode === "external";
          // 导入进行中：status===importing（git clone / zip 解压 / 目录复制中），编码/部署按钮禁用
          const isImporting = a.status === "importing";
          return (
            <div key={a.id} className="rounded-lg border border-border bg-surface p-3">
              {/* 导入进度提示：importing 态显示 last_error 进度（后端实时回写「正在 clone...」等） */}
              {isImporting && (
                <div className="mb-2 flex items-center gap-2 rounded bg-warn/10 px-3 py-1.5 text-sm text-warn">
                  <span className="inline-block h-3 w-3 animate-spin rounded-full border-2 border-warn border-t-transparent"></span>
                  导入已有项目中...
                  <span className="truncate text-xs text-warn">
                    {a.last_error || "(等待进度...)"}
                  </span>
                  <span className="ml-auto text-xs text-warn">每3秒自动刷新</span>
                </div>
              )}
              {/* 部署进度提示：从 app.status 派生（切 tab 回来也可见，因为 3s 轮询刷新 status） */}
              {(a.status === "building" || a.status === "preparing") && (
                <div className="mb-2 flex items-center gap-2 rounded bg-accent/10 px-3 py-1.5 text-sm text-accent">
                  <span className="inline-block h-3 w-3 animate-spin rounded-full border-2 border-accent border-t-transparent"></span>
                  {a.status === "preparing" ? "AI 部署中（简报→执行→验证）..." : "构建部署中..."}
                  {a.instances?.find(
                    (i) => i.status === "building" || i.status === "preparing"
                  ) && (
                    <span className="text-xs text-accent">
                      {a.instances.find((i) => i.status === "building" || i.status === "preparing")
                        ?.url || ""}
                    </span>
                  )}
                  <span className="ml-auto text-xs text-accent">每3秒自动刷新</span>
                </div>
              )}
              {a.status === "running" && a.instances?.some((i) => i.status === "building") && (
                <div className="mb-2 flex items-center gap-2 rounded bg-accent/10 px-3 py-1.5 text-sm text-accent">
                  <span className="inline-block h-3 w-3 animate-spin rounded-full border-2 border-accent border-t-transparent"></span>
                  {a.instances.find((i) => i.status === "building")?.env} 环境构建中...
                  <span className="ml-auto text-xs text-accent">每3秒自动刷新</span>
                </div>
              )}
              {a.status === "failed" && (
                <div className="mb-2 rounded bg-danger/10 px-3 py-1.5 text-sm text-danger">
                  <div className="flex items-center gap-2">
                    <span className="flex-1">
                      ❌ 构建失败：{a.last_error?.slice(0, 100) || "(无错误摘要)"}
                    </span>
                    <button
                      onClick={() =>
                        actions.act(
                          a.id,
                          "deploy",
                          a.instances
                            ?.filter((i) => i.status === "failed")
                            .sort((x, y) =>
                              (y.updated_at || "").localeCompare(x.updated_at || "")
                            )[0]?.env || "test",
                          selectedNode,
                          "fixed"
                        )
                      }
                      className="shrink-0 rounded bg-surface-2 px-2 py-0.5 text-xs text-text"
                      title="放弃 AI 引擎，用固定部署引擎重试本次构建部署（spec §5：失败不静默降级，由人工选择）"
                    >
                      🔧 用固定引擎重试
                    </button>
                  </div>
                  {a.build_log && (
                    <details className="mt-1">
                      <summary className="cursor-pointer text-xs text-danger">
                        查看构建日志详情
                      </summary>
                      <pre className="mt-1 max-h-64 overflow-auto rounded bg-neutral-900 p-2 text-xs text-green-300 whitespace-pre-wrap">
                        {a.build_log}
                      </pre>
                    </details>
                  )}
                </div>
              )}
              {!isExternal && !isImporting && <DevWizard app={a} />}
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-mono font-medium">{a.name}</span>
                {isExternal ? (
                  <span className="rounded bg-accent/10 px-1.5 py-0.5 text-xs text-accent">
                    external · 纳管
                  </span>
                ) : null}
                {/* 导入来源徽章：git=远程仓 / dir=本机zip或服务器目录；空=平台建仓（不显示） */}
                {a.import_source === "git" && (
                  <span
                    className="rounded bg-success/10 px-1.5 py-0.5 text-xs text-success"
                    title={"导入自 git 仓库: " + (a.import_ref || "")}
                  >
                    📥 git
                  </span>
                )}
                {a.import_source === "dir" && (
                  <span
                    className="rounded bg-warn/10 px-1.5 py-0.5 text-xs text-warn"
                    title={"导入自 目录/zip: " + (a.import_ref || "")}
                  >
                    📁 目录
                  </span>
                )}
                <span
                  className={`rounded px-1.5 py-0.5 text-xs ${STATUS_COLOR[a.status] ?? "bg-surface-2"}`}
                >
                  {a.status}
                </span>
                {/* 构建进度条 */}
                {(a.status === "building" || a.status === "preparing" || a.status === "running") &&
                  a.instances &&
                  a.instances.some((i) => i.status === "building" || i.status === "preparing") && (
                    <span className="flex items-center gap-1 text-xs text-warn">
                      <span className="inline-block h-3 w-3 animate-spin rounded-full border-2 border-warn border-t-transparent"></span>
                      构建中...
                    </span>
                  )}
                {(a.status === "building" || a.status === "preparing") &&
                  a.instances &&
                  a.instances.some((i) => i.status === "failed") && (
                    <span className="text-xs text-danger">构建失败</span>
                  )}
                {!isExternal && a.image && (
                  <span className="text-xs text-text-muted">
                    v{a.version} · {a.image}
                  </span>
                )}
                <div className="ml-auto flex gap-1">
                  {isExternal ? (
                    <>
                      <a
                        href={a.external_url}
                        target="_blank"
                        rel="noreferrer"
                        className="rounded bg-accent/10 px-2 py-0.5 text-xs text-accent"
                        title="打开外部应用地址"
                      >
                        🔗 访问
                      </a>
                      <button
                        onClick={() => showDetail(a.id)}
                        className="rounded bg-surface-2 px-2 py-0.5 text-xs"
                      >
                        详情
                      </button>
                    </>
                  ) : (
                    <>
                      {/* 容器部署按钮（构建部署/上线 prod）仅 web/service/headless 形态显示：
                          desktop/mobile/cli 走 ArtifactSection 的「构建产物」流程，
                          无容器可部署，点了会 docker build 失败（I-4）。 */}
                      {(a.app_kind === "web" ||
                        a.app_kind === "service" ||
                        a.app_kind === "headless" ||
                        !a.app_kind) && (
                        <>
                          <button
                            onClick={() => actions.act(a.id, "deploy", "test", selectedNode)}
                            className="rounded bg-accent/10 px-2 py-0.5 text-xs text-accent disabled:cursor-not-allowed disabled:opacity-40"
                            disabled={isImporting}
                            title={
                              isImporting
                                ? "导入完成前不可部署"
                                : "从主仓(master)代码构建部署到 test 环境——不含编码工作台 dev 分支未合并的改动；要部署 AI 最新代码请到编码工作台点「构建部署」"
                            }
                          >
                            构建部署(test·master)
                          </button>
                          <button
                            onClick={() => actions.promoteWithNode(a.id, selectedNode)}
                            className="rounded bg-success/10 px-2 py-0.5 text-xs text-success disabled:cursor-not-allowed disabled:opacity-40"
                            disabled={isImporting}
                            title={isImporting ? "导入完成前不可上线" : "上线到 prod"}
                          >
                            🚀 上线(prod)
                          </button>
                        </>
                      )}
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
                        onClick={() => openWorkspace(a.id, wsTool)}
                        className="rounded bg-warn/10 px-2 py-0.5 text-xs text-warn disabled:cursor-not-allowed disabled:opacity-40"
                        disabled={isImporting}
                        title={
                          isImporting
                            ? "导入完成前不可编码（仓库尚未就绪）"
                            : "打开该工具的官方交互编码界面"
                        }
                      >
                        🧑‍💻 编码
                      </button>
                      <button
                        onClick={() => actions.showEnv(a.id)}
                        className="rounded bg-surface-2 px-2 py-0.5 text-xs"
                      >
                        ⚙️变量
                      </button>
                      {a.status === "running" && (
                        <button
                          onClick={() => actions.act(a.id, "stop")}
                          className="rounded bg-surface-2 px-2 py-0.5 text-xs"
                        >
                          停止
                        </button>
                      )}
                      {a.status === "stopped" && (
                        <button
                          onClick={() => actions.act(a.id, "start")}
                          className="rounded bg-success/10 px-2 py-0.5 text-xs text-success"
                        >
                          启动
                        </button>
                      )}
                      <button
                        onClick={() => actions.showReqs(a.id)}
                        className="rounded bg-surface-2 px-2 py-0.5 text-xs"
                      >
                        需求
                      </button>
                      <button
                        onClick={() => showDetail(a.id)}
                        className="rounded bg-surface-2 px-2 py-0.5 text-xs"
                      >
                        详情
                      </button>
                      <button
                        onClick={() => actions.showLogs(a.id)}
                        className="rounded bg-surface-2 px-2 py-0.5 text-xs"
                      >
                        日志
                      </button>
                    </>
                  )}
                  <button
                    onClick={() => actions.remove(a.id)}
                    className="rounded bg-danger/10 px-2 py-0.5 text-xs text-danger"
                  >
                    删除
                  </button>
                </div>
              </div>
              <div className="mt-1 text-xs text-text-muted">
                {isExternal ? (
                  <>
                    external_url:{" "}
                    <a
                      href={a.external_url}
                      target="_blank"
                      rel="noreferrer"
                      className="text-accent hover:underline"
                    >
                      {a.external_url}
                    </a>{" "}
                    · 统一入口 <code>/apps/{a.id}/</code>
                  </>
                ) : (
                  <>
                    repo: <code>{a.repo_dir}</code> · 内部端口 {a.internal_port}
                    {a.host_port ? ` · 宿主端口 ${a.host_port}` : ""}
                  </>
                )}
              </div>
              {a.updated_at && (
                <div className="text-xs text-text-muted">
                  {a.status === "running" ? "部署于" : "更新于"}：
                  {new Date(a.updated_at).toLocaleString("zh-CN", { hour12: false })}
                </div>
              )}
              {isExternal ? (
                // external 应用探活展示：无容器/test-prod 实例，只显示 external_url 健康状态
                appStats[a.id]?.deployed ? (
                  <div className="mt-2 rounded bg-accent/10 p-2 text-xs">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="rounded bg-accent/10 px-1.5 py-0.5 font-medium text-accent">
                        external 探活
                      </span>
                      <span className="text-text-muted">健康</span>
                      <span
                        className={
                          (appStats[a.id].health === "up" ? "text-success" : "text-danger") +
                          " font-medium"
                        }
                      >
                        {appStats[a.id].health}
                      </span>
                    </div>
                  </div>
                ) : null
              ) : (
                <div className="mt-2 grid grid-cols-1 gap-2 sm:grid-cols-2">
                  {(["test", "prod"] as const).map((env) => {
                    const ins = a.instances?.find((i) => i.env === env);
                    const label = env === "prod" ? "🚀 生产 prod" : "🧪 测试 test";
                    return (
                      <div key={env} className="rounded bg-bg p-2 text-xs">
                        <div className="flex items-center gap-2">
                          <span
                            className={`rounded px-1.5 py-0.5 font-medium ${env === "prod" ? "bg-accent/10 text-accent" : "bg-warn/10 text-warn"}`}
                          >
                            {label}
                          </span>
                          {ins && (
                            <span
                              className={`rounded px-1.5 py-0.5 ${STATUS_COLOR[ins.status] ?? "bg-surface-2"}`}
                            >
                              {ins.status}
                            </span>
                          )}
                          {ins && ins.version > 0 && (
                            <span className="text-text-muted">v{ins.version}</span>
                          )}
                        </div>
                        {ins?.url ? (
                          <a
                            href={ins.url}
                            target="_blank"
                            rel="noreferrer"
                            className="mt-1 block truncate text-accent hover:underline"
                          >
                            {ins.url}
                          </a>
                        ) : (
                          <div className="mt-1 text-text-muted">
                            {env === "prod"
                              ? "未上线（点「上线」部署）"
                              : "未部署（发布或「构建部署」）"}
                          </div>
                        )}
                        {env === "prod" && appStats[a.id]?.deployed && (
                          <div className="mt-1 flex flex-wrap items-center gap-2 text-[11px]">
                            <span className="text-text-muted">健康</span>
                            <span
                              className={
                                (appStats[a.id].health === "up" ? "text-success" : "text-danger") +
                                " font-medium"
                              }
                            >
                              {appStats[a.id].health}
                            </span>
                            {appStats[a.id].cpu && (
                              <span className="text-text-muted">
                                CPU {appStats[a.id].cpu} · 内存 {appStats[a.id].mem}
                              </span>
                            )}
                          </div>
                        )}
                        {env === "prod" &&
                          (appChanges[a.id] || []).filter((c) => c.status === "approved").length >
                            0 && (
                            <div className="mt-1 rounded bg-warn/10 p-1 text-[11px]">
                              <div className="font-medium text-warn">
                                📋 待上线变更(
                                {
                                  (appChanges[a.id] || []).filter((c) => c.status === "approved")
                                    .length
                                }
                                )：
                              </div>
                              {(appChanges[a.id] || [])
                                .filter((c) => c.status === "approved")
                                .map((c) => (
                                  <div key={c.id} className="truncate text-warn">
                                    {(
                                      (c.output || "").match(/【总结】(.+)/)?.[1] ||
                                      c.id.slice(0, 12)
                                    ).slice(0, 50)}
                                    {c.created_at && (
                                      <span className="ml-1 text-[10px] text-text-muted">
                                        {new Date(c.created_at).toLocaleString("zh-CN", {
                                          hour12: false,
                                          month: "2-digit",
                                          day: "2-digit",
                                          hour: "2-digit",
                                          minute: "2-digit",
                                        })}
                                      </span>
                                    )}
                                  </div>
                                ))}
                            </div>
                          )}
                      </div>
                    );
                  })}
                </div>
              )}
              {a.last_error && (
                <div className="mt-1 rounded bg-danger/10 p-1 text-xs text-danger">
                  {a.last_error}
                </div>
              )}
              <DetailPanels
                app={a}
                detail={detailFor === a.id ? detail : null}
                appStats={appStats[a.id]}
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
                openFor={actions.openFor}
                onApprove={actions.approveChange}
                onReject={actions.rejectChange}
                onRelease={actions.releaseChange}
                onMerge={actions.mergeChange}
                onRegisterChange={registerChange}
                onDeployCommit={actions.deployCommit}
                onSaveEnv={actions.saveEnv}
                onRemoveEnv={actions.removeEnv}
              />
              <ArtifactSection psID={psID} appID={a.id} appKind={a.app_kind} />
              {/* external 应用不经 buildAndDeploy→Reconcile 从不调→声明的依赖永不供给，
                  不渲染依赖 section，避免误导性的「下次部署生效」hint。 */}
              {a.deploy_mode !== "external" && (
                <>
                  <DepsSection psID={psID} appID={a.id} />
                  <NetworkModeSection psID={psID} appID={a.id} mode={a.network_mode} />
                </>
              )}
            </div>
          );
        })}
        {apps.length === 0 && (
          <div className="text-sm text-text-muted">
            暂无应用。注册一个（源码目录需含 Dockerfile）后点「构建部署」，或点「📥
            导入已有项目」把现有代码项目（git仓库/zip/服务器目录）导入平台，或用 external
            模式接入已在运行的外部应用。
          </div>
        )}
      </div>

      <div className="mt-4 rounded-md bg-warn/10 p-2 text-xs text-warn">
        说明：构建部署在 ANP 后端容器内经宿主 docker socket 执行。repo_dir 必须是
        <b>后端容器内可见</b>的路径（产出应用默认在 <code>/data/repos/&lt;应用名&gt;</code>
        ，对应宿主 <code>/opt/anp/data/repos/...</code>）。端口自动从 9100-9300 分配。
      </div>
    </div>
  );
}

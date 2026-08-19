"use client";

// /applications 页面壳（T9 收口，spec §2）：本文件只管页面级状态与编排，应用内容
// 全部由 _components 承接。结构（自上而下）：
//   ① 工具条（标题+空间选择+节点选择+说明） ② 注册表单 ③ ImportWizard
//   ④ OverviewBar 总览条（一格=一应用，点击开/切 tab）
//   ⑤ tab 页签行（名字+✕；click=selectTab、✕=closeTab）
//   ⑥ 工作区（activeApp ? AppTabPanel : 空态） ⑦ 底部说明卡
// tab 态走 _lib/app-tabs 纯函数状态机；detail/登记输入等面板局部态全部在 AppTabPanel 内。
import { useEffect, useRef, useState } from "react";
import { API_BASE_URL } from "@/lib/api";
import type { Envelope, PS } from "./_lib/types";
import { isDockerKind } from "./_lib/predicates";
import {
  closeTab,
  openTab,
  pruneTabs,
  selectTab,
  EMPTY_TABS,
  type TabState,
} from "./_lib/app-tabs";
import { buildOverviewCells } from "./_lib/overview";
import { useAppData } from "./_lib/use-app-data";
import { useAppActions } from "./_lib/use-app-actions";
import { ImportWizard, type ImportWizardHandle } from "./_components/import-wizard";
import { OverviewBar } from "./_components/overview-bar";
import { AppTabPanel } from "./_components/app-tab-panel";

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
  // tab 态：ids=已打开应用，activeId=当前展示（app-tabs 状态机管转移语义）
  const [tabs, setTabs] = useState<TabState>(EMPTY_TABS);
  // 导入已有项目向导（自包含组件）：ref 句柄触发 open()，终态 onDone 刷新列表
  const wizRef = useRef<ImportWizardHandle>(null);

  useEffect(() => {
    fetch(`${API_BASE_URL}/project-spaces`)
      .then((r) => r.json())
      .then((r: Envelope<PS[]>) => {
        setSpaces(r.data ?? []);
        const def = (r.data ?? []).find((s) => s.id === "ps_default") ?? (r.data ?? [])[0];
        if (def) setPsID(def.id);
      });
  }, []);

  // 数据 hook：apps/nodes/appStats/appChanges/deployMsg + 双轮询
  const data = useAppData(psID);
  // 动作 hook：面板展开态 + 部署/启停/变量/日志/需求/闭环操作。
  // detailFor 注入 ""（收口①）：hook 内 refreshClosedLoop 的 detail 分支永不触发，
  // detail 刷新由 AppTabPanel 的 afterDetail 单通道负责——闭环操作无双 detail 请求。
  const actions = useAppActions({
    psID,
    apps: data.apps,
    nodes: data.nodes,
    appChanges: data.appChanges,
    deployMsg: data.deployMsg,
    setDeployMsg: data.setDeployMsg,
    reload: data.reload,
    loadChanges: data.loadChanges,
    refreshClosedLoop: data.refreshClosedLoop,
    detailFor: "",
    setDetail: () => undefined, // 占位：detail 态在面板内自管（见上注）
  });
  const activeApp = data.apps.find((a) => a.id === tabs.activeId);

  // 应用删除后清 tab：pruneTabs 无变化时返回原引用 → setState 不触发重渲染，
  // data.apps 每 3s 轮询都是新数组也安全（deps 只决定何时检查，不引发额外渲染）。
  useEffect(() => {
    setTabs((t) =>
      pruneTabs(
        t,
        data.apps.map((a) => a.id)
      )
    );
  }, [data.apps]);

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
    data.reload(psID);
  }

  return (
    <div>
      {/* ① 工具条：标题+空间选择+节点选择 */}
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
        {data.nodes.length > 1 && (
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
                本地（{data.nodes.find((n) => n.id === "node_local")?.host || ".28"}）
              </option>
              {data.nodes
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

      {/* ② 注册 */}
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

      {/* ③ 导入已有项目向导：3 步 ①选来源 ②填信息 ③执行/进度；终态 onDone 刷新列表 */}
      <ImportWizard ref={wizRef} psID={psID} onDone={() => data.reload(psID)} />

      {/* ④ 总览条：每应用一格（状态+环境迷你点）；点击=开 tab（已开=激活） */}
      <OverviewBar
        cells={buildOverviewCells(data.apps)}
        activeId={tabs.activeId}
        onSelect={(id) => setTabs((t) => openTab(t, id))}
      />

      {/* ⑤ tab 页签行：已打开应用一行式小 tab 头（名字=选中，✕=关闭） */}
      {tabs.ids.length > 0 && (
        <div className="mb-3 flex items-center gap-1 overflow-x-auto" data-testid="tab-row">
          {tabs.ids.map((id) => {
            const name = data.apps.find((a) => a.id === id)?.name ?? id;
            const active = id === tabs.activeId;
            return (
              <div
                key={id}
                className={`flex shrink-0 items-center gap-1 rounded-t-md border border-b-0 px-2 py-1 text-xs ${
                  active ? "border-accent bg-surface font-medium" : "border-border bg-surface-2"
                }`}
              >
                <button
                  onClick={() => setTabs((t) => selectTab(t, id))}
                  className="max-w-40 truncate"
                  title="切换到此应用 tab"
                >
                  {name}
                </button>
                <button
                  onClick={() => setTabs((t) => closeTab(t, id))}
                  className="rounded px-1 text-text-muted hover:text-danger"
                  title="关闭 tab"
                >
                  ✕
                </button>
              </div>
            );
          })}
        </div>
      )}

      {/* ⑥ 工作区：激活应用面板；未选给引导空态 */}
      {activeApp ? (
        <AppTabPanel
          app={activeApp}
          psID={psID}
          selectedNode={selectedNode}
          wsTool={wsTool}
          setWsTool={setWsTool}
          appStats={data.appStats[activeApp.id]}
          appChanges={data.appChanges[activeApp.id] || []}
          data={data}
          actions={actions}
          onClose={() => setTabs((t) => closeTab(t, activeApp.id))}
        />
      ) : data.apps.length === 0 ? (
        <div className="text-sm text-text-muted">
          暂无应用。注册一个（源码目录需含 Dockerfile）后点「构建部署」，或点「📥
          导入已有项目」把现有代码项目（git仓库/zip/服务器目录）导入平台，或用 external
          模式接入已在运行的外部应用。
        </div>
      ) : (
        <div className="text-sm text-text-muted">点击上方应用格子打开工作区。</div>
      )}

      {/* ⑦ 底部说明卡 */}
      <div className="mt-4 rounded-md bg-warn/10 p-2 text-xs text-warn">
        说明：构建部署在 ANP 后端容器内经宿主 docker socket 执行。repo_dir 必须是
        <b>后端容器内可见</b>的路径（产出应用默认在 <code>/data/repos/&lt;应用名&gt;</code>
        ，对应宿主 <code>/opt/anp/data/repos/...</code>）。端口自动从 9100-9300 分配。
      </div>
    </div>
  );
}

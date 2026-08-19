"use client";

// 动作 hook（自 page.tsx 平移，函数体逐行等价）：部署/启停/变量/日志/需求/闭环操作。
// 展开：
// - 原 logsFor/reqsFor/envFor/detailFor 四组 per-app 展开态，tab 化后同一时刻只有一个应用
//   面板存在 → 收敛为单值 openPanel: "logs"|"reqs"|"env"|"detail"|""。toggle 语义（原
//   showReqs/showEnv 的「同 id 再点关闭」统一化）：同 key 再点关闭；跨应用切换不再复用
//   展开态——壳在切 tab 时调 resetPanels()（面板级数据归属门 openFor 恒真，壳级 state
//   必须随切换清空，否则 B 面板会渲染 A 的 logs/reqs/envs）。
// - openFor 已删（T9 收口）：tab 面板化后同一时刻只有一个应用面板，面板 JSX 恒传
//   openFor={app.id} 字面量 → hook 内 toggle 归属判断退化为 openPanel 单值比较（行为等价）。
// - detail 三态（detail/detailFor/showDetail）不进本 hook（已迁 T8 tab 面板）；闭环函数经
//   deps.refreshClosedLoop(appID, detailFor, setDetail) 注入刷新——壳注入 detailFor:""
//   使 detail 分支永不触发（detail 由面板 afterDetail 单通道刷新，无双请求），
//   setDetail 仅为满足签名的占位。
// - 登记输入（regFor/regReq/regNote/regBusy）是登记面板局部 state，留在壳；
//   registerChange 收三参并返回成功与否，重置输入由调用方按返回值处理。
import { useState, type Dispatch, type SetStateAction } from "react";
import { API_BASE_URL } from "@/lib/api";
import { toast } from "@/lib/toast";
import type { App, ChangeSummary, Detail, EnvVar, NodeInfo, Req } from "./types";
import { isDockerKind, nodeMatchesEnv } from "./predicates";

export type ExpandedKey = "logs" | "reqs" | "env" | "detail";

export function useAppActions(deps: {
  psID: string;
  apps: App[];
  nodes: NodeInfo[];
  appChanges: Record<string, ChangeSummary[]>;
  deployMsg: Record<string, string>;
  setDeployMsg: Dispatch<SetStateAction<Record<string, string>>>;
  reload: (id: string) => void;
  loadChanges: (id: string) => void;
  refreshClosedLoop: (
    appID: string,
    detailFor: string,
    setDetail: (d: Detail | null) => void
  ) => void;
  detailFor: string;
  setDetail: (d: Detail | null) => void;
}) {
  // 展开态（原 logsFor/reqsFor/envFor/detailFor 四组收敛，见文件头说明）
  const [openPanel, setOpenPanel] = useState<ExpandedKey | "">("");
  const [logs, setLogs] = useState("");
  const [appReqs, setAppReqs] = useState<Req[]>([]);
  const [appEnvs, setAppEnvs] = useState<EnvVar[]>([]);
  const [envForm, setEnvForm] = useState({ key: "", value: "", is_secret: false });

  // 上线 prod（带节点 + 变更闸门检查）
  async function promoteWithNode(id: string, nodeID: string) {
    // 节点环境校验：prod 部署需 env=prod 节点（node_local 豁免，始终可用）。
    const sel = deps.nodes.find((n) => n.id === nodeID);
    if (!nodeMatchesEnv(sel, "prod")) {
      alert(`上线 prod 需选择 env=prod 的节点（当前为 env=${sel?.env ?? "?"}），或用本地节点。`);
      return;
    }
    const chgs = (deps.appChanges[id] || []).filter((c) => c.status === "approved");
    if (chgs.length > 0) {
      const summaries = chgs
        .map(
          (c) =>
            "• " + ((c.output || "").match(/【总结】(.+)/)?.[1] || c.id.slice(0, 12)).slice(0, 60)
        )
        .join("\n");
      if (!confirm(`本次上线将部署以下 ${chgs.length} 个已审批变更：\n${summaries}\n\n确认上线？`))
        return;
    }
    const body: Record<string, string> = {};
    if (nodeID) body.node_id = nodeID;
    // 部署权限分离：上线统一走 /promote（带变更闸门 + prod 鉴权），不再绕道 /deploy env=prod
    const res = await fetch(`${API_BASE_URL}/project-spaces/${deps.psID}/apps/${id}/promote`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const r = await res.json();
    if (r.code !== 0) alert(r.message);
    deps.reload(deps.psID);
    deps.loadChanges(id);
  }

  async function act(
    id: string,
    action: "deploy" | "stop" | "start",
    env?: string,
    nodeID?: string,
    engine?: "fixed" | "ai"
  ) {
    const body: Record<string, string> = {};
    if (action === "deploy") {
      if (env) body.env = env;
      if (nodeID) body.node_id = nodeID;
      if (engine) body.engine = engine;
      // 节点环境校验：部署 env=test 只能用 env=test 节点；env=prod 只能用 env=prod 节点。
      // node_local 豁免（始终可用）。Windows 节点：ssh/winrm 走原生部署（支持 Windows，
      // 由后端校验 deploy.yaml 并显式报错）；仅非 ssh/winrm 的 Windows 节点（无 docker
      // 守护进程）才在此拦截。
      const sel = deps.nodes.find((n) => n.id === nodeID);
      const app = deps.apps.find((a) => a.id === id);
      if (env && !nodeMatchesEnv(sel, env)) {
        alert(
          `部署 ${env} 环境需选择 env=${env} 的节点（当前为 env=${sel?.env ?? "?"}），或用本地节点。`
        );
        return;
      }
      if (
        sel &&
        app &&
        isDockerKind(app.app_kind) &&
        sel.os_type === "windows" &&
        sel.connect_type !== "ssh" &&
        sel.connect_type !== "winrm"
      ) {
        alert("该 Windows 节点非 ssh/winrm 连接，无 docker 守护进程，不可部署容器应用。");
        return;
      }
    } else {
      // stop/start：显式带 env（默认 prod；后端按 env 鉴权，dev 无 prod 权限会被 403）
      body.env = env || "prod";
    }
    // 进度提示
    if (action === "deploy") {
      deps.setDeployMsg((prev) => ({
        ...prev,
        [id]: `⏳ 构建部署 ${env} ${nodeID ? "(" + (deps.nodes.find((n) => n.id === nodeID)?.name || nodeID) + ")" : ""}`,
      }));
    }
    const res = await fetch(`${API_BASE_URL}/project-spaces/${deps.psID}/apps/${id}/${action}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const r = await res.json();
    if (r.code !== 0) {
      alert(r.message);
      deps.setDeployMsg((prev) => {
        const n = { ...prev };
        delete n[id];
        return n;
      });
    }
    deps.reload(deps.psID);
  }
  // 语义化薄别名：启停按钮用（= act(id, action)，默认 env=prod）
  const stopOrStart = act;

  async function reloadEnv(id: string) {
    const res = await fetch(`${API_BASE_URL}/project-spaces/${deps.psID}/apps/${id}/env`);
    const r = await res.json();
    setAppEnvs(r.data ?? []);
  }
  async function showEnv(id: string) {
    if (openPanel === "env") {
      setOpenPanel("");
      return;
    }
    setOpenPanel("env");
    await reloadEnv(id);
  }
  async function saveEnv(id: string) {
    if (!envForm.key.trim()) return;
    await fetch(`${API_BASE_URL}/project-spaces/${deps.psID}/apps/${id}/env`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(envForm),
    });
    setEnvForm({ key: "", value: "", is_secret: false });
    reloadEnv(id);
  }
  async function removeEnv(id: string, key: string) {
    await fetch(
      `${API_BASE_URL}/project-spaces/${deps.psID}/apps/${id}/env/${encodeURIComponent(key)}`,
      { method: "DELETE" }
    );
    reloadEnv(id);
  }
  async function remove(id: string) {
    if (!confirm("删除应用（含容器）？")) return;
    await fetch(`${API_BASE_URL}/project-spaces/${deps.psID}/apps/${id}`, { method: "DELETE" });
    deps.reload(deps.psID);
  }
  async function showLogs(id: string) {
    if (openPanel === "logs") {
      setOpenPanel("");
      return;
    }
    setOpenPanel("logs");
    const res = await fetch(`${API_BASE_URL}/project-spaces/${deps.psID}/apps/${id}/logs`);
    const r = await res.json();
    setLogs(r.data?.logs ?? "(无)");
  }
  async function showReqs(id: string) {
    if (openPanel === "reqs") {
      setOpenPanel("");
      return;
    }
    setOpenPanel("reqs");
    const res = await fetch(`${API_BASE_URL}/project-spaces/${deps.psID}/apps/${id}/requirements`);
    const r = await res.json();
    setAppReqs(r.data ?? []);
  }
  // 切 tab 清子面板（T9 评审 Important #1 修复）：openPanel/logs/appReqs/appEnvs 是壳级
  // state（页面单份实例），openFor 归属门恒真拦不住跨应用残留——B 面板会直接渲染 A 的
  // 日志/需求/变量。壳在「tabs.activeId 将变」处调用（面板另有 key=app.id 重挂，清局部
  // detail/登记态；本函数只管壳级残留数据态）。
  function resetPanels() {
    setOpenPanel("");
    setLogs("");
    setAppReqs([]);
    setAppEnvs([]);
  }
  async function approveChange(appID: string, chgID: string) {
    const r = await fetch(`${API_BASE_URL}/changes/${chgID}/approve`, { method: "POST" }).then(
      (rr) => rr.json()
    );
    if (r.code !== 0) alert(r.message);
    deps.refreshClosedLoop(appID, deps.detailFor, deps.setDetail);
  }
  async function rejectChange(appID: string, chgID: string) {
    const r = await fetch(`${API_BASE_URL}/changes/${chgID}/reject`, { method: "POST" }).then(
      (rr) => rr.json()
    );
    if (r.code !== 0) alert(r.message);
    deps.refreshClosedLoop(appID, deps.detailFor, deps.setDetail);
  }
  async function releaseChange(appID: string, chgID: string) {
    const r = await fetch(`${API_BASE_URL}/project-spaces/${deps.psID}/releases`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ change_id: chgID }),
    }).then((rr) => rr.json());
    if (r.code !== 0) {
      alert(r.message);
      return;
    }
    toast.success(`已发布上线 v${r.data?.version ?? ""}`);
    deps.refreshClosedLoop(appID, deps.detailFor, deps.setDetail);
    deps.reload(deps.psID);
  }
  async function mergeChange(appID: string, chgID: string, reqID?: string) {
    const r = await fetch(`${API_BASE_URL}/project-spaces/${deps.psID}/apps/${appID}/merge`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ req_id: reqID || undefined }),
    }).then((rr) => rr.json());
    if (r.code !== 0) {
      alert(r.message);
      return;
    }
    toast.success("已合并到 main（关联需求标 delivered）");
    deps.refreshClosedLoop(appID, deps.detailFor, deps.setDetail);
  }
  // 登记变更：把交互编码产出（opencode 对话 + git diff）登记为待审批变更；
  // 可选关联需求（req_id）→ change.source_id 收敛到需求，release 回写 delivered 非 0 行。
  // 登记输入（reqID/note）由调用方传入；返回是否成功，成功后调用方重置输入面板。
  // regBusy 态留在壳（原 try/catch/alert 错误处理原样保留在 hook 内）。
  async function registerChange(appID: string, reqID: string, note: string): Promise<boolean> {
    try {
      const r = await fetch(
        `${API_BASE_URL}/project-spaces/${deps.psID}/apps/${appID}/register-change`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ req_id: reqID || undefined, note: note || undefined }),
        }
      ).then((rr) => rr.json());
      if (r.code !== 0) {
        alert(r.message);
        return false;
      }
      toast.success("已登记变更（AI 已总结编码产出）");
      deps.refreshClosedLoop(appID, deps.detailFor, deps.setDetail);
      return true;
    } catch (e) {
      alert(String(e));
      return false;
    }
  }
  async function deployCommit(appID: string, sha: string) {
    const res = await fetch(
      `${API_BASE_URL}/project-spaces/${deps.psID}/apps/${appID}/deploy-commit`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ sha }),
      }
    );
    const r = await res.json();
    if (r.code !== 0) alert(r.message);
    deps.reload(deps.psID);
  }

  return {
    openPanel,
    setOpenPanel,
    logs,
    setLogs,
    appReqs,
    setAppReqs,
    appEnvs,
    setAppEnvs,
    envForm,
    setEnvForm,
    deployMsg: deps.deployMsg,
    act,
    stopOrStart,
    promoteWithNode,
    resetPanels,
    saveEnv,
    removeEnv,
    remove,
    showLogs,
    showReqs,
    showEnv,
    approveChange,
    rejectChange,
    releaseChange,
    mergeChange,
    registerChange,
    deployCommit,
  };
}

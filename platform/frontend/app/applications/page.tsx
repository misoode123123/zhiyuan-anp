"use client";

import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { API_BASE_URL } from "@/lib/api";
import type { Artifact } from "@/lib/api-types";
import { devStep } from "@/lib/devstep";
import { logger } from "@/lib/logger";
import { toast } from "@/lib/toast";

type Envelope<T> = { code: number; data: T; message?: string };
type PS = { id: string; name: string; slug: string };
type Instance = {
  env: string;
  status: string;
  url: string;
  version: number;
  host_port: number;
  image: string;
  updated_at: string;
};
type EnvVar = { id: string; key: string; value: string; is_secret: boolean };
type AppStats = {
  health?: string;
  cpu?: string;
  mem?: string;
  deployed?: boolean;
  external?: boolean;
  url?: string;
};
type App = {
  id: string;
  name: string;
  repo_dir: string;
  internal_port: number;
  image: string;
  container_name: string;
  host_port: number;
  url: string;
  version: number;
  status: string;
  last_error: string;
  build_log: string;
  deploy_mode: string; // managed(A类) / external(B类纳管外部)
  external_url: string; // external 模式时外部应用访问地址
  app_kind: string; // 应用类型 web/desktop/mobile/cli/service
  import_source?: "" | "git" | "dir"; // 导入来源：''=平台建仓 / git=远程仓 / dir=本机zip或服务器目录
  import_ref?: string; // git=url / dir=来源标识
  imported_at?: string; // 导入完成时间，进行中空
  updated_at: string;
  instances?: Instance[]; // 各环境部署实例（test/prod）
};
type Req = { id: string; title: string; status: string; application_id: string };
type Detail = {
  application: App;
  requirements: Req[];
  changes: { id: string; status: string; kind: string; source_id: string; created_at: string }[];
  releases: {
    id: string;
    version: string;
    status: string;
    change_id: string;
    created_at: string;
  }[];
  commits: { sha: string; message: string; date: string }[];
};

const STATUS_COLOR: Record<string, string> = {
  running: "bg-emerald-100 text-emerald-700",
  building: "bg-amber-100 text-amber-700",
  registered: "bg-neutral-100 text-neutral-500",
  stopped: "bg-blue-100 text-blue-700",
  failed: "bg-red-100 text-red-700",
  importing: "bg-purple-100 text-purple-700", // 导入进行中态（复用 status 列）
};

// DevWizard 开发向导：编码→测试→上线 进度条 + 项目上下文 + 引导文案。
// 让开发者一眼看到当前在哪步、下一步做什么（解决"流程不明确"）。
function DevWizard({ app }: { app: App }) {
  const s = devStep({ image: app.image, instances: app.instances });
  const testIns = app.instances?.find((i) => i.env === "test");
  const prodIns = app.instances?.find((i) => i.env === "prod");
  const step = (key: "code" | "test" | "prod", label: string) => {
    const st = s[key];
    const isCur = s.current === key;
    const cls =
      st === "done"
        ? "text-emerald-600"
        : isCur
          ? "font-semibold text-blue-600"
          : "text-neutral-400";
    const mark = st === "done" ? "✅" : isCur ? "●" : "○";
    return (
      <span className={cls}>
        {label} {mark}
      </span>
    );
  };
  return (
    <div className="mb-2 rounded-md bg-blue-50/60 p-2 text-xs">
      <div className="flex items-center gap-2">
        {step("code", "✏ 编码")}
        <span className="text-neutral-300">→</span>
        {step("test", "🧪 测试")}
        <span className="text-neutral-300">→</span>
        {step("prod", "🚀 上线")}
      </div>
      <div className="mt-1 flex flex-wrap gap-x-3 text-neutral-500">
        <span>
          仓库 <code>{app.repo_dir}</code>
        </span>
        {testIns && (
          <span>
            test{" "}
            <span className={testIns.status === "running" ? "text-emerald-600" : ""}>
              :{testIns.host_port} {testIns.status}
            </span>
          </span>
        )}
        {prodIns && (
          <span>
            prod{" "}
            <span className={prodIns.status === "running" ? "text-emerald-600" : ""}>
              :{prodIns.host_port} {prodIns.status}
            </span>
          </span>
        )}
      </div>
      <div className="mt-1 text-blue-700">👉 {s.hint}</div>
    </div>
  );
}

// 构建产物区：仅非 web/service 应用显示。
// 调 /artifacts 列产物、/build-artifacts 触发构建、/download 浏览器直接下载（跟随 302/流式）。
function ArtifactSection({
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
    if (appKind === "web" || appKind === "service") return;
    fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/artifacts`)
      .then((r) => r.json())
      .then((r: { data?: { artifacts?: Artifact[] } }) => setArts(r.data?.artifacts ?? []));
  }, [psID, appID, appKind]);
  if (appKind === "web" || appKind === "service") return null;
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
    <div className="mt-3 rounded border border-neutral-200 p-3">
      <div className="mb-2 flex items-center justify-between">
        <span className="text-sm font-semibold">构建产物</span>
        <button
          onClick={build}
          disabled={building}
          className="rounded bg-blue-600 px-2 py-0.5 text-xs text-white disabled:cursor-not-allowed disabled:opacity-40"
        >
          {building ? "构建中…" : "构建产物"}
        </button>
      </div>
      {arts.length === 0 ? (
        <div className="text-xs text-neutral-500">暂无产物，点「构建产物」生成</div>
      ) : (
        arts.map((a) => (
          <div key={a.id} className="flex items-center justify-between py-1 text-xs">
            <span>
              📦 {a.filename} · {a.platform}/{a.arch} · {(a.size_bytes / 1048576).toFixed(1)}MB ·
              sha: {a.sha256.slice(0, 8)}
            </span>
            <a
              href={`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/artifacts/${a.id}/download`}
              className="text-blue-600 hover:underline"
            >
              下载
            </a>
          </div>
        ))
      )}
    </div>
  );
}

export default function ApplicationsPage() {
  const [spaces, setSpaces] = useState<PS[]>([]);
  const [psID, setPsID] = useState("");
  const [apps, setApps] = useState<App[]>([]);
  const [nodes, setNodes] = useState<
    { id: string; name: string; host: string; status: string; app_count?: number }[]
  >([]);
  const [selectedNode, setSelectedNode] = useState(""); // 部署目标节点
  const [form, setForm] = useState({
    name: "",
    internal_port: 8080,
    deploy_mode: "managed" as "managed" | "external",
    external_url: "",
  });
  const [appKind, setAppKind] = useState<string>("web"); // 应用类型 web/desktop/mobile/cli/service
  const [wsTool, setWsTool] = useState("opencode"); // 交互编码工具（开发者可选，不同人选不同）
  const [logsFor, setLogsFor] = useState<string>("");
  const [logs, setLogs] = useState("");
  const [reqsFor, setReqsFor] = useState<string>("");
  const [appReqs, setAppReqs] = useState<Req[]>([]);
  const [detailFor, setDetailFor] = useState<string>("");
  const [detail, setDetail] = useState<Detail | null>(null);
  const [envFor, setEnvFor] = useState<string>("");
  const [appEnvs, setAppEnvs] = useState<EnvVar[]>([]);
  const [envForm, setEnvForm] = useState({ key: "", value: "", is_secret: false });
  const [appStats, setAppStats] = useState<Record<string, AppStats>>({});
  const [appChanges, setAppChanges] = useState<
    Record<string, { id: string; status: string; output?: string; created_at?: string }[]>
  >({});
  // 导入已有项目向导：source=git(远程仓) / upload(本机zip) / dir(服务器目录)
  const [importOpen, setImportOpen] = useState(false);
  const [importStep, setImportStep] = useState<1 | 2 | 3>(1);
  const [importSource, setImportSource] = useState<"git" | "upload" | "dir">("git");
  const [importForm, setImportForm] = useState({
    name: "",
    git_url: "",
    auth_token: "",
    server_path: "",
    internal_port: 8080,
  });
  const [importFile, setImportFile] = useState<File | null>(null);
  const [importing, setImporting] = useState(false); // 向导「开始导入」执行中（按钮禁用）
  const [importProgress, setImportProgress] = useState<string>(""); // 步骤3 显示的 last_error 进度
  // 轮询取消令牌：startImport 置 false、关闭向导置 true。
  // 用 ref 而非闭包里的 importing 状态——后者在 startImport 所属渲染里恒为 false（setState 仅排队），
  // 会导致 tick 永不递归、轮询只 fetch 一次、真实导入进度冻结。
  const importCancelRef = useRef(false);
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

  const load = (id: string) => {
    if (!id) return;
    fetch(`${API_BASE_URL}/project-spaces/${id}/apps`)
      .then((r) => r.json())
      .then((r: Envelope<App[]>) => setApps(r.data ?? []));
    fetch(`${API_BASE_URL}/deploy-nodes`)
      .then((r) => r.json())
      .then((r: Envelope<typeof nodes>) => setNodes(r.data ?? []));
  };
  useEffect(() => {
    load(psID);
    // 有 building 中的应用时轮询
    const t = setInterval(() => {
      load(psID);
      // 清理已完成的部署进度提示
      setDeployMsg((prev) => {
        if (Object.keys(prev).length === 0) return prev;
        const next = { ...prev };
        for (const id of Object.keys(next)) {
          const app = apps.find((a) => a.id === id);
          if (app && app.status !== "building") {
            toast.success(app.name + " → " + app.status);
            logger.info("app.deploy.done", { app: app.name, status: app.status });
            delete next[id];
          }
        }
        return next;
      });
    }, 3000);
    return () => clearInterval(t);
  }, [psID]);
  async function loadStats(id: string) {
    try {
      const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${id}/stats?env=prod`);
      const r = await res.json();
      if (r.code === 0) {
        const d = r.data;
        setAppStats((p) => ({
          ...p,
          [id]: {
            health: d.health,
            cpu: d.stats?.cpu_perc,
            mem: d.stats?.mem_usage,
            deployed: d.deployed,
            external: d.external,
            url: d.url,
          },
        }));
      }
    } catch {}
  }
  async function loadChanges(id: string) {
    try {
      const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${id}/detail`);
      const r = await res.json();
      if (r.code === 0 && r.data?.changes) {
        setAppChanges((p) => ({ ...p, [id]: r.data.changes }));
      }
    } catch {}
  }
  // 轮询已上线(prod running)应用的资源/健康（运维可观测）
  useEffect(() => {
    const poll = () =>
      apps.forEach((a) => {
        // external 应用无 instances，直接按 deploy_mode 探活；managed 看 prod running
        if (
          a.deploy_mode === "external" ||
          a.instances?.some((i) => i.env === "prod" && i.status === "running")
        )
          loadStats(a.id);
      });
    poll();
    apps.forEach((a) => loadChanges(a.id));
    const t = setInterval(poll, 30000);
    return () => clearInterval(t);
  }, [apps]);

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
    load(psID);
  }

  // 导入已有项目：source=git/dir 走 JSON，upload 走 multipart。
  // 返回占位应用(importing 态) 的 id 后，轮询 detail 至 status !== "importing"，
  // 把 last_error 当进度文案显示在向导第 3 步；完成/失败都刷新列表 + 关向导。
  function resetImportWizard() {
    setImportStep(1);
    setImportSource("git");
    setImportForm({ name: "", git_url: "", auth_token: "", server_path: "", internal_port: 8080 });
    setImportFile(null);
    setImportProgress("");
    setImporting(false);
  }
  function closeImportWizard() {
    // 取消可能挂起的轮询回调，防止旧 tick 在新向导打开后 reset 新向导状态（竞态）
    importCancelRef.current = true;
    setImportOpen(false);
    resetImportWizard();
  }
  async function startImport() {
    if (!importForm.name.trim()) {
      alert("请填应用名");
      return;
    }
    // 重置取消令牌：新一轮导入开始，允许 tick 递归轮询
    importCancelRef.current = false;
    if (importSource === "git" && !importForm.git_url.trim()) {
      alert("请填 git 仓库地址");
      return;
    }
    if (importSource === "dir" && !importForm.server_path.trim()) {
      alert("请填服务器目录路径");
      return;
    }
    if (importSource === "upload" && !importFile) {
      alert("请选择 zip 文件");
      return;
    }
    setImporting(true);
    setImportStep(3);
    setImportProgress("提交导入请求...");
    try {
      let res: Response;
      if (importSource === "upload") {
        // multipart：file + name + internal_port（不手动设 Content-Type，让浏览器带 boundary）
        const fd = new FormData();
        fd.append("file", importFile as Blob);
        fd.append("name", importForm.name);
        fd.append("internal_port", String(importForm.internal_port || 8080));
        res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/import/apps/upload`, {
          method: "POST",
          body: fd,
        });
      } else {
        // JSON：source=git 带 git_url + auth_token；source=dir 带 server_path
        const body: Record<string, unknown> = {
          source: importSource, // "git" | "dir"
          name: importForm.name,
          internal_port: importForm.internal_port || 8080,
        };
        if (importSource === "git") {
          body.git_url = importForm.git_url;
          if (importForm.auth_token) body.auth_token = importForm.auth_token;
        } else {
          body.server_path = importForm.server_path;
        }
        res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/import/apps`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        });
      }
      const r = (await res.json()) as Envelope<App & { id: string }>;
      if (r.code !== 0) {
        alert(r.message || "导入请求失败");
        setImporting(false);
        setImportStep(2);
        return;
      }
      const appID = r.data?.id;
      if (!appID) {
        alert("后端未返回应用 ID");
        setImporting(false);
        setImportStep(2);
        return;
      }
      // 轮询 detail 至 status !== "importing"（2s 一次；最多 15min 兜底）
      await pollImportDetail(appID);
    } catch (e) {
      alert("导入请求异常: " + (e instanceof Error ? e.message : String(e)));
      setImporting(false);
      setImportStep(2);
    }
  }
  // 轮询 GET .../apps/{aid}/detail 直至 status !== "importing"，把 last_error 当进度。
  async function pollImportDetail(appID: string) {
    const deadline = Date.now() + 15 * 60 * 1000;
    const tick = async () => {
      try {
        const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/detail`);
        const r = (await res.json()) as Envelope<Detail>;
        const app = r.data?.application;
        if (app) {
          setImportProgress(app.last_error || app.status);
          if (app.status !== "importing") {
            // 终态：registered(成功) / failed(失败)
            load(psID);
            setImporting(false);
            if (app.status === "failed") {
              setImportStep(3); // 留在第 3 步展示失败原因，用户手动关闭
              alert("导入失败: " + (app.last_error || "(无错误摘要)"));
            } else {
              toast.success(app.name + " 导入完成 → " + app.status);
              closeImportWizard();
            }
            return;
          }
        }
      } catch {
        // 网络抖动忽略，继续轮询
      }
      if (Date.now() < deadline && !importCancelRef.current) {
        setTimeout(tick, 2000);
      }
    };
    await tick();
  }
  const [deployMsg, setDeployMsg] = useState<Record<string, string>>({});

  // 上线 prod（带节点 + 变更闸门检查）
  async function promoteWithNode(id: string, nodeID: string) {
    const chgs = (appChanges[id] || []).filter((c) => c.status === "approved");
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
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${id}/promote`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const r = await res.json();
    if (r.code !== 0) alert(r.message);
    load(psID);
    loadChanges(id);
  }

  async function act(
    id: string,
    action: "deploy" | "stop" | "start",
    env?: string,
    nodeID?: string
  ) {
    const body: Record<string, string> = {};
    if (action === "deploy") {
      if (env) body.env = env;
      if (nodeID) body.node_id = nodeID;
    } else {
      // stop/start：显式带 env（默认 prod；后端按 env 鉴权，dev 无 prod 权限会被 403）
      body.env = env || "prod";
    }
    // 进度提示
    if (action === "deploy") {
      setDeployMsg((prev) => ({
        ...prev,
        [id]: `⏳ 构建部署 ${env} ${nodeID ? "(" + (nodes.find((n) => n.id === nodeID)?.name || nodeID) + ")" : ""}`,
      }));
    }
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${id}/${action}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const r = await res.json();
    if (r.code !== 0) {
      alert(r.message);
      setDeployMsg((prev) => {
        const n = { ...prev };
        delete n[id];
        return n;
      });
    }
    load(psID);
  }

  async function promote(id: string) {
    const chgs = (appChanges[id] || []).filter((c) => c.status === "approved");
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
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${id}/promote`, {
      method: "POST",
    });
    const r = await res.json();
    if (r.code !== 0) alert(r.message);
    load(psID);
    loadChanges(id); // 上线后 approved→released,刷新变更列表让"待上线"消失
  }
  // 跳转到「编码工作台」tab(/workspace):由该页自己调 /workspace 接口拉起 opencode 并全屏加载。
  // 走 tab 系统而非弹窗/页内嵌入——与平台其他功能一致,可在 tab 间切换、不离开平台。
  function openWorkspace(id: string, tool: string) {
    router.push(`/workspace?app=${id}&ps=${psID}&tool=${encodeURIComponent(tool)}`);
  }
  async function reloadEnv(id: string) {
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${id}/env`);
    const r = await res.json();
    setAppEnvs(r.data ?? []);
  }
  async function showEnv(id: string) {
    if (envFor === id) {
      setEnvFor("");
      return;
    }
    setEnvFor(id);
    await reloadEnv(id);
  }
  async function saveEnv(id: string) {
    if (!envForm.key.trim()) return;
    await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${id}/env`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(envForm),
    });
    setEnvForm({ key: "", value: "", is_secret: false });
    reloadEnv(id);
  }
  async function removeEnv(id: string, key: string) {
    await fetch(
      `${API_BASE_URL}/project-spaces/${psID}/apps/${id}/env/${encodeURIComponent(key)}`,
      { method: "DELETE" }
    );
    reloadEnv(id);
  }
  async function remove(id: string) {
    if (!confirm("删除应用（含容器）？")) return;
    await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${id}`, { method: "DELETE" });
    load(psID);
  }
  async function showLogs(id: string) {
    setLogsFor(id);
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${id}/logs`);
    const r = await res.json();
    setLogs(r.data?.logs ?? "(无)");
  }
  async function showReqs(id: string) {
    if (reqsFor === id) {
      setReqsFor("");
      return;
    }
    setReqsFor(id);
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${id}/requirements`);
    const r = await res.json();
    setAppReqs(r.data ?? []);
  }
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
  async function deployCommit(appID: string, sha: string) {
    const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/deploy-commit`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ sha }),
    });
    const r = await res.json();
    if (r.code !== 0) alert(r.message);
    load(psID);
  }

  return (
    <div>
      <div className="mb-4 flex items-center gap-3">
        <h1 className="text-xl font-bold">📦 应用部署</h1>
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
        {nodes.length > 1 && (
          <div className="flex items-center gap-1 text-sm">
            <span className="text-xs text-neutral-500">默认部署节点</span>
            <select
              value={selectedNode}
              onChange={(e) => setSelectedNode(e.target.value)}
              className="rounded-md border border-neutral-300 px-2 py-1 text-sm"
              title="新增应用的默认部署节点"
            >
              <option value="">
                本地（{nodes.find((n) => n.id === "node_local")?.host || ".28"}）
              </option>
              {nodes
                .filter((n) => n.id !== "node_local")
                .map((n) => (
                  <option key={n.id} value={n.id}>
                    {n.name} ({n.host}){n.app_count != null ? ` · ${n.app_count}应用` : ""}
                  </option>
                ))}
            </select>
          </div>
        )}
      </div>
      <p className="mb-4 text-sm text-neutral-600">
        把研发产出的应用（含 Dockerfile 的源码目录）自动{" "}
        <b>docker build → docker run → 分配端口 → 暴露 URL</b>。 repo_dir 填{" "}
        <b>docker 守护进程可见的路径</b>（生产 .28 上形如 <code>/data/repos/myapp</code>）。
      </p>

      {/* 注册 */}
      <div className="mb-4 flex flex-wrap items-end gap-2 rounded-lg border border-neutral-200 bg-white p-3 text-sm">
        <button
          onClick={() => {
            resetImportWizard();
            setImportOpen(true);
          }}
          className="rounded bg-emerald-600 px-3 py-1.5 text-white"
          title="把已有代码项目（git仓库/zip/服务器目录）导入 ANP 托管，后续走 AI 全流程"
        >
          📥 导入已有项目
        </button>
        <div>
          <label className="block text-xs text-neutral-500">接入模式</label>
          <select
            value={form.deploy_mode}
            onChange={(e) =>
              setForm({
                ...form,
                deploy_mode: e.target.value as "managed" | "external",
                external_url: "",
              })
            }
            className="rounded border border-neutral-300 px-2 py-1"
            title="managed=A 类平台托管(编码/部署);external=B 类纳管已在运行的外部应用(代码不动)"
          >
            <option value="managed">managed · 平台托管（A 类）</option>
            <option value="external">external · 纳管外部应用（B 类）</option>
          </select>
        </div>
        <div>
          <label className="block text-xs text-neutral-500">应用类型</label>
          <select
            value={appKind}
            onChange={(e) => setAppKind(e.target.value)}
            className="rounded border border-neutral-300 px-2 py-1"
            title="应用产物形态：web/service 走容器部署；desktop/mobile/cli 走构建产物下载"
          >
            <option value="web">Web 应用</option>
            <option value="desktop">桌面应用</option>
            <option value="mobile">移动应用</option>
            <option value="cli">命令行工具</option>
            <option value="service">后端服务</option>
          </select>
        </div>
        <div>
          <label className="block text-xs text-neutral-500">应用名</label>
          <input
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
            placeholder={form.deploy_mode === "external" ? "如 存量ERP" : "如 hello-go"}
            className="rounded border border-neutral-300 px-2 py-1"
          />
        </div>
        {form.deploy_mode === "external" ? (
          <div className="min-w-[16rem] flex-1">
            <label className="block text-xs text-neutral-500">
              外部应用地址 external_url（必填）
            </label>
            <input
              value={form.external_url}
              onChange={(e) => setForm({ ...form, external_url: e.target.value })}
              placeholder="http://host:port 或 https://domain/path"
              className="w-full rounded border border-neutral-300 px-2 py-1"
            />
          </div>
        ) : (
          <div>
            <label className="block text-xs text-neutral-500">容器内端口（可选）</label>
            <input
              type="number"
              value={form.internal_port}
              onChange={(e) => setForm({ ...form, internal_port: Number(e.target.value) })}
              className="w-24 rounded border border-neutral-300 px-2 py-1"
            />
          </div>
        )}
        <button onClick={register} className="rounded bg-blue-600 px-3 py-1.5 text-white">
          {form.deploy_mode === "external" ? "接入外部应用" : "创建应用"}
        </button>
        <span className="text-xs text-neutral-400">
          {form.deploy_mode === "external"
            ? "B 类轻接入：仅注册 + appgw 统一入口 /apps/&lt;app_id&gt;/ + ops 按 external_url 探活。不动外部代码。"
            : "仓库自动托管到 /data/repos/<应用名>（git），opencode 编码即提交到此"}
        </span>
      </div>

      {/* 导入已有项目向导（条件渲染区块）：3 步 ①选来源 ②填信息 ③执行/进度 */}
      {importOpen && (
        <div className="mb-4 rounded-lg border-2 border-emerald-300 bg-emerald-50/40 p-3 text-sm">
          <div className="mb-3 flex items-center gap-2">
            <span className="font-semibold text-emerald-700">📥 导入已有项目</span>
            {/* 步骤指示器 */}
            <span className="ml-2 flex gap-1 text-xs">
              {[1, 2, 3].map((n) => (
                <span
                  key={n}
                  className={`rounded px-1.5 py-0.5 ${
                    importStep === n
                      ? "bg-emerald-600 text-white"
                      : "bg-neutral-200 text-neutral-500"
                  }`}
                >
                  {n}
                </span>
              ))}
            </span>
            <span className="text-xs text-neutral-500">
              {importStep === 1 ? "选来源" : importStep === 2 ? "填信息" : "执行"}
            </span>
            <button
              onClick={closeImportWizard}
              className="ml-auto rounded bg-neutral-100 px-2 py-0.5 text-xs text-neutral-600"
              disabled={importing}
              title={importing ? "导入进行中，暂不能关闭" : "关闭向导"}
            >
              ✕ 关闭
            </button>
          </div>

          {/* 步骤 1：选来源 */}
          {importStep === 1 && (
            <div className="space-y-2">
              <div className="text-xs text-neutral-600">
                选择要导入的项目来源（导入后统一走 ANP AI 全流程：编码→测试→上线）
              </div>
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
                {(
                  [
                    {
                      key: "git",
                      icon: "📥",
                      title: "远程仓库",
                      desc: "git clone http(s)/SSH 仓到 /data/repos/，私有仓可填 token",
                    },
                    {
                      key: "upload",
                      icon: "📦",
                      title: "本机 zip",
                      desc: "上传源码 zip 包，平台解压到 /data/repos/（≤500MB）",
                    },
                    {
                      key: "dir",
                      icon: "📁",
                      title: "服务器目录",
                      desc: "复制服务器上已有源码目录（须在 /data/、/opt/legacy/ 白名单下）",
                    },
                  ] as const
                ).map((opt) => (
                  <button
                    key={opt.key}
                    onClick={() => {
                      setImportSource(opt.key);
                      setImportStep(2);
                    }}
                    className={`rounded border p-2 text-left hover:border-emerald-400 hover:bg-emerald-50 ${
                      importSource === opt.key
                        ? "border-emerald-500 bg-emerald-50"
                        : "border-neutral-200 bg-white"
                    }`}
                  >
                    <div className="font-medium">
                      {opt.icon} {opt.title}
                    </div>
                    <div className="mt-1 text-xs text-neutral-500">{opt.desc}</div>
                  </button>
                ))}
              </div>
            </div>
          )}

          {/* 步骤 2：填信息 + 应用名 + 端口 */}
          {importStep === 2 && (
            <div className="space-y-2">
              <div className="flex flex-wrap items-end gap-2">
                <div>
                  <label className="block text-xs text-neutral-500">来源</label>
                  <select
                    value={importSource}
                    onChange={(e) => setImportSource(e.target.value as "git" | "upload" | "dir")}
                    className="rounded border border-neutral-300 px-2 py-1"
                    disabled={importing}
                  >
                    <option value="git">📥 远程仓库 (git)</option>
                    <option value="upload">📦 本机 zip 上传</option>
                    <option value="dir">📁 服务器目录 (dir)</option>
                  </select>
                </div>
                <div>
                  <label className="block text-xs text-neutral-500">应用名（必填）</label>
                  <input
                    value={importForm.name}
                    onChange={(e) => setImportForm({ ...importForm, name: e.target.value })}
                    placeholder="如 hello-go（至少 2 字符，非纯数字/ID 前缀）"
                    className="rounded border border-neutral-300 px-2 py-1"
                    disabled={importing}
                  />
                </div>
                <div>
                  <label className="block text-xs text-neutral-500">容器内端口</label>
                  <input
                    type="number"
                    value={importForm.internal_port}
                    onChange={(e) =>
                      setImportForm({ ...importForm, internal_port: Number(e.target.value) })
                    }
                    className="w-24 rounded border border-neutral-300 px-2 py-1"
                    disabled={importing}
                  />
                </div>
              </div>
              {/* 来源特定字段 */}
              {importSource === "git" && (
                <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                  <div>
                    <label className="block text-xs text-neutral-500">git 仓库地址（必填）</label>
                    <input
                      value={importForm.git_url}
                      onChange={(e) => setImportForm({ ...importForm, git_url: e.target.value })}
                      placeholder="https://github.com/owner/repo.git 或 git@github.com:owner/repo.git"
                      className="w-full rounded border border-neutral-300 px-2 py-1"
                      disabled={importing}
                    />
                  </div>
                  <div>
                    <label className="block text-xs text-neutral-500">
                      私有仓 token（可选，不落库）
                    </label>
                    <input
                      value={importForm.auth_token}
                      onChange={(e) => setImportForm({ ...importForm, auth_token: e.target.value })}
                      placeholder="HTTPS 私有仓填 token；SSH 仓留空"
                      type="password"
                      className="w-full rounded border border-neutral-300 px-2 py-1"
                      disabled={importing}
                    />
                  </div>
                </div>
              )}
              {importSource === "upload" && (
                <div>
                  <label className="block text-xs text-neutral-500">zip 文件（必填，≤500MB）</label>
                  <input
                    type="file"
                    accept=".zip,application/zip"
                    onChange={(e) => setImportFile(e.target.files?.[0] ?? null)}
                    className="block w-full text-sm"
                    disabled={importing}
                  />
                  {importFile && (
                    <div className="mt-1 text-xs text-neutral-500">
                      已选: {importFile.name}（{(importFile.size / 1024 / 1024).toFixed(1)} MB）
                    </div>
                  )}
                </div>
              )}
              {importSource === "dir" && (
                <div>
                  <label className="block text-xs text-neutral-500">
                    服务器目录绝对路径（必填，须在 /data/、/opt/legacy/ 白名单下）
                  </label>
                  <input
                    value={importForm.server_path}
                    onChange={(e) => setImportForm({ ...importForm, server_path: e.target.value })}
                    placeholder="/data/legacy/myapp 或 /opt/legacy/svc"
                    className="w-full rounded border border-neutral-300 px-2 py-1"
                    disabled={importing}
                  />
                </div>
              )}
              <div className="flex gap-2 pt-1">
                <button
                  onClick={() => setImportStep(1)}
                  className="rounded bg-neutral-100 px-3 py-1.5 text-neutral-600"
                  disabled={importing}
                >
                  ← 上一步
                </button>
                <button
                  onClick={startImport}
                  className="rounded bg-emerald-600 px-3 py-1.5 text-white"
                  disabled={importing}
                >
                  开始导入
                </button>
              </div>
            </div>
          )}

          {/* 步骤 3：执行 / 进度 */}
          {importStep === 3 && (
            <div className="space-y-2">
              <div className="flex items-center gap-2 rounded bg-white p-2 text-sm">
                {importing && (
                  <span className="inline-block h-3 w-3 animate-spin rounded-full border-2 border-emerald-400 border-t-transparent"></span>
                )}
                <span className="font-medium text-emerald-700">
                  {importing ? "导入中..." : "导入结束"}
                </span>
                <span className="ml-auto text-xs text-neutral-400">
                  {importSource === "git"
                    ? "📥 git"
                    : importSource === "upload"
                      ? "📦 zip"
                      : "📁 dir"}{" "}
                  · {importForm.name}
                </span>
              </div>
              <div className="rounded bg-neutral-900 p-2 text-xs text-green-300">
                <div className="mb-1 text-neutral-400">后端进度（last_error 实时回显）：</div>
                <pre className="whitespace-pre-wrap break-all">
                  {importProgress || "(等待后端响应...)"}
                </pre>
              </div>
              {!importing && (
                <button
                  onClick={closeImportWizard}
                  className="rounded bg-emerald-600 px-3 py-1.5 text-white"
                >
                  完成 / 关闭
                </button>
              )}
            </div>
          )}
        </div>
      )}

      {/* 应用列表 */}
      <div className="space-y-3">
        {apps.map((a) => {
          const isExternal = a.deploy_mode === "external";
          // 导入进行中：status===importing（git clone / zip 解压 / 目录复制中），编码/部署按钮禁用
          const isImporting = a.status === "importing";
          return (
            <div key={a.id} className="rounded-lg border border-neutral-200 bg-white p-3">
              {/* 导入进度提示：importing 态显示 last_error 进度（后端实时回写「正在 clone...」等） */}
              {isImporting && (
                <div className="mb-2 flex items-center gap-2 rounded bg-purple-50 px-3 py-1.5 text-sm text-purple-700">
                  <span className="inline-block h-3 w-3 animate-spin rounded-full border-2 border-purple-400 border-t-transparent"></span>
                  导入已有项目中...
                  <span className="truncate text-xs text-purple-500">
                    {a.last_error || "(等待进度...)"}
                  </span>
                  <span className="ml-auto text-xs text-purple-400">每3秒自动刷新</span>
                </div>
              )}
              {/* 部署进度提示：从 app.status 派生（切 tab 回来也可见，因为 3s 轮询刷新 status） */}
              {a.status === "building" && (
                <div className="mb-2 flex items-center gap-2 rounded bg-blue-50 px-3 py-1.5 text-sm text-blue-700">
                  <span className="inline-block h-3 w-3 animate-spin rounded-full border-2 border-blue-400 border-t-transparent"></span>
                  构建部署中...
                  {a.instances?.find((i) => i.status === "building") && (
                    <span className="text-xs text-blue-400">
                      {a.instances.find((i) => i.status === "building")?.url || ""}
                    </span>
                  )}
                  <span className="ml-auto text-xs text-blue-400">每3秒自动刷新</span>
                </div>
              )}
              {a.status === "running" && a.instances?.some((i) => i.status === "building") && (
                <div className="mb-2 flex items-center gap-2 rounded bg-blue-50 px-3 py-1.5 text-sm text-blue-700">
                  <span className="inline-block h-3 w-3 animate-spin rounded-full border-2 border-blue-400 border-t-transparent"></span>
                  {a.instances.find((i) => i.status === "building")?.env} 环境构建中...
                  <span className="ml-auto text-xs text-blue-400">每3秒自动刷新</span>
                </div>
              )}
              {a.status === "failed" && (
                <div className="mb-2 rounded bg-red-50 px-3 py-1.5 text-sm text-red-700">
                  <div>❌ 构建失败：{a.last_error?.slice(0, 100) || "(无错误摘要)"}</div>
                  {a.build_log && (
                    <details className="mt-1">
                      <summary className="cursor-pointer text-xs text-red-500">
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
                  <span className="rounded bg-indigo-100 px-1.5 py-0.5 text-xs text-indigo-700">
                    external · 纳管
                  </span>
                ) : null}
                {/* 导入来源徽章：git=远程仓 / dir=本机zip或服务器目录；空=平台建仓（不显示） */}
                {a.import_source === "git" && (
                  <span
                    className="rounded bg-emerald-100 px-1.5 py-0.5 text-xs text-emerald-700"
                    title={"导入自 git 仓库: " + (a.import_ref || "")}
                  >
                    📥 git
                  </span>
                )}
                {a.import_source === "dir" && (
                  <span
                    className="rounded bg-amber-100 px-1.5 py-0.5 text-xs text-amber-700"
                    title={"导入自 目录/zip: " + (a.import_ref || "")}
                  >
                    📁 目录
                  </span>
                )}
                <span
                  className={`rounded px-1.5 py-0.5 text-xs ${STATUS_COLOR[a.status] ?? "bg-neutral-100"}`}
                >
                  {a.status}
                </span>
                {/* 构建进度条 */}
                {(a.status === "building" || a.status === "running") &&
                  a.instances &&
                  a.instances.some((i) => i.status === "building") && (
                    <span className="flex items-center gap-1 text-xs text-amber-600">
                      <span className="inline-block h-3 w-3 animate-spin rounded-full border-2 border-amber-400 border-t-transparent"></span>
                      构建中...
                    </span>
                  )}
                {a.status === "building" &&
                  a.instances &&
                  a.instances.some((i) => i.status === "failed") && (
                    <span className="text-xs text-red-500">构建失败</span>
                  )}
                {!isExternal && a.image && (
                  <span className="text-xs text-neutral-400">
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
                        className="rounded bg-indigo-100 px-2 py-0.5 text-xs text-indigo-700"
                        title="打开外部应用地址"
                      >
                        🔗 访问
                      </a>
                      <button
                        onClick={() => showDetail(a.id)}
                        className="rounded bg-neutral-100 px-2 py-0.5 text-xs"
                      >
                        详情
                      </button>
                    </>
                  ) : (
                    <>
                      <button
                        onClick={() => act(a.id, "deploy", "test", selectedNode)}
                        className="rounded bg-blue-100 px-2 py-0.5 text-xs text-blue-700 disabled:cursor-not-allowed disabled:opacity-40"
                        disabled={isImporting}
                        title={isImporting ? "导入完成前不可部署" : "构建并部署到 test 环境"}
                      >
                        构建部署(test)
                      </button>
                      <button
                        onClick={() => promoteWithNode(a.id, selectedNode)}
                        className="rounded bg-emerald-100 px-2 py-0.5 text-xs text-emerald-700 disabled:cursor-not-allowed disabled:opacity-40"
                        disabled={isImporting}
                        title={isImporting ? "导入完成前不可上线" : "上线到 prod"}
                      >
                        🚀 上线(prod)
                      </button>
                      <select
                        value={wsTool}
                        onChange={(e) => setWsTool(e.target.value)}
                        className="rounded border border-neutral-300 px-1 py-0.5 text-xs disabled:opacity-40"
                        title="选择交互编码工具"
                        disabled={isImporting}
                      >
                        <option value="opencode">opencode</option>
                        <option value="claude">claude</option>
                        <option value="codex">codex</option>
                      </select>
                      <button
                        onClick={() => openWorkspace(a.id, wsTool)}
                        className="rounded bg-purple-100 px-2 py-0.5 text-xs text-purple-700 disabled:cursor-not-allowed disabled:opacity-40"
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
                        onClick={() => showEnv(a.id)}
                        className="rounded bg-neutral-100 px-2 py-0.5 text-xs"
                      >
                        ⚙️变量
                      </button>
                      {a.status === "running" && (
                        <button
                          onClick={() => act(a.id, "stop")}
                          className="rounded bg-neutral-100 px-2 py-0.5 text-xs"
                        >
                          停止
                        </button>
                      )}
                      {a.status === "stopped" && (
                        <button
                          onClick={() => act(a.id, "start")}
                          className="rounded bg-emerald-100 px-2 py-0.5 text-xs text-emerald-700"
                        >
                          启动
                        </button>
                      )}
                      <button
                        onClick={() => showReqs(a.id)}
                        className="rounded bg-neutral-100 px-2 py-0.5 text-xs"
                      >
                        需求
                      </button>
                      <button
                        onClick={() => showDetail(a.id)}
                        className="rounded bg-neutral-100 px-2 py-0.5 text-xs"
                      >
                        详情
                      </button>
                      <button
                        onClick={() => showLogs(a.id)}
                        className="rounded bg-neutral-100 px-2 py-0.5 text-xs"
                      >
                        日志
                      </button>
                    </>
                  )}
                  <button
                    onClick={() => remove(a.id)}
                    className="rounded bg-red-100 px-2 py-0.5 text-xs text-red-700"
                  >
                    删除
                  </button>
                </div>
              </div>
              <div className="mt-1 text-xs text-neutral-500">
                {isExternal ? (
                  <>
                    external_url:{" "}
                    <a
                      href={a.external_url}
                      target="_blank"
                      rel="noreferrer"
                      className="text-indigo-600 hover:underline"
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
                <div className="text-xs text-neutral-400">
                  {a.status === "running" ? "部署于" : "更新于"}：
                  {new Date(a.updated_at).toLocaleString("zh-CN", { hour12: false })}
                </div>
              )}
              {isExternal ? (
                // external 应用探活展示：无容器/test-prod 实例，只显示 external_url 健康状态
                appStats[a.id]?.deployed ? (
                  <div className="mt-2 rounded bg-indigo-50 p-2 text-xs">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="rounded bg-indigo-100 px-1.5 py-0.5 font-medium text-indigo-700">
                        external 探活
                      </span>
                      <span className="text-neutral-400">健康</span>
                      <span
                        className={
                          (appStats[a.id].health === "up" ? "text-emerald-600" : "text-red-600") +
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
                      <div key={env} className="rounded bg-neutral-50 p-2 text-xs">
                        <div className="flex items-center gap-2">
                          <span
                            className={`rounded px-1.5 py-0.5 font-medium ${env === "prod" ? "bg-blue-100 text-blue-700" : "bg-purple-100 text-purple-700"}`}
                          >
                            {label}
                          </span>
                          {ins && (
                            <span
                              className={`rounded px-1.5 py-0.5 ${STATUS_COLOR[ins.status] ?? "bg-neutral-100"}`}
                            >
                              {ins.status}
                            </span>
                          )}
                          {ins && ins.version > 0 && (
                            <span className="text-neutral-400">v{ins.version}</span>
                          )}
                        </div>
                        {ins?.url ? (
                          <a
                            href={ins.url}
                            target="_blank"
                            rel="noreferrer"
                            className="mt-1 block truncate text-blue-600 hover:underline"
                          >
                            {ins.url}
                          </a>
                        ) : (
                          <div className="mt-1 text-neutral-400">
                            {env === "prod"
                              ? "未上线（点「上线」部署）"
                              : "未部署（发布或「构建部署」）"}
                          </div>
                        )}
                        {env === "prod" && appStats[a.id]?.deployed && (
                          <div className="mt-1 flex flex-wrap items-center gap-2 text-[11px]">
                            <span className="text-neutral-400">健康</span>
                            <span
                              className={
                                (appStats[a.id].health === "up"
                                  ? "text-emerald-600"
                                  : "text-red-600") + " font-medium"
                              }
                            >
                              {appStats[a.id].health}
                            </span>
                            {appStats[a.id].cpu && (
                              <span className="text-neutral-400">
                                CPU {appStats[a.id].cpu} · 内存 {appStats[a.id].mem}
                              </span>
                            )}
                          </div>
                        )}
                        {env === "prod" &&
                          (appChanges[a.id] || []).filter((c) => c.status === "approved").length >
                            0 && (
                            <div className="mt-1 rounded bg-amber-50 p-1 text-[11px]">
                              <div className="font-medium text-amber-700">
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
                                  <div key={c.id} className="truncate text-amber-600">
                                    {(
                                      (c.output || "").match(/【总结】(.+)/)?.[1] ||
                                      c.id.slice(0, 12)
                                    ).slice(0, 50)}
                                    {c.created_at && (
                                      <span className="ml-1 text-[10px] text-neutral-400">
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
                <div className="mt-1 rounded bg-red-50 p-1 text-xs text-red-700">
                  {a.last_error}
                </div>
              )}
              {logsFor === a.id && (
                <pre className="mt-2 max-h-48 overflow-auto rounded bg-neutral-900 p-2 text-xs text-green-300">
                  {logs}
                </pre>
              )}
              {reqsFor === a.id && (
                <div className="mt-2 rounded bg-neutral-50 p-2 text-xs">
                  <div className="mb-1 text-neutral-500">归属此应用的需求（{appReqs.length}）</div>
                  {appReqs.map((q) => (
                    <div key={q.id} className="flex items-center gap-2 py-0.5">
                      <span
                        className={`rounded px-1.5 py-0.5 ${q.status === "delivered" ? "bg-emerald-100 text-emerald-700" : "bg-neutral-100 text-neutral-500"}`}
                      >
                        {q.status}
                      </span>
                      <span className="truncate">{q.title}</span>
                    </div>
                  ))}
                  {appReqs.length === 0 && (
                    <div className="text-neutral-400">暂无（发布此应用的需求后会自动归属到此）</div>
                  )}
                </div>
              )}
              {envFor === a.id && (
                <div className="mt-2 rounded bg-neutral-50 p-2 text-xs">
                  <div className="mb-1 text-neutral-500">
                    运行时环境变量（部署时 -e 注入容器；🔒=密钥已隐藏明文）
                  </div>
                  <div className="space-y-1">
                    {appEnvs.map((e) => (
                      <div key={e.id} className="flex items-center gap-2">
                        <code className="text-neutral-700">{e.key}</code>
                        <span className="text-neutral-400">=</span>
                        <span className={e.is_secret ? "text-amber-600" : "text-neutral-600"}>
                          {e.is_secret ? "🔒 已隐藏" : e.value || "(空)"}
                        </span>
                        <button
                          onClick={() => removeEnv(a.id, e.key)}
                          className="ml-auto rounded bg-red-100 px-1.5 py-0.5 text-red-700"
                        >
                          删
                        </button>
                      </div>
                    ))}
                    {appEnvs.length === 0 && <div className="text-neutral-400">暂无</div>}
                  </div>
                  <div className="mt-2 flex flex-wrap items-center gap-1 border-t border-neutral-200 pt-2">
                    <input
                      value={envForm.key}
                      onChange={(ev) => setEnvForm({ ...envForm, key: ev.target.value })}
                      placeholder="KEY"
                      className="w-28 rounded border border-neutral-300 px-1 py-0.5"
                    />
                    <input
                      value={envForm.value}
                      onChange={(ev) => setEnvForm({ ...envForm, value: ev.target.value })}
                      placeholder="value"
                      type={envForm.is_secret ? "password" : "text"}
                      className="flex-1 rounded border border-neutral-300 px-1 py-0.5"
                    />
                    <label className="flex items-center gap-1">
                      <input
                        type="checkbox"
                        checked={envForm.is_secret}
                        onChange={(ev) => setEnvForm({ ...envForm, is_secret: ev.target.checked })}
                      />
                      密钥
                    </label>
                    <button
                      onClick={() => saveEnv(a.id)}
                      className="rounded bg-blue-600 px-2 py-0.5 text-white"
                    >
                      保存
                    </button>
                  </div>
                </div>
              )}
              {detailFor === a.id && detail && (
                <>
                  <div className="mt-2 grid grid-cols-1 gap-2 rounded bg-neutral-50 p-2 text-xs md:grid-cols-3">
                    <div>
                      <div className="mb-1 font-medium text-neutral-500">
                        需求（{detail.requirements.length}）
                      </div>
                      {detail.requirements.map((q) => (
                        <div key={q.id} className="truncate">
                          <span
                            className={
                              q.status === "delivered" ? "text-emerald-600" : "text-neutral-500"
                            }
                          >
                            ●
                          </span>{" "}
                          {q.title}
                        </div>
                      ))}
                      {detail.requirements.length === 0 && (
                        <div className="text-neutral-400">无</div>
                      )}
                    </div>
                    <div>
                      <div className="mb-1 font-medium text-neutral-500">
                        变更（{detail.changes.length}）
                      </div>
                      {detail.changes.map((c) => (
                        <div key={c.id}>
                          <span
                            className={
                              c.status === "approved" ? "text-emerald-600" : "text-amber-600"
                            }
                          >
                            ●
                          </span>{" "}
                          {c.kind} · {c.status}
                        </div>
                      ))}
                      {detail.changes.length === 0 && <div className="text-neutral-400">无</div>}
                    </div>
                    <div>
                      <div className="mb-1 font-medium text-neutral-500">
                        发布（{detail.releases.length}）
                      </div>
                      {detail.releases.map((r) => (
                        <div key={r.id}>
                          <span className="text-blue-600">●</span> {r.version} · {r.status}
                        </div>
                      ))}
                      {detail.releases.length === 0 && <div className="text-neutral-400">无</div>}
                    </div>
                  </div>
                  {detail.commits.length > 0 && (
                    <div className="mt-2 border-t border-neutral-200 pt-2">
                      <div className="mb-1 font-medium text-neutral-500">
                        版本历史（{detail.commits.length}，可部署/回滚任意版本）
                      </div>
                      <div className="space-y-1">
                        {detail.commits.map((c) => (
                          <div key={c.sha} className="flex items-center gap-2">
                            <code className="text-xs text-neutral-400">{c.sha.slice(0, 7)}</code>
                            <span className="truncate text-neutral-700">{c.message}</span>
                            <button
                              onClick={() => deployCommit(a.id, c.sha)}
                              className="ml-auto rounded bg-amber-100 px-2 py-0.5 text-xs text-amber-700"
                            >
                              部署此版本
                            </button>
                          </div>
                        ))}
                      </div>
                    </div>
                  )}
                </>
              )}
              <ArtifactSection psID={psID} appID={a.id} appKind={a.app_kind} />
            </div>
          );
        })}
        {apps.length === 0 && (
          <div className="text-sm text-neutral-400">
            暂无应用。注册一个（源码目录需含 Dockerfile）后点「构建部署」，或点「📥
            导入已有项目」把现有代码项目（git仓库/zip/服务器目录）导入平台，或用 external
            模式接入已在运行的外部应用。
          </div>
        )}
      </div>

      <div className="mt-4 rounded-md bg-amber-50 p-2 text-xs text-amber-700">
        说明：构建部署在 ANP 后端容器内经宿主 docker socket 执行。repo_dir 必须是
        <b>后端容器内可见</b>的路径（产出应用默认在 <code>/data/repos/&lt;应用名&gt;</code>
        ，对应宿主 <code>/opt/anp/data/repos/...</code>）。端口自动从 9100-9300 分配。
      </div>
    </div>
  );
}

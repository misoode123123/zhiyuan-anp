"use client";

// 导入已有项目向导（自 page.tsx 原样平移，行为零变化）：
// 3 步（选来源/填信息/执行进度）；startImport 提交、pollImportDetail 轮询 detail 至终态，
// last_error 当进度文案；importCancelRef 防旧 tick 竞态（注释随原样保留）。
import { useRef, useState, forwardRef, useImperativeHandle } from "react";
import { API_BASE_URL } from "@/lib/api";
import { toast } from "@/lib/toast";
import type { App, Detail, Envelope } from "../_lib/types";
import { SourceFields, type ImportForm } from "./source-fields";

// 壳通过 ref 持有的命令句柄：open() 复位步骤并打开（原「导入已有项目」按钮行为）。
export type ImportWizardHandle = {
  open: () => void;
};

// 步骤 1 来源三卡描述符（自组件体内提升为模块级常量，内容一字不改）。
const SOURCE_OPTIONS = [
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
] as const;

function ImportWizardInner(
  { psID, onDone }: { psID: string; onDone: () => void },
  ref: React.Ref<ImportWizardHandle>
) {
  // 导入已有项目向导：source=git(远程仓) / upload(本机zip) / dir(服务器目录)
  const [importOpen, setImportOpen] = useState(false);
  const [importStep, setImportStep] = useState<1 | 2 | 3>(1);
  const [importSource, setImportSource] = useState<"git" | "upload" | "dir">("git");
  const [importForm, setImportForm] = useState<ImportForm>({
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
            onDone(); // 原 load(psID)：终态/失败时壳刷新列表
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

  useImperativeHandle(ref, () => ({
    open: () => {
      resetImportWizard();
      setImportOpen(true);
    },
  }));

  if (!importOpen) return null;
  return (
    <div className="mb-4 rounded-lg border-2 border-success bg-success/40 p-3 text-sm">
      <div className="mb-3 flex items-center gap-2">
        <span className="font-semibold text-success">📥 导入已有项目</span>
        {/* 步骤指示器 */}
        <span className="ml-2 flex gap-1 text-xs">
          {[1, 2, 3].map((n) => (
            <span
              key={n}
              className={`rounded px-1.5 py-0.5 ${
                importStep === n ? "bg-success text-white" : "bg-surface-2 text-text-muted"
              }`}
            >
              {n}
            </span>
          ))}
        </span>
        <span className="text-xs text-text-muted">
          {importStep === 1 ? "选来源" : importStep === 2 ? "填信息" : "执行"}
        </span>
        <button
          onClick={closeImportWizard}
          className="ml-auto rounded bg-surface-2 px-2 py-0.5 text-xs text-text-muted"
          disabled={importing}
          title={importing ? "导入进行中，暂不能关闭" : "关闭向导"}
        >
          ✕ 关闭
        </button>
      </div>

      {/* 步骤 1：选来源 */}
      {importStep === 1 && (
        <div className="space-y-2">
          <div className="text-xs text-text-muted">
            选择要导入的项目来源（导入后统一走 ANP AI 全流程：编码→测试→上线）
          </div>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
            {SOURCE_OPTIONS.map((opt) => (
              <button
                key={opt.key}
                onClick={() => {
                  setImportSource(opt.key);
                  setImportStep(2);
                }}
                className={`rounded border p-2 text-left hover:border-success hover:bg-success/10 ${
                  importSource === opt.key
                    ? "border-success bg-success/10"
                    : "border-border bg-surface"
                }`}
              >
                <div className="font-medium">
                  {opt.icon} {opt.title}
                </div>
                <div className="mt-1 text-xs text-text-muted">{opt.desc}</div>
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
              <label className="block text-xs text-text-muted">来源</label>
              <select
                value={importSource}
                onChange={(e) => setImportSource(e.target.value as "git" | "upload" | "dir")}
                className="rounded border border-border px-2 py-1"
                disabled={importing}
              >
                <option value="git">📥 远程仓库 (git)</option>
                <option value="upload">📦 本机 zip 上传</option>
                <option value="dir">📁 服务器目录 (dir)</option>
              </select>
            </div>
            <div>
              <label className="block text-xs text-text-muted">应用名（必填）</label>
              <input
                value={importForm.name}
                onChange={(e) => setImportForm({ ...importForm, name: e.target.value })}
                placeholder="如 hello-go（至少 2 字符，非纯数字/ID 前缀）"
                className="rounded border border-border px-2 py-1"
                disabled={importing}
              />
            </div>
            <div>
              <label className="block text-xs text-text-muted">容器内端口</label>
              <input
                type="number"
                value={importForm.internal_port}
                onChange={(e) =>
                  setImportForm({ ...importForm, internal_port: Number(e.target.value) })
                }
                className="w-24 rounded border border-border px-2 py-1"
                disabled={importing}
              />
            </div>
          </div>
          {/* 来源特定字段（块抽至 source-fields.tsx，JSX 等价） */}
          <SourceFields
            source={importSource}
            form={importForm}
            setForm={setImportForm}
            file={importFile}
            setFile={setImportFile}
            disabled={importing}
          />
          <div className="flex gap-2 pt-1">
            <button
              onClick={() => setImportStep(1)}
              className="rounded bg-surface-2 px-3 py-1.5 text-text-muted"
              disabled={importing}
            >
              ← 上一步
            </button>
            <button
              onClick={startImport}
              className="rounded bg-success px-3 py-1.5 text-white"
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
          <div className="flex items-center gap-2 rounded bg-surface p-2 text-sm">
            {importing && (
              <span className="inline-block h-3 w-3 animate-spin rounded-full border-2 border-success border-t-transparent"></span>
            )}
            <span className="font-medium text-success">{importing ? "导入中..." : "导入结束"}</span>
            <span className="ml-auto text-xs text-text-muted">
              {importSource === "git" ? "📥 git" : importSource === "upload" ? "📦 zip" : "📁 dir"}{" "}
              · {importForm.name}
            </span>
          </div>
          <div className="rounded bg-neutral-900 p-2 text-xs text-green-300">
            <div className="mb-1 text-text-muted">后端进度（last_error 实时回显）：</div>
            <pre className="whitespace-pre-wrap break-all">
              {importProgress || "(等待后端响应...)"}
            </pre>
          </div>
          {!importing && (
            <button
              onClick={closeImportWizard}
              className="rounded bg-success px-3 py-1.5 text-white"
            >
              完成 / 关闭
            </button>
          )}
        </div>
      )}
    </div>
  );
}

// forwardRef 包装：壳 <ImportWizard ref={wizRef} psID={...} onDone={() => load(psID)} />
export const ImportWizard = forwardRef(ImportWizardInner);

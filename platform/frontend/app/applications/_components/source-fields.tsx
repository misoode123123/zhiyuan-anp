"use client";

// 导入向导步骤 2 的来源特定字段块（自 import-wizard.tsx 原样抽出，JSX/文案/类名零改动）：
// git=url+token、upload=zip 文件、dir=服务器目录三个条件块。
// 抽出动机：import-wizard.tsx 守「组件 ≤400 行」纪律（本分支单文件纪律首次落地样板）。
import type { Dispatch, SetStateAction } from "react";

// 导入表单态（import-wizard 的 useState 形状提升命名）
export type ImportForm = {
  name: string;
  git_url: string;
  auth_token: string;
  server_path: string;
  internal_port: number;
};

export function SourceFields({
  source,
  form,
  setForm,
  file,
  setFile,
  disabled,
}: {
  source: "git" | "upload" | "dir";
  form: ImportForm;
  setForm: Dispatch<SetStateAction<ImportForm>>;
  file: File | null;
  setFile: Dispatch<SetStateAction<File | null>>;
  disabled: boolean;
}) {
  return (
    <>
      {source === "git" && (
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
          <div>
            <label className="block text-xs text-text-muted">git 仓库地址（必填）</label>
            <input
              value={form.git_url}
              onChange={(e) => setForm({ ...form, git_url: e.target.value })}
              placeholder="https://github.com/owner/repo.git 或 git@github.com:owner/repo.git"
              className="w-full rounded border border-border px-2 py-1"
              disabled={disabled}
            />
          </div>
          <div>
            <label className="block text-xs text-text-muted">私有仓 token（可选，不落库）</label>
            <input
              value={form.auth_token}
              onChange={(e) => setForm({ ...form, auth_token: e.target.value })}
              placeholder="HTTPS 私有仓填 token；SSH 仓留空"
              type="password"
              className="w-full rounded border border-border px-2 py-1"
              disabled={disabled}
            />
          </div>
        </div>
      )}
      {source === "upload" && (
        <div>
          <label className="block text-xs text-text-muted">zip 文件（必填，≤500MB）</label>
          <input
            type="file"
            accept=".zip,application/zip"
            onChange={(e) => setFile(e.target.files?.[0] ?? null)}
            className="block w-full text-sm"
            disabled={disabled}
          />
          {file && (
            <div className="mt-1 text-xs text-text-muted">
              已选: {file.name}（{(file.size / 1024 / 1024).toFixed(1)} MB）
            </div>
          )}
        </div>
      )}
      {source === "dir" && (
        <div>
          <label className="block text-xs text-text-muted">
            服务器目录绝对路径（必填，须在 /data/、/opt/legacy/ 白名单下）
          </label>
          <input
            value={form.server_path}
            onChange={(e) => setForm({ ...form, server_path: e.target.value })}
            placeholder="/data/legacy/myapp 或 /opt/legacy/svc"
            className="w-full rounded border border-border px-2 py-1"
            disabled={disabled}
          />
        </div>
      )}
    </>
  );
}

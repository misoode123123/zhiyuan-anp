"use client";

import { ProjectDocs } from "../project-docs";

// 文件视图：包现有 ProjectDocs（repo 文档列表 + 展开），样式沿用。
export function FilesView({ psID, appID }: { psID: string; appID: string }) {
  return (
    <div className="flex h-full flex-col">
      <div className="border-b border-[#e6e6e6] px-3 py-2 text-[11px] uppercase tracking-wide text-[#636363]">
        文件结构
      </div>
      <div className="flex-1 overflow-auto p-1 text-[12px]">
        <ProjectDocs psID={psID} appID={appID} />
      </div>
    </div>
  );
}

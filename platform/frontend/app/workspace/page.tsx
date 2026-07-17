import { Suspense } from "react";
import WorkspaceFrame from "./workspace-frame";

// 编码工作台 tab 页：作为顶部 TabBar 的一个 tab（与概览/需求/应用部署并列），
// 全屏加载 opencode（直达预创建会话）。编码产出 commit 到 repo，回应用卡片「构建部署」即可验证。
//
// 下方 client 子组件用 useSearchParams 读 query；Next 16 要求其外层包 <Suspense>，
// 否则生产 build 会以 missing Suspense boundary 失败。
export default function Page() {
  return (
    <Suspense fallback={<div className="p-4 text-sm text-neutral-500">准备编码工作台…</div>}>
      <WorkspaceFrame />
    </Suspense>
  );
}

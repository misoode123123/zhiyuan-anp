# 模型中心·前端模型选择与授权管理（Plan 2a）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让平台用户在 AI 生成入口（需求规格 / 测试生成）选择「自己被授权的模型」，并让管理员在用户管理页给每个用户分配可用的模型——前端纯改造，后端 API（Plan 1 / PR#9 已合 main）已就绪。

**Architecture:** 新建一个受控下拉组件 `ModelSelect`，数据源 `GET /users/me/models`（当前用户授权模型，返回 `compute_model` 列表），空授权时回退到该 `task_type` 路由的 primary。挂到 requirements 与 testing 两个入口，把所选模型的 `id`（`cmd_xxx`）加进既有 fetch body（后端 handler 已支持可选 `model` 字段，空=走 route）。管理员授权页在 `app/admin/users/page.tsx` 现有两列布局上加一个「模型授权」面板：勾选 `compute_model` 列表，保存调 `POST /users/:id/models`。全程用原生 `<select>`/`<input>` + Tailwind design token（本仓库**不是** shadcn 项目），与全站风格一致。

**Tech Stack:** Next.js 16（App Router）+ TypeScript + Tailwind v4（CSS 变量 design token）+ 原生 HTML 表单元素 + vitest。fetch 走全局拦截器（`lib/api.ts` 的 `installAuthInterceptor`，自动加 `Authorization`/`X-Project-Space-Id`），GET 用 `apiGet<T>`，类型化 POST/DELETE 可选 `apiClient`（openapi-fetch）。

## Global Constraints

- **不是 shadcn 项目**：无 `components/`、无 `<Select>` 组件。一律原生 `<select>`/`<input>` + design token className（`border-border`/`text-text-muted`/`bg-surface`/`bg-accent` 等）。全局表单兜底（`app/globals.css:58-69`）已让原生 `input/select` 自动跟主题。
- **后端统一信封** `{ code: number; data: T; message?: string }`，`code===0` 成功。
- **`lib/api-types.ts` 严禁手改**（CI swag regen 会覆盖 + `git diff --exit-code` 漂移红）；手维护类型放 `lib/api-types-manual.ts`。
- **模型标识符 = `compute_model.id`（`cmd_xxx`）**：Gateway 路径（requirement/qa）的 `req.Model`、授权表 `user_model_grant.model_id`、`IsGranted` 全程用 `cmd_xxx`。ModelSelect 的 option `value` 必须是 `Model.id`。**不要**用 `provider/name`（那是 opencode 路径的标识符，属 Plan 2b）。
- **fetch 鉴权**：用 `apiGet` 或裸 `fetch(${API_BASE_URL}...)`，全局拦截器自动带 token；不要手写 `Authorization`。
- **Next.js 16 有破坏性变更**：写客户端组件前先翻一眼 `platform/frontend/node_modules/next/dist/docs/` 相关指南（`AGENTS.md` 提示）。
- **提交**：Conventional Commits，body 每行 ≤100 字符，AI 提交带 `Co-authored-by: Claude <noreply@anthropic.com>`。
- **验证命令**（前端目录 `platform/frontend`）：`pnpm lint`（eslint）、`pnpm exec tsc --noEmit`（类型）、`pnpm test`（vitest）、`pnpm format`（prettier，已排除 api-types.ts）。无独立 typecheck 脚本，用 `tsc --noEmit`。

---

## File Structure

| 文件                                                 | 职责                                                                             | 动作 |
| ---------------------------------------------------- | -------------------------------------------------------------------------------- | ---- |
| `platform/frontend/lib/model-select.ts`              | ModelSelect 的纯逻辑：`AIModel` 类型、`modelLabel`、`pickDefaultModel`（可单测） | 新建 |
| `platform/frontend/lib/model-select.test.ts`         | 上述纯逻辑的 vitest 单测                                                         | 新建 |
| `platform/frontend/app/_components/model-select.tsx` | 受控下拉组件（数据加载 + 回退 + 渲染）                                           | 新建 |
| `platform/frontend/app/requirements/page.tsx`        | 需求规格生成入口：挂 ModelSelect + body 加 model                                 | 改   |
| `platform/frontend/app/testing/page.tsx`             | 测试生成入口：挂 ModelSelect + body 加 model                                     | 改   |
| `platform/frontend/app/admin/users/page.tsx`         | 管理员授权面板（per-user 模型勾选 + 保存）                                       | 改   |

**不在本期（Plan 2b）**：`dev/page.tsx`（opencode 标识符 `provider/name`，需与 codews 一并对齐）、`requirements/chat/page.tsx`（conversation handler 无 model 字段，需后端改造）、`workspace/workspace-frame.tsx`（codews 注入）。

---

## Task 1: ModelSelect 纯逻辑 + 组件

**Files:**

- Create: `platform/frontend/lib/model-select.ts`
- Create: `platform/frontend/lib/model-select.test.ts`
- Create: `platform/frontend/app/_components/model-select.tsx`

**Interfaces:**

- Produces: `AIModel` 类型、`modelLabel(m: AIModel): string`、`pickDefaultModel(granted: AIModel[], routePrimaryId: string): string`、`<ModelSelect value onChange taskType className />` 组件。后续 Task 2/3 消费组件。

- [ ] **Step 1: 写纯逻辑的失败测试**

Create `platform/frontend/lib/model-select.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { modelLabel, pickDefaultModel, type AIModel } from "./model-select";

const m = (over: Partial<AIModel> = {}): AIModel => ({
  id: "cmd_x",
  provider_id: "csp_x",
  name: "glm-5.1",
  ...over,
});

describe("modelLabel", () => {
  it("优先 display_name", () => {
    expect(modelLabel(m({ name: "glm-5.1", display_name: "GLM 5.1" }))).toBe("GLM 5.1");
  });
  it("无 display_name 回退 name", () => {
    expect(modelLabel(m({ name: "glm-5.1", display_name: undefined }))).toBe("glm-5.1");
  });
});

describe("pickDefaultModel", () => {
  it("授权列表非空取第一个 id", () => {
    const list = [m({ id: "cmd_a" }), m({ id: "cmd_b" })];
    expect(pickDefaultModel(list, "cmd_route")).toBe("cmd_a");
  });
  it("授权列表为空回退路由 primary", () => {
    expect(pickDefaultModel([], "cmd_route")).toBe("cmd_route");
  });
  it("都为空返回空串（后端走 route）", () => {
    expect(pickDefaultModel([], "")).toBe("");
  });
});
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd platform/frontend && pnpm test lib/model-select.test.ts`
Expected: FAIL（`Cannot find module './model-select'`）

- [ ] **Step 3: 写纯逻辑实现**

Create `platform/frontend/lib/model-select.ts`:

```ts
// ModelSelect 的纯逻辑（与 React 解耦，便于单测）。

export type AIModel = {
  id: string; // compute_model.id（cmd_xxx）—— 全程模型标识符
  provider_id: string;
  name: string;
  display_name?: string;
  modality?: string;
  enabled?: boolean;
};

// 显示名：优先 display_name，否则 name。
export function modelLabel(m: AIModel): string {
  return m.display_name || m.name;
}

// 默认选中值：有授权取第一个；无授权回退该 task_type 路由的 primary（可能为空）。
export function pickDefaultModel(granted: AIModel[], routePrimaryId: string): string {
  if (granted.length > 0) return granted[0].id;
  return routePrimaryId;
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd platform/frontend && pnpm test lib/model-select.test.ts`
Expected: PASS（5 tests）

- [ ] **Step 5: 写组件**

Create `platform/frontend/app/_components/model-select.tsx`:

```tsx
"use client";
import { useEffect, useState } from "react";
import { API_BASE_URL, apiGet } from "@/lib/api";
import { modelLabel, pickDefaultModel, type AIModel } from "@/lib/model-select";

type Envelope<T> = { code: number; message?: string; data: T };
type Route = { task_type: string; primary_model_id: string };

/**
 * ModelSelect — 当前用户已授权模型下拉（受控）。
 * 数据源 GET /users/me/models；空授权时回退到该 task_type 路由的 primary（取 /compute/routes 列表过滤）。
 * value/onChange 由父组件持有；taskType 决定空授权回退的默认模型来源。
 * option value = Model.id（cmd_xxx），与后端 Gateway/授权表标识符一致。
 */
export function ModelSelect({
  value,
  onChange,
  taskType,
  className,
}: {
  value: string;
  onChange: (v: string) => void;
  taskType: string;
  className?: string;
}) {
  const [models, setModels] = useState<AIModel[]>([]);
  const [fallback, setFallback] = useState(false);

  useEffect(() => {
    let cancelled = false;
    apiGet<Envelope<AIModel[]>>("/users/me/models")
      .then(async (r) => {
        if (cancelled) return;
        const granted = r.data ?? [];
        if (granted.length > 0) {
          setModels(granted);
          setFallback(false);
          if (!value) onChange(pickDefaultModel(granted, ""));
          return;
        }
        // 空授权：回退该 task_type 路由 primary
        setFallback(true);
        const rr = await apiGet<Envelope<Route[]>>("/compute/routes");
        if (cancelled) return;
        const primary =
          (rr.data ?? []).find((x) => x.task_type === taskType)?.primary_model_id ?? "";
        setModels(primary ? [{ id: primary, provider_id: "", name: primary }] : []);
        if (!value) onChange(primary);
      })
      .catch(() => setModels([]));
    return () => {
      cancelled = true;
    };
    // 仅 taskType 变化时重载；value 由父组件控制，不作为依赖。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [taskType]);

  return (
    <div className={className}>
      <label className="text-xs text-text-muted">模型</label>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-full rounded-md border border-border px-2 py-1.5 text-sm"
      >
        {models.length === 0 && <option value="">— 平台默认 —</option>}
        {models.map((m) => (
          <option key={m.id} value={m.id}>
            {modelLabel(m)}
          </option>
        ))}
      </select>
      {fallback && <span className="mt-0.5 block text-xs text-warn">未授权模型，使用平台默认</span>}
    </div>
  );
}
```

- [ ] **Step 6: 类型 + lint 检查**

Run: `cd platform/frontend && pnpm exec tsc --noEmit && pnpm lint`
Expected: 无错误（warning 可接受，但本组件不应引入新 warning）

- [ ] **Step 7: 提交**

```bash
cd platform/frontend
git add lib/model-select.ts lib/model-select.test.ts app/_components/model-select.tsx
git commit -m "feat(model-center): ModelSelect 组件（授权模型下拉 + 空授权回退）" -m "Co-authored-by: Claude <noreply@anthropic.com>"
```

---

## Task 2: 挂载到需求规格生成入口

**Files:**

- Modify: `platform/frontend/app/requirements/page.tsx`（generate() 约 `:99-134`，模型 UI 区约 `:230-263`）

**Interfaces:**

- Consumes: Task 1 的 `<ModelSelect value onChange taskType />`。
- 后端 `POST /project-spaces/:id/requirements` 的 `createRequest` 已支持 `model` 字段（`internal/requirement/handler.go:142`），空=走 route。

- [ ] **Step 1: 加 model state 与 ModelSelect**

在 `app/requirements/page.tsx` 顶部组件 state 区（与 `desc`/`loading` 同处，约 `:40-60` 区间，找现有 `useState` 集中处）加：

```tsx
const [model, setModel] = useState("");
```

在生成表单 UI（空间/应用选择那一行附近，约 `:230-263`）加 ModelSelect，与其它字段并排：

```tsx
<ModelSelect value={model} onChange={setModel} taskType="spec" className="min-w-[160px]" />
```

并在文件顶部 import：

```tsx
import { ModelSelect } from "@/app/_components/model-select";
```

- [ ] **Step 2: generate() body 加 model 字段**

修改 `generate()`（约 `:99-134`）的 fetch body，在 `application_id` 后加 `model`：

```tsx
const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/requirements`, {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({
    description: desc + textPart,
    images,
    application_id: selApp || undefined,
    model: model || undefined, // 空=后端走 route
  }),
});
```

- [ ] **Step 3: 类型 + lint 检查**

Run: `cd platform/frontend && pnpm exec tsc --noEmit && pnpm lint`
Expected: 无错误。

- [ ] **Step 4: 提交**

```bash
cd platform/frontend
git add app/requirements/page.tsx
git commit -m "feat(model-center): 需求规格生成挂 ModelSelect 并透传 model" -m "Co-authored-by: Claude <noreply@anthropic.com>"
```

---

## Task 3: 挂载到测试生成入口

**Files:**

- Modify: `platform/frontend/app/testing/page.tsx`（generate() 约 `:77-97`，模型 UI 区约 `:194-207`）

**Interfaces:**

- Consumes: Task 1 的 `<ModelSelect />`。
- 后端 `POST /project-spaces/:id/requirements/:rid/generate-tests` 的请求体已支持 `model`（`internal/qa/handler.go:72`），空=走 route。注意：当前前端该 fetch **无 body**，需新增 `{ model }`。

- [ ] **Step 1: 加 model state 与 ModelSelect**

在 `app/testing/page.tsx` state 区加：

```tsx
const [model, setModel] = useState("");
```

在测试生成表单 UI（约 `:194-207`）加：

```tsx
<ModelSelect value={model} onChange={setModel} taskType="test" className="min-w-[160px]" />
```

顶部 import：

```tsx
import { ModelSelect } from "@/app/_components/model-select";
```

- [ ] **Step 2: generate() 加 body 带 model**

修改 `generate()`（约 `:77-97`），把无 body 的 POST 改为带 body：

```tsx
const res = await fetch(
  `${API_BASE_URL}/project-spaces/${psID}/requirements/${rid}/generate-tests`,
  {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ model: model || undefined }),
  }
);
```

- [ ] **Step 3: 类型 + lint 检查**

Run: `cd platform/frontend && pnpm exec tsc --noEmit && pnpm lint`
Expected: 无错误。

- [ ] **Step 4: 提交**

```bash
cd platform/frontend
git add app/testing/page.tsx
git commit -m "feat(model-center): 测试生成挂 ModelSelect 并透传 model" -m "Co-authored-by: Claude <noreply@anthropic.com>"
```

---

## Task 4: 管理员模型授权面板

**Files:**

- Modify: `platform/frontend/app/admin/users/page.tsx`（两列布局 `:100`，用户卡片 `:129-151`，addMember 范式 `:75-88`）

**Interfaces:**

- Consumes: `GET /compute/models`（全量模型，`compute/page.tsx:82` 已用，裸 fetch + 本地类型）、`GET /users/:id/models`（某用户已授权，返回 `internal_compute.Model[]`）、`POST /users/:id/models` body `{ model_ids: string[] }`（`api-types.ts:7319` grantReq）、`DELETE /users/:id/models/:model_id`。
- 注意：授权端点 path 参数 `id` = `usr_xxx`（`u.id`），**不是** `u.name`。admin/users 页 `addMember` 的 selUser 用的是 `u.name`，本任务用独立的 `grantUserId`（`u.id`）避免混淆。

- [ ] **Step 1: 加授权面板 state 与数据加载**

在 `app/admin/users/page.tsx` 组件内（与现有 `users`/`selUser` state 同处）加：

```tsx
const [grantUserId, setGrantUserId] = useState(""); // usr_xxx
const [allModels, setAllModels] = useState<{ id: string; name: string; display_name?: string }[]>(
  []
);
const [grantedIds, setGrantedIds] = useState<Set<string>>(new Set());
const [grantMsg, setGrantMsg] = useState("");

// 加载全量模型（一次）
useEffect(() => {
  fetch(`${API_BASE_URL}/compute/models`)
    .then((r) => r.json())
    .then((r: Envelope<typeof allModels>) => setAllModels(r.data ?? []))
    .catch(() => {});
  // eslint-disable-next-line react-hooks/exhaustive-deps
}, []);

// 选中某用户时加载其已授权模型
useEffect(() => {
  if (!grantUserId) {
    setGrantedIds(new Set());
    setGrantMsg("");
    return;
  }
  fetch(`${API_BASE_URL}/users/${grantUserId}/models`)
    .then((r) => r.json())
    .then((r: Envelope<{ id: string }[]>) =>
      setGrantedIds(new Set((r.data ?? []).map((x) => x.id)))
    )
    .catch(() => setGrantedIds(new Set()));
  // eslint-disable-next-line react-hooks/exhaustive-deps
}, [grantUserId]);
```

（`Envelope<T>` 类型若本文件已有则复用，否则在文件顶部加 `type Envelope<T> = { code: number; data: T; message?: string };`。）

- [ ] **Step 2: 加勾选切换 + 保存逻辑**

```tsx
function toggleGrant(mid: string) {
  setGrantedIds((prev) => {
    const next = new Set(prev);
    if (next.has(mid)) next.delete(mid);
    else next.add(mid);
    return next;
  });
}

async function saveGrants() {
  if (!grantUserId) return;
  const res = await fetch(`${API_BASE_URL}/users/${grantUserId}/models`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ model_ids: Array.from(grantedIds) }),
  });
  const r = await res.json();
  setGrantMsg(r.code === 0 ? `✓ 已保存 ${grantedIds.size} 个模型` : `✗ ${r.message}`);
}
```

- [ ] **Step 3: 用户卡片加「模型授权」入口**

在每个用户卡片（约 `:129-151` 的 `{users.map((u) => (...))}` 块）内，role 徽章之后加一个按钮：

```tsx
<button
  onClick={() => setGrantUserId(grantUserId === u.id ? "" : u.id)}
  className={`ml-auto rounded px-1.5 py-0.5 text-xs ${grantUserId === u.id ? "bg-accent text-white" : "bg-surface-2"}`}
>
  模型授权
</button>
```

（`ml-auto` 把按钮推到卡片右端；外层 `flex items-center gap-2` 容器已有。）

- [ ] **Step 4: 渲染授权面板**

在用户目录列（左列，`users.map` 之后）追加授权面板，仅当 `grantUserId` 非空时显示：

```tsx
{
  grantUserId && (
    <div className="mb-3 rounded-lg border border-border bg-surface p-2">
      <div className="mb-1 text-xs font-medium">
        授权模型 — {users.find((u) => u.id === grantUserId)?.name}
      </div>
      <div className="max-h-48 overflow-auto">
        {allModels.map((m) => (
          <label key={m.id} className="flex items-center gap-2 py-0.5 text-sm">
            <input
              type="checkbox"
              checked={grantedIds.has(m.id)}
              onChange={() => toggleGrant(m.id)}
            />
            <span>{m.display_name || m.name}</span>
          </label>
        ))}
        {allModels.length === 0 && (
          <span className="text-xs text-text-muted">无可用模型（先在算力中心添加）</span>
        )}
      </div>
      <div className="mt-1 flex items-center gap-2">
        <button onClick={saveGrants} className="rounded bg-accent px-3 py-1 text-xs text-white">
          保存
        </button>
        {grantMsg && <span className="text-xs text-text-muted">{grantMsg}</span>}
      </div>
    </div>
  );
}
```

- [ ] **Step 5: 类型 + lint 检查**

Run: `cd platform/frontend && pnpm exec tsc --noEmit && pnpm lint`
Expected: 无错误。

- [ ] **Step 6: 提交**

```bash
cd platform/frontend
git add app/admin/users/page.tsx
git commit -m "feat(model-center): 管理员用户授权面板（勾选 compute_model 存授权）" -m "Co-authored-by: Claude <noreply@anthropic.com>"
```

---

## 完成判据（全任务后）

- [ ] `pnpm exec tsc --noEmit` 无错；`pnpm lint` 无新 error；`pnpm test` 全过。
- [ ] 推送后 CI Frontend (Next.js) job 绿（eslint + tsc via build + prettier check）。
- [ ] **手测（需用户在浏览器）**：
  1. 管理员在 `/admin/users` 给某用户勾选若干模型 → 保存 → 刷新仍勾选。
  2. 该用户登录，需求规格页 / 测试页的 ModelSelect 下拉只列被授权模型；选一个生成，后端用该模型（查日志确认 `req.Model` 非 route 默认）。
  3. 未授权任何模型的用户：下拉显示「未授权模型，使用平台默认」+ 回退该 task_type 路由 primary。

## Plan 2b（后续，不在本期）

- `dev/page.tsx`：opencode 路径模型标识符是 `provider/name`，需与 codews 一并对齐（先核验 opencode 是否遵循 `XDG_CONFIG_HOME`）。
- `requirements/chat/page.tsx`：conversation handler `messageRequest` 无 model 字段，需后端加 model 透传。
- `workspace/workspace-frame.tsx` + codews：按用户授权模型配置编码工具（per-session config 隔离、claude env 注入、`userID` 从 name 统一到 `usr_xxx`）。

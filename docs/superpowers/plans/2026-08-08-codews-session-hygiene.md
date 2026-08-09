# codews 会话治理（A+B）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让开发者能手动开干净的 opencode 新会话，并堵死后端重启后「按 workDir 把上个需求的臃肿会话捞回」的串台/浪费路径。

**Architecture:** 三个触点——① `SessionStore` 加只读 `LastSession` 查「该开发者×应用最近一次落库会话」；② `Ensure` 把 forceNew 决策收敛进纯函数 `computeForceNew`，新增请求级 `reqForceNew` 入参 + 冷启动（`old==nil`）查库补充判定，复用/kill/`ensureSession` 三处统一消费；③ 前端工具栏加「🆕 新会话」按钮，复用 `POST /workspace` 带 `force_new:true` 重新 boot。

**Tech Stack:** Go 1.25（gin + sqlx）/ Next.js 16（原生 HTML + Tailwind v4，**非 shadcn**）/ Postgres `codews_session` 表（迁移 000018 + 000020 已有 `requirement_id` 列，无需新迁移）。

**Scope（用户已确认）:** 仅 A（新会话按钮）+ B（重启跨需求 forceNew）。**C（token 超阈值提示）延后** —— 它依赖先在 .28 探测 opencode `/api/session` 的 token 字段名（仓库内仅 manager.go:331 注释称「响应含 token 统计」，字段名未知，结果不确定），作为独立后续 PR。

---

## Context

用户实测反馈（verbatim）：admin 点「启动」会加载 opencode，但 **opencode 加载了老进程**，新任务内容不可见，**老进程上下文已 10M+，浪费 token**。

根因（已从代码确认）：

1. **重启跨需求串台（B 要修的）**：`Ensure` 里 `forceNew := shouldForceNewForRequirement(old, reqID)`（manager.go:219）的 `old` 只来自内存 `m.sessions`。后端重启后内存为空 → `old==nil` → `shouldForceNewForRequirement` 返回 `false` → `ensureSession(port, workDir, false)`（manager.go:282）**按 workDir 把磁盘上上个需求的 opencode 会话捞回**（opencode 对话持久化在 `/root/.local/share/opencode`，1GB 共享 db）。新需求内容不在其中，且带着上个需求 10M+ 的臃肿上下文。
2. **无手动逃生口（A 要修的）**：用户无法主动开一个干净的空会话。前端从不发 `force_new`；后端 `Ensure` 无请求级 forceNew 入参；`Manager` 无 `Stop/Kill` 公开方法；`Handler.Stop`（handler.go:2023）是停 Docker 应用容器，与 codews 工作台进程无关。

关键控制流事实（已读 manager.go:204-298 确认）：

- 复用路径（221）与 kill 块（226）在 `m.mu` 锁内；`ensureSession(forceNew)`（282）与 `StartSession` 落库（289）在锁外，且**仅在新建路径**触达（复用在 223 提前 return）。
- 故 `codews_session` 表是「每次新建 boot 一行」，每行带当次 `requirement_id`。冷启动时查 `LastSession` 拿到的就是**上一次新建**绑的需求——正是判定串台的依据。
- `SessionStore` 当前**只由 `pgSessionStore` 实现**（无 mock）；`coverage_test.go` 用 Manager 时**不调** `SetSessionLogger`（`sessionLog==nil`），故新查询必须 `m.sessionLog != nil` 守卫。

---

## Global Constraints

- **Conventional commits**，body 每行 ≤100 字符，AI 提交带 `Co-authored-by: Claude`。
- **分支工作流**：`feat/codews-session-hygiene` 分支开发 → PR → CI 全绿 squash 合 main，**不直接推 main**。
- **前端非 shadcn**：原生 HTML 元素 + Tailwind v4 design tokens（`text-text-muted`/`bg-surface`/`border-border` 等），不加新组件库。
- **codews 包不得 import compute 包**：本改动不触及包边界，保持。
- **`lib/api-types.ts` 不得手编**：handler 若改 swag `@Param` body 注解 → 必须 `swag init` + openapi regen，CI 有 drift 检查。
- **后端测试**：`go test -p 1 -count=1 ./...`（共享 PG，串行避 TRUNCATE 踩踏）；`session_store_test` 走真实 PG（testutil.TestDB）。
- **前端测试**：Node 24；`tsc --noEmit` + `vitest` + `eslint` + `prettier --check`。
- **.28 部署**：只动 `deploy_` 前缀容器；SSH `ssh -o PubkeyAcceptedAlgorithms=+ssh-rsa -i ~/.ssh/miscode root@10.10.0.28`。

---

## File Structure

| 文件                                                         | 职责                 | 改动                                                                                                 |
| ------------------------------------------------------------ | -------------------- | ---------------------------------------------------------------------------------------------------- |
| `platform/backend/internal/codews/session_store.go`          | 会话持久化           | 加接口方法 `LastSession` + `pgSessionStore` 实现                                                     |
| `platform/backend/internal/codews/session_store_test.go`     | store 测试           | 加 `TestPGSessionStore_LastSession`                                                                  |
| `platform/backend/internal/codews/manager.go`                | 工作台生命周期       | 加纯函数 `computeForceNew`；`Ensure` 加 `reqForceNew` 入参 + 冷启动查库；复用/kill 统一消费 forceNew |
| `platform/backend/internal/codews/coverage_test.go`          | 决策+Ensure 测试     | 7 处 `Ensure(...)` 补 `, false`；加 `computeForceNew` 表驱动测试                                     |
| `platform/backend/internal/appdeploy/handler.go`             | `/workspace` handler | body 加 `ForceNew`；透传给 `Ensure`                                                                  |
| `platform/frontend/app/workspace/workspace-toolbar.tsx`      | 工具条               | 加 `onNewSession` prop + 「🆕 新会话」按钮                                                           |
| `platform/frontend/app/workspace/workspace-frame.tsx`        | boot 编排            | `forceNewRef`+`newSessionKey`；boot body 带 `force_new`；`onNewSession` 回调                         |
| `platform/frontend/app/workspace/workspace-toolbar.test.tsx` | 工具条测试           | 新增：按钮渲染+点击回调                                                                              |

---

## Task 1: SessionStore.LastSession 读方法（数据层）

**Files:**

- Modify: `platform/backend/internal/codews/session_store.go`
- Test: `platform/backend/internal/codews/session_store_test.go`

**Interfaces:**

- Produces: `SessionStore.LastSession(ctx, projectSpaceID, appID, userID) (*SessionRecord, error)` —— 返回最近一行（`started_at DESC LIMIT 1`），无历史返回 `(nil, nil)`。

- [ ] **Step 1: 写失败测试**

在 `session_store_test.go` 加（仿现有 `TestPGSessionStore_Lifecycle` 的 `db := testutil.TestDB(t)` 套路）：

```go
func TestPGSessionStore_LastSession(t *testing.T) {
	db := testutil.TestDB(t)
	store := NewPGSessionStore(db)
	ctx := context.Background()
	const ps, app, user = "ps_l", "app_l", "u_l"

	// 插两条同 (ps,app,user)、不同 requirement_id 的会话；把第一条 started_at 调早，
	// 保证 ORDER BY started_at DESC 确定性地返回第二条（避免微秒级同戳歧义）。
	rA := &SessionRecord{ProjectSpaceID: ps, AppID: app, UserID: user, Tool: "opencode", RepoDir: "/r", Port: 1, RequirementID: "reqA"}
	if err := store.StartSession(ctx, rA); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE codews_session SET started_at = started_at - INTERVAL '1 hour' WHERE id=$1`, rA.ID); err != nil {
		t.Fatal(err)
	}
	rB := &SessionRecord{ProjectSpaceID: ps, AppID: app, UserID: user, Tool: "opencode", RepoDir: "/r", Port: 2, RequirementID: "reqB"}
	if err := store.StartSession(ctx, rB); err != nil {
		t.Fatal(err)
	}

	got, err := store.LastSession(ctx, ps, app, user)
	if err != nil || got == nil {
		t.Fatalf("LastSession 欲返回最近会话，got=%v err=%v", got, err)
	}
	if got.RequirementID != "reqB" {
		t.Fatalf("LastSession 应返回最新(reqB)，实得 %s", got.RequirementID)
	}

	// 无历史 → (nil, nil)，非错误
	got2, err := store.LastSession(ctx, ps, app, "nobody")
	if err != nil || got2 != nil {
		t.Fatalf("无历史应返回 (nil,nil)，got=%v err=%v", got2, err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd platform/backend && go test -p 1 -count=1 -run TestPGSessionStore_LastSession ./internal/codews/`
Expected: FAIL / 编译错（`store.LastSession undefined`）。

- [ ] **Step 3: 实现 `LastSession`**

`session_store.go`：加 `"database/sql"` 到 import；接口加方法；`pgSessionStore` 加实现：

```go
type SessionStore interface {
	StartSession(ctx context.Context, s *SessionRecord) error
	FinishSession(ctx context.Context, id string, counts SessionCounts) error
	LastSession(ctx context.Context, projectSpaceID, appID, userID string) (*SessionRecord, error) // 最近一行；无历史返 (nil,nil)
}
```

```go
// LastSession 返回某开发者×应用最近一次落库的编码会话（started_at 倒序首条）。
// 用于后端重启后(old==nil)判定上次会话绑的需求：若与本次不同则强制新建，
// 避免 ensureSession 按 workDir 把上个需求的会话捞回（跨需求串台 + token 浪费）。
// 无历史行 → (nil, nil)（非错误）。userID 经 nullableStr：与 StartSession 写入口径一致。
func (p *pgSessionStore) LastSession(ctx context.Context, projectSpaceID, appID, userID string) (*SessionRecord, error) {
	var r SessionRecord
	err := p.db.GetContext(ctx, &r,
		`SELECT id, project_space_id, app_id, user_id, tool, repo_dir, port, session_id, requirement_id, started_at, ended_at, prompt_count, message_count
		 FROM codews_session
		 WHERE project_space_id=$1 AND app_id=$2 AND user_id=$3
		 ORDER BY started_at DESC LIMIT 1`,
		projectSpaceID, appID, nullableStr(userID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}
```

> 说明：`Ensure` 已把空 `userID` 兜底为 `"anonymous"`（manager.go:208），故查询时 `userID` 恒非空，`nullableStr` 返回字面量、与写入一致；返回行 `user_id` 非空，扫描进 `string` 字段安全。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd platform/backend && go test -p 1 -count=1 -run TestPGSessionStore_LastSession ./internal/codews/`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add platform/backend/internal/codews/session_store.go platform/backend/internal/codews/session_store_test.go
git commit -m "feat(codews): add SessionStore.LastSession for cold-start recovery"
```

---

## Task 2: forceNew 决策（A 请求级 + B 冷启动）+ Ensure 入参 + /workspace force_new

**Files:**

- Modify: `platform/backend/internal/codews/manager.go`
- Modify: `platform/backend/internal/codews/coverage_test.go`
- Modify: `platform/backend/internal/appdeploy/handler.go`

**Interfaces:**

- Consumes: `SessionStore.LastSession`（Task 1）
- Produces: `Ensure(..., reqForceNew bool)` 新尾参；纯函数 `computeForceNew`；`/workspace` body 新字段 `force_new`。

- [ ] **Step 1: 写 `computeForceNew` 失败测试**

`coverage_test.go` 加表驱动测试（纯函数，无需启进程）：

```go
func TestComputeForceNew(t *testing.T) {
	warm := &Session{RequirementID: "reqA"} // old != nil
	cases := []struct {
		name        string
		old         *Session
		reqID       string
		reqForceNew bool
		last        *SessionRecord
		want        bool
	}{
		{"请求级强制", nil, "reqA", true, nil, true},
		{"热切换需求", warm, "reqB", false, nil, true},
		{"热同需求不强制", warm, "reqA", false, nil, false},
		{"热无需求不强制", warm, "", false, nil, false},
		{"冷启动跨需求", nil, "reqB", false, &SessionRecord{RequirementID: "reqA"}, true},
		{"冷启动同需求不强制", nil, "reqA", false, &SessionRecord{RequirementID: "reqA"}, false},
		{"冷启动无历史不强制", nil, "reqA", false, nil, false},
		{"冷启动无需求不强制", nil, "", false, &SessionRecord{RequirementID: "reqA"}, false},
		{"冷启动历史无req不强制", nil, "reqB", false, &SessionRecord{RequirementID: ""}, false},
	}
	for _, c := range cases {
		if got := computeForceNew(c.old, c.reqID, c.reqForceNew, c.last); got != c.want {
			t.Errorf("%s: want %v got %v", c.name, c.want, got)
		}
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd platform/backend && go test -p 1 -count=1 -run TestComputeForceNew ./internal/codews/`
Expected: FAIL（`computeForceNew undefined`）。

- [ ] **Step 3: 实现 `computeForceNew` 纯函数**

`manager.go`，紧挨 `shouldForceNewForRequirement`（约 340 行）后加：

```go
// computeForceNew 综合判定是否强制新建 opencode 会话（跳过磁盘复用直接 initSession）：
//  1. 请求级显式新建 reqForceNew（前端「🆕 新会话」按钮）
//  2. 内存旧会话绑了不同需求（shouldForceNewForRequirement）
//  3. 冷启动 old==nil（后端重启后内存空）：库里最近会话(last)绑了不同需求
//
// last 仅冷启动时由调用方查库传入；非冷启动或未启用持久化时传 nil。
func computeForceNew(old *Session, reqID string, reqForceNew bool, last *SessionRecord) bool {
	if reqForceNew {
		return true
	}
	if shouldForceNewForRequirement(old, reqID) {
		return true
	}
	if old == nil && reqID != "" && last != nil && last.RequirementID != "" && last.RequirementID != reqID {
		return true
	}
	return false
}
```

- [ ] **Step 4: 改 `Ensure` 签名 + 决策 + 冷启动查库**

签名（manager.go:204）加尾参 `reqForceNew bool`：

```go
func (m *Manager) Ensure(psID, appID, repoDir, userID, toolName, reqID, model string, reqForceNew bool) (*Session, error) {
```

替换 manager.go:218-219（`old :=` + `forceNew := shouldForceNewForRequirement`）为：

```go
	old := m.sessions[key]
	forceNew := computeForceNew(old, reqID, reqForceNew, nil) // 先按内存+请求级；冷启动查库后重算（见解锁后）
```

复用路径（manager.go:221）加 `&& !forceNew`：

```go
	if s, exists := m.sessions[key]; exists && s.alive() && s.Tool == toolName && !forceNew && (reqID == "" || s.RequirementID == reqID) {
```

kill 块条件（manager.go:226）改为 `(old.Tool != toolName || forceNew)`（吸纳「换需求/请求强制」）：

```go
	if old, exists := m.sessions[key]; exists && old.cmd != nil && old.cmd.Process != nil && (old.Tool != toolName || forceNew) {
```

在 `m.mu.Unlock()`（manager.go:235）之后、`workDir := ensureWorktree(repoDir, userID)`（238）之前插入冷启动查库：

```go
	// 冷启动(后端重启→内存 old==nil)：查库最近会话绑的需求，若与本次不同则强制新建，
	// 避免 ensureSession 按 workDir 把上个需求的臃肿会话捞回（跨需求串台 + token 浪费）。
	if !forceNew && old == nil && reqID != "" && m.sessionLog != nil {
		if last, err := m.sessionLog.LastSession(context.Background(), psID, appID, userID); err != nil {
			log.Printf("[codews] 查最近会话失败(非致命): %v", err)
		} else {
			forceNew = computeForceNew(old, reqID, reqForceNew, last)
			if forceNew {
				log.Printf("[codews] 冷启动跨需求(库=%s 现=%s)→forceNew 新建会话", last.RequirementID, reqID)
			}
		}
	}
```

- [ ] **Step 5: 更新全部 `Ensure` 调用方**

- `appdeploy/handler.go`：body 结构体（262-266）加字段，调用（287）加 `in.ForceNew`：

```go
	var in struct {
		Tool          string `json:"tool"`
		RequirementID string `json:"requirement_id"`
		Model         string `json:"model,omitempty"`
		ForceNew      bool   `json:"force_new,omitempty"` // 前端「🆕 新会话」按钮：强制开空会话
	}
```

```go
	s, err := h.codeWS.Ensure(psID, aid, a.RepoDir, user, in.Tool, in.RequirementID, in.Model, in.ForceNew)
```

- `coverage_test.go`：7 处 `m.Ensure("ps_1", "app", "/tmp/repo", ...)` 调用，**每处末尾补 `, false`**。

- [ ] **Step 6: 运行测试**

Run: `cd platform/backend && go test -p 1 -count=1 ./internal/codews/ ./internal/appdeploy/`
Expected: PASS（含 `TestComputeForceNew` + 既有 coverage 全绿）。

- [ ] **Step 7: 编译 + vet + swag 一致性**

```bash
cd platform/backend
go build ./... && go vet ./...
# body 为匿名 inline struct，@Param 多半不引用具名 body 类型；若 swag 注解引用了 body 类型则需 regen：
swag init -g cmd/server/main.go -o docs --parseDependency --parseInternal
git diff --exit-code -- docs  # 若有 drift，把 regen 产物一并提交
```

- [ ] **Step 8: 提交**

```bash
git add platform/backend/internal/codews/manager.go platform/backend/internal/codews/coverage_test.go platform/backend/internal/appdeploy/handler.go platform/backend/internal/codews/session_store.go  # 若 swag regen 有产物一并加 docs/
git commit -m "feat(codews): force new session on request and cold-start cross-requirement"
```

---

## Task 3: 前端「🆕 新会话」按钮 + force_new 透传

**Files:**

- Modify: `platform/frontend/app/workspace/workspace-toolbar.tsx`
- Modify: `platform/frontend/app/workspace/workspace-frame.tsx`
- Test: `platform/frontend/app/workspace/workspace-toolbar.test.tsx`（新增）

**Interfaces:**

- Consumes: `/workspace` body 新字段 `force_new`（Task 2）
- Produces: 工具栏 `onNewSession: () => void` 回调。

- [ ] **Step 1: 写工具栏按钮失败测试**

新增 `workspace-toolbar.test.tsx`（沿用仓库既有组件测试的 import 套路；若仓库用 `@testing-library/react` 则照搬）：

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { WorkspaceToolbar } from "./workspace-toolbar";

describe("WorkspaceToolbar", () => {
  it("点击「🆕 新会话」触发 onNewSession", () => {
    const onNewSession = vi.fn();
    render(
      <WorkspaceToolbar
        appID="app"
        tool="opencode"
        model=""
        onModelChange={() => {}}
        deployState="idle"
        testUrl=""
        deployErr=""
        onDeploy={() => {}}
        onRegister={() => {}}
        registering={false}
        onOpenWindow={() => {}}
        onReconnect={() => {}}
        onNewSession={onNewSession}
        drawerOpen={false}
        onToggleDrawer={() => {}}
      />
    );
    fireEvent.click(screen.getByTitle("开新会话（丢弃当前上下文）"));
    expect(onNewSession).toHaveBeenCalledOnce();
  });
});
```

> 若仓库既有组件测试用了别的 render 套路，implementer 照既有模式对齐 import；断言不变。

- [ ] **Step 2: 运行确认失败**

Run: `pnpm --filter frontend test -- workspace-toolbar`
Expected: FAIL（`onNewSession` 未接线 / 按钮不存在）。

- [ ] **Step 3: 工具栏加按钮 + prop**

`workspace-toolbar.tsx`：props（9-41）加 `onNewSession: () => void;`；在「重连」按钮（82-84）后插入：

```tsx
<button
  onClick={onNewSession}
  className="text-accent"
  title="开新会话（丢弃当前上下文，需求内容用「🤖 AI 编码」重新注入）"
>
  🆕 新会话
</button>
```

- [ ] **Step 4: frame 接线 force_new + onNewSession**

`workspace-frame.tsx`：

- 顶部加（与 `reloadKey` 同区，约 28 行）：
  ```tsx
  const forceNewRef = useRef(false);
  const [newSessionKey, setNewSessionKey] = useState(0);
  ```
  （`useRef`/`useState` 已在文件 import 中；若缺 `useRef` 则补。）
- boot effect（150-159）的 `setTimeout` 回调开头捕获并立即重置 ref，body 带字段：
  ```tsx
      const timer = setTimeout(() => {
        const wantForceNew = forceNewRef.current;
        forceNewRef.current = false;
        fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/workspace`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            tool,
            requirement_id: selectedReq,
            model: modelRef.current || undefined,
            force_new: wantForceNew || undefined,
          }),
        })
  ```
- boot effect deps（185）加 `newSessionKey`：
  ```tsx
    }, [appID, psID, tool, reloadKey, newSessionKey, missingParams, selectedReq]);
  ```
- 工具栏 JSX（496-499 `onReconnect` 旁）加 `onNewSession`：

  ```tsx
        onNewSession={() => {
          if (!selectedReq) {
            setErr("请先选择需求");
            return;
          }
          if (!window.confirm("确认开启新会话？当前对话上下文不会带入新会话，需求内容请用「🤖 AI 编码」重新注入。")) {
            return;
          }
          forceNewRef.current = true;
          setUrl("");
          setNewSessionKey((k) => k + 1);
        }}
  ```

- [ ] **Step 5: 运行前端门禁**

```bash
pnpm --filter frontend exec tsc --noEmit
pnpm --filter frontend test
pnpm --filter frontend lint
pnpm --filter frontend exec prettier --check app/workspace
```

Expected: tsc clean；vitest 全绿（含新 toolbar 测试）；eslint 0 新 warning；prettier 通过。

- [ ] **Step 6: 提交**

```bash
git add platform/frontend/app/workspace/workspace-toolbar.tsx platform/frontend/app/workspace/workspace-frame.tsx platform/frontend/app/workspace/workspace-toolbar.test.tsx
git commit -m "feat(workspace): add 新会话 button to force fresh opencode session"
```

---

## Verification

**本地（需 PG）**

```bash
cd platform/backend && go test -p 1 -count=1 ./...
pnpm --filter frontend exec tsc --noEmit && pnpm --filter frontend test && pnpm --filter frontend lint
```

**CI 对齐**：push `feat/codews-session-hygiene` → 开 PR → 5 job 全绿（backend/frontend/python/openapi/security）。重点确认 openapi drift check 不红（Task 2 Step 7 已处理）。

**.28 手动 e2e（核心验收，直击用户痛点）**

1. 部署到 .28（方式 A 单文件或方式 B，排除 `data/` 与 `deploy/.env.prod`），重建 backend+frontend。
2. **B 验收（重启跨需求）**：admin 对某需求 reqA 启动工作台 → 注入/对话若干（产生 opencode 磁盘会话 + `codews_session` 行 requirement_id=reqA）→ `docker restart deploy_backend_1`（冷启动，内存清空）→ 切到需求 reqB 点「启动」→ 后端日志应见 `[codews] 冷启动跨需求(库=reqA 现=reqB)→forceNew`，opencode 打开**空的新会话**（非 reqA 的臃肿上下文）。
3. **A 验收（新会话按钮）**：任一已启动工作台 → 点「🆕 新会话」→ 确认 → opencode 重启为**空会话**；点「🤖 AI 编码」把当前需求注入新会话，内容可见。
4. 回归：同需求点「重连」仍复用现有会话（force_new=false，不误杀）。

**完成后**：用 `superpowers:finishing-a-development-branch` 收尾（默认走 PR，不直推 main）；更新记忆 [[model-center-user-grant]] 的 #15 状态。

---

## Defer（C，后续独立 PR）

token 超阈值提示。前置：在 .28 `curl` 一个活跃工作台的 `http://127.0.0.1:<port>/api/session`，dump 原始 JSON 确认 token 字段名（manager.go:331 注释称存在但字段名未知）。确认后：`ensureSession` 的解码 struct（manager.go:361-371）加字段 → 新增 `GET .../workspace/session-status` 返回 `{tokens, threshold, suggest_new}` → 前端轮询 + 超限横幅「建议开新会话」。若字段不可用，退化为 `LiveTranscript` 消息计数代理。

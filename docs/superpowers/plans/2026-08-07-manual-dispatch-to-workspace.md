# 需求派发改为「指派开发人员 + 人工工作台」实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `/requirements` 的「派发」从「启动 AI 全自动编码」改为「指派开发人员 + 确定应用 + 跳转 `/workspace` 人工工作台协同开发」；AI 全自动路径（`coder.Submit`）保留作未来自动化底座。

**Architecture:** 后端重定义 `service.Dispatch`（`EnsureApp` 拿 appID + `repo.Assign` 指派，**去掉 `coder.Submit`**，返回 appID）；`handler.DispatchCode` 返回 `workspace_url`；成员接口补 `name`（JOIN user）供选人显示。前端派发改为选人（成员接口）+ 跳转工作台；`/workspace` 支持 `req` 参数直达；新增「派给我的」入口。

**Tech Stack:** Go（gin + sqlx + PG）、Next.js（TS/React）、swag OpenAPI、前端类型手维护于 `lib/api-types-manual.ts`。

## Global Constraints

- **assignee 口径**：`requirement.assignee` 存**用户名 name**（= `auth.CtxUserID`，与 X-User 一致），**不是** `usr_xxx`。MyTasks/TeamTasks 按 name 匹配。前端选人必须用 member 的 **name**（故需 Task 1 给成员接口补 name）。
- 改后端 handler 须 `swag init` regen OpenAPI（`platform/backend` 下执行）。
- 改后端验证：`cd platform/backend && go test -p 1 -count=1 ./...`（避共享 DB 并行踩踏，见 auto-memory `anp-ci-pipeline`）。
- 前端验证：`cd platform/frontend && npx tsc --noEmit && npx eslint . && npx prettier --check .`；类型改 `lib/api-types-manual.ts`（手维护，防 regen 漂移）。
- commit：conventional commits，中文 body 可，**每行 ≤ 100 字符**；不直接推 main，本分支 `feat/manual-dispatch-to-workspace` 走 PR。
- `repo.Assign`（`requirement/repository.go:28`）已自带 `status='developing'` 且 SQL `OR assignee=$3` 允许**本人重复认领（幂等）**——开放点④天然满足，无需改 Assign。

## File Structure

**后端（Go）**

- `internal/auth/membership.go` — 改：`Member` 加 `Name`，`ListMembers` JOIN `user` 表
- `internal/requirement/service.go` — 改：`Dispatch` 重定义（去 `coder.Submit`，加 `assignee`，返回 appID）
- `internal/requirement/service_test.go` — 改：删/改旧 Dispatch 测试，加 fakeAppResolver + 新测试
- `internal/requirement/handler.go` — 改：`dispatchRequest`、`DispatchCode`、swagger 注释
- `internal/requirement/handler_test.go` — 改：加 `newHandlerWithApps`、`doJSONBody`、DispatchCode 测试
- `internal/auth/membership_test.go` — 新增：`ListMembers` 返回 name
- `docs/openapi.{json,yaml}` + `docs/docs.go` — swag regen（产物）

**前端（Next.js）**

- `app/requirements/page.tsx` — 改：派发选人 + 跳转、「派给我的」入口
- `app/workspace/workspace-frame.tsx` — 改：URL `req` 参数直达
- `lib/api-types-manual.ts` — 改：`dispatch-code` 请求/响应类型

---

### Task 1: 后端 — 成员接口补 `name`（选人显示名）

**Files:**

- Modify: `platform/backend/internal/auth/membership.go`
- Test: `platform/backend/internal/auth/membership_test.go`（新建）

**Interfaces:**

- Produces: `auth.Member{Name, UserID, ProjectSpaceID, Role}`；`GET /project-spaces/:id/members` 响应每项含 `name`（供 Task 4 前端选人）。

- [ ] **Step 1: 写失败测试**

新建 `platform/backend/internal/auth/membership_test.go`：

```go
package auth

import (
	"context"
	"testing"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// TestListMembers_NameJoin 成员列表 JOIN user 返回 name（供派发选人显示/指派）。
func TestListMembers_NameJoin(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "membership", `"user"`)
	// FK 前置：membership 引用 project_space；migrate 已建表，补一个 ps_default。
	db.MustExec(`INSERT INTO project_space (id, name, slug) VALUES ('ps_m', 'm', 'm')
		ON CONFLICT (id) DO NOTHING`)
	db.MustExec(`INSERT INTO "user" (id, name, email, status) VALUES ('usr_alice', 'alice', '', 'active')
		ON CONFLICT (id) DO NOTHING`)
	db.MustExec(`INSERT INTO membership (id, project_space_id, user_id, role)
		VALUES ('mbr_1', 'ps_m', 'usr_alice', 'dev')`)

	s := NewStore(db)
	list, err := s.ListMembers(context.Background(), "ps_m")
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(list) != 1 || list[0].Name != "alice" {
		t.Fatalf("期望 1 个成员 name=alice，得到: %#v", list)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd platform/backend && go test -run TestListMembers_NameJoin ./internal/auth/ -count=1`
Expected: FAIL（`list[0].Name` 为空，因当前 SQL 未 JOIN user、结构无 Name 字段 → 编译错或断言失败）。

- [ ] **Step 3: 改 `membership.go`（加 Name 字段 + JOIN）**

把 `Member` 结构改为（加 `Name`）：

```go
// Member 成员关系（用户 × 项目空间 × 角色）。
type Member struct {
	UserID         string `json:"user_id" db:"user_id"`
	Name           string `json:"name" db:"name"` // JOIN user 取的用户名（派发选人/指派用 name 口径）
	ProjectSpaceID string `json:"project_space_id" db:"project_space_id"`
	Role           string `json:"role" db:"role"` // business/dev/rule_architect/gatekeeper/admin
}
```

把 `ListMembers` 改为 JOIN `user`：

```go
// ListMembers 列出项目空间成员（含用户名 name，供派发选人）。
func (s *Store) ListMembers(ctx context.Context, projectSpaceID string) ([]Member, error) {
	var list []Member
	err := s.db.SelectContext(ctx, &list,
		`SELECT m.user_id, COALESCE(u.name,'') AS name, m.project_space_id, m.role
		 FROM membership m LEFT JOIN "user" u ON u.id = m.user_id
		 WHERE m.project_space_id = $1`, projectSpaceID)
	return list, err
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd platform/backend && go test -run TestListMembers_NameJoin ./internal/auth/ -count=1`
Expected: PASS

- [ ] **Step 5: 回归 auth 包**

Run: `cd platform/backend && go test ./internal/auth/ -count=1`
Expected: PASS（其他用到 `Member` 的地方只读 user_id/role，加字段不破坏）

- [ ] **Step 6: Commit**

```bash
git add platform/backend/internal/auth/membership.go platform/backend/internal/auth/membership_test.go
git commit -m "feat(auth): 成员列表 JOIN user 返回 name

供需求派发选人使用（assignee 存用户名 name 口径）。"
```

---

### Task 2: 后端 — 重定义 `service.Dispatch`（去自动编码 + 指派 + 返回 appID）

**Files:**

- Modify: `platform/backend/internal/requirement/service.go:162-196`（`Dispatch`）
- Test: `platform/backend/internal/requirement/service_test.go:162-185`（删/改两个旧 Dispatch 测试，新增）

**Interfaces:**

- Consumes: `AppResolver.EnsureAppForRequirement(ctx, psID, appName) (appID, repoDir, port, err)`（`service.go:27`）；`repo.Assign(ctx, id, user)`（自带 developing + 本人幂等）。
- Produces: `Service.Dispatch(ctx, psID, reqID, assignee) (appID string, err error)`（Task 3 handler 调用）。

- [ ] **Step 1: 改测试（先写新测试 + 删旧）**

在 `service_test.go` 末尾追加 fake + 新测试；**删除**旧的 `TestService_Dispatch_NoCoder`、`TestService_Dispatch_RequirementNotFound`（它们断言的"编码引擎未配置"路径已不存在）：

```go
// fakeAppResolver 测试用 AppResolver：EnsureApp 返回固定 appID（不碰真仓库）。
type fakeAppResolver struct{ appID string }

func (f *fakeAppResolver) ResolveApp(ctx context.Context, appID string) (string, int, error) {
	return "/tmp/repo", 0, nil
}
func (f *fakeAppResolver) EnsureAppForRequirement(ctx context.Context, psID, appName string) (string, string, int, error) {
	return f.appID, "/tmp/repo", 0, nil
}

// TestService_Dispatch_AssignsAndReturnsAppID 派发=EnsureApp+指派+返回appID，不调 coder。
func TestService_Dispatch_AssignsAndReturnsAppID(t *testing.T) {
	repo := newTestRepo(t)
	apps := &fakeAppResolver{appID: "app_disp1"}
	svc := NewService(repo, "", nil, nil, apps) // coder=nil 也不再阻断派发
	mustCreateRepo(t, repo, mkReq("req_disp", "ps_1"))

	appID, err := svc.Dispatch(context.Background(), "ps_1", "req_disp", "alice")
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if appID != "app_disp1" {
		t.Fatalf("appID 得 %s 想 app_disp1", appID)
	}
	got, _ := repo.Get(context.Background(), "req_disp")
	if got.Assignee != "alice" {
		t.Fatalf("assignee 得 %s 想 alice", got.Assignee)
	}
	if got.Status != "developing" {
		t.Fatalf("status 得 %s 想 developing", got.Status)
	}
}

// TestService_Dispatch_NoAssignee 派发必须指派开发人员。
func TestService_Dispatch_NoAssignee(t *testing.T) {
	repo := newTestRepo(t)
	svc := NewService(repo, "", nil, nil, &fakeAppResolver{appID: "app_x"})
	mustCreateRepo(t, repo, mkReq("req_nobody", "ps_1"))

	_, err := svc.Dispatch(context.Background(), "ps_1", "req_nobody", "")
	if err == nil || !strings.Contains(err.Error(), "指派") {
		t.Fatalf("空 assignee 应报含'指派'的错误，得到: %v", err)
	}
}

// TestService_Dispatch_RequirementNotFound 需求不存在→明确错误。
func TestService_Dispatch_RequirementNotFound(t *testing.T) {
	repo := newTestRepo(t)
	svc := NewService(repo, "", nil, nil, &fakeAppResolver{appID: "app_x"})
	_, err := svc.Dispatch(context.Background(), "ps_1", "req_missing", "alice")
	if err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("需求不存在应报错，得到: %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd platform/backend && go test -run TestService_Dispatch ./internal/requirement/ -count=1`
Expected: 编译失败（`Dispatch` 签名仍是旧的 6 参返 `*codetask.Task`）。

- [ ] **Step 3: 重写 `Dispatch`（service.go:162-196）**

把整个 `Dispatch` 方法替换为：

```go
// Dispatch 把需求派发给开发人员：确定/兜底创建托管应用 + 指派(assignee) + 进入开发通道。
// 不再启动 AI 自动编码——由开发人员在 /workspace 人工工作台协同 AI 开发；
// coder.Submit（自动编码）保留作未来自动化底座，此处不调用。
// 返回 appID 供前端跳转 /workspace?app=...。
func (s *Service) Dispatch(ctx context.Context, projectSpaceID, reqID, assignee string) (string, error) {
	if assignee == "" {
		return "", fmt.Errorf("请指派开发人员")
	}
	req, err := s.repo.Get(ctx, reqID)
	if err != nil {
		return "", fmt.Errorf("读取需求: %w", err)
	}
	if req == nil || req.ID == "" {
		return "", fmt.Errorf("需求 %s 不存在", reqID)
	}
	if s.apps == nil {
		return "", fmt.Errorf("应用解析器未配置")
	}
	var appID string
	if req.ApplicationID != "" {
		appID = req.ApplicationID // 已归属应用：直接用
	} else {
		// 未归属应用：兜底创建托管应用（req-<短id> ASCII 名）并绑定到需求。
		aid, _, _, e := s.apps.EnsureAppForRequirement(ctx, projectSpaceID, deriveAppName(req.Title, req.ID))
		if e != nil {
			return "", fmt.Errorf("为需求兜底创建托管应用失败: %w", e)
		}
		appID = aid
		_ = s.repo.SetApplication(ctx, req.ID, appID)
	}
	// 指派给开发人员。repo.Assign 自带 status=developing，且本人重复指派幂等。
	// 已被他人认领时 Assign 返回错误——派发者需先释放再改派（最小惊讶，不无声夺走）。
	if err := s.repo.Assign(ctx, reqID, assignee); err != nil {
		return "", err
	}
	return appID, nil
}
```

> 注意：原 `Dispatch` 里调 `buildCodePrompt`、`s.coder.Submit`、`UpdateStatus` 全部移除。`buildCodePrompt`/`deployableServiceHint` 仍被 `/dev` 手动编码路径的 prompt 构造引用（若仅 Dispatch 用则成 dead code，编译不报错；保留以备未来自动化接回）。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd platform/backend && go test -run TestService_Dispatch ./internal/requirement/ -count=1`
Expected: PASS（3 个新测试全过）

- [ ] **Step 5: 跑 requirement 整包**

Run: `cd platform/backend && go test ./internal/requirement/ -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add platform/backend/internal/requirement/service.go platform/backend/internal/requirement/service_test.go
git commit -m "feat(requirement): Dispatch 改为指派开发人员+返回appID

去掉 coder.Submit 自动编码，改为 EnsureApp+repo.Assign 指派，
供前端跳转人工工作台。自动能力保留作未来底座。"
```

---

### Task 3: 后端 — `handler.DispatchCode` 返回 workspace_url + swag regen

**Files:**

- Modify: `platform/backend/internal/requirement/handler.go:210-316`（`dispatchRequest` + `DispatchCode`）
- Test: `platform/backend/internal/requirement/handler_test.go`（加 helper + 测试）
- Regen: `platform/backend/docs/openapi.{json,yaml}`、`docs/docs.go`

**Interfaces:**

- Consumes: `Service.Dispatch(ctx, psID, reqID, assignee) (appID, err)`（Task 2 产出）。
- Produces: `POST /dispatch-code` body `{assignee}` → `{requirement_id, app_id, workspace_url}`。

- [ ] **Step 1: 加测试 helper（`handler_test.go`）**

在 `handler_test.go` 现有 helper 之后追加（`newHandlerWith` 不注入 apps，派发需要 apps；`doJSON` 不带 body）：

```go
// newHandlerWithApps 同 newHandlerWith，但注入 AppResolver（派发需要 EnsureApp）。
func newHandlerWithApps(t *testing.T, repo *Repository, chg *change.Store, apps AppResolver) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if u := c.GetHeader("X-User"); u != "" {
			c.Set("user_id", u)
		}
		c.Next()
	})
	svc := NewService(repo, "", nil, nil, apps)
	h := NewHandler(svc, chg, nil)
	h.Register(r.Group("/api/v1"))
	return r
}

// doJSONBody 发带 body 的请求并返回状态码 + 响应体。
func doJSONBody(t *testing.T, r http.Handler, method, target, xUser, body string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if xUser != "" {
		req.Header.Set("X-User", xUser)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var b map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &b)
	return w.Code, b
}
```

并追加测试（`fakeAppResolver` 已在 `service_test.go` 定义，同包可用）：

```go
// TestHandler_DispatchCode_ReturnsWorkspaceURL 派发→指派+返回 workspace_url。
func TestHandler_DispatchCode_ReturnsWorkspaceURL(t *testing.T) {
	repo, chg := newReqRepoWithChanges(t)
	mustCreateRepo(t, repo, mkReq("req_d1", "ps_1"))
	r := newHandlerWithApps(t, repo, chg, &fakeAppResolver{appID: "app_ws1"})

	code, body := doJSONBody(t, r, "POST",
		"/api/v1/project-spaces/ps_1/requirements/req_d1/dispatch-code",
		"lead", `{"assignee":"alice"}`)
	if code != 200 {
		t.Fatalf("code=%d body=%v", code, body)
	}
	d := dataOf(t, body)
	if d["app_id"] != "app_ws1" {
		t.Fatalf("app_id=%v 想 app_ws1", d["app_id"])
	}
	want := "/workspace?app=app_ws1&ps=ps_1&req=req_d1"
	if d["workspace_url"] != want {
		t.Fatalf("workspace_url=%v 想 %s", d["workspace_url"], want)
	}
	got, _ := repo.Get(context.Background(), "req_d1")
	if got.Assignee != "alice" {
		t.Fatalf("assignee=%s 想 alice", got.Assignee)
	}
}

// TestHandler_DispatchCode_NoAssignee 缺 assignee → 400。
func TestHandler_DispatchCode_NoAssignee(t *testing.T) {
	repo, chg := newReqRepoWithChanges(t)
	mustCreateRepo(t, repo, mkReq("req_d2", "ps_1"))
	r := newHandlerWithApps(t, repo, chg, &fakeAppResolver{appID: "app_ws2"})
	code, _ := doJSONBody(t, r, "POST",
		"/api/v1/project-spaces/ps_1/requirements/req_d2/dispatch-code",
		"lead", `{}`)
	if code != 400 {
		t.Fatalf("缺 assignee 应 400，得到 %d", code)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd platform/backend && go test -run TestHandler_DispatchCode ./internal/requirement/ -count=1`
Expected: FAIL（`dispatchRequest` 仍要求 `repo_dir`/无 assignee，或返回体无 `workspace_url`）。

- [ ] **Step 3: 改 `handler.go`（`dispatchRequest` + `DispatchCode`）**

把 `dispatchRequest`（handler.go:210）改为：

```go
type dispatchRequest struct {
	Assignee string `json:"assignee" binding:"required"` // 派发给的开发人员（用户名 name）
}
```

把 `DispatchCode`（handler.go:293-316）整体替换为：

```go
// DispatchCode 需求 → 指派开发人员 + 确定应用 + 返回人工工作台 URL（不再自动编码）。
//
// @Summary      派发给开发人员（进编码工作台）
// @Tags         requirement
// @Accept       json
// @Produce      json
// @Param        id    path  string           true  "项目空间ID"
// @Param        rid   path  string           true  "需求ID"
// @Param        body  body  dispatchRequest  true  "指派人 assignee"
// @Success      200  {object}  map[string]interface{}  "app_id/workspace_url"
// @Failure      400  {object}  map[string]interface{}  "invalid body"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/requirements/{rid}/dispatch-code [post]
func (h *Handler) DispatchCode(c *gin.Context) {
	var in dispatchRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	psID := c.Param("id")
	rid := c.Param("rid")
	appID, err := h.svc.Dispatch(c.Request.Context(), psID, rid, in.Assignee)
	if err != nil {
		httpx.Err(c, 500, 50004, err.Error())
		return
	}
	httpx.OK(c, gin.H{
		"requirement_id": rid,
		"app_id":         appID,
		"workspace_url":  fmt.Sprintf("/workspace?app=%s&ps=%s&req=%s", appID, psID, rid),
	})
}
```

> `handler.go` 顶部若 `errors` 包仅为 `errors.Is(ErrActiveTaskConflict)` 而导入，且此时已无其他用处，移除 `"errors"` import；`dev` 包仍因 `Register` 签名（`*dev.CodingAgent`）需要保留。加 `"fmt"` import（若未有）。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd platform/backend && go test -run TestHandler_DispatchCode ./internal/requirement/ -count=1`
Expected: PASS

- [ ] **Step 5: 跑整包 + swag regen**

Run: `cd platform/backend && go test ./internal/requirement/ -count=1`
Expected: PASS

Regen OpenAPI（`swag` 已在 CI 用）：

```bash
cd platform/backend && swag init -g cmd/server/main.go -o docs --parseDependency --parseInternal
```

Expected: `docs/openapi.json`、`docs/swagger.yaml`、`docs/docs.go` 更新（`dispatch-code` 描述/参数变更）。

- [ ] **Step 6: 全量后端回归**

Run: `cd platform/backend && go test -p 1 -count=1 ./...`
Expected: 全包 PASS（含 mwsupply 等串行隔离）

- [ ] **Step 7: Commit**

```bash
git add platform/backend/internal/requirement/handler.go \
  platform/backend/internal/requirement/handler_test.go \
  platform/backend/docs/openapi.json platform/backend/docs/swagger.yaml \
  platform/backend/docs/docs.go
git commit -m "feat(requirement): dispatch-code 返回 workspace_url 指向人工工作台

body 改 {assignee}，返回 app_id+workspace_url；swag regen。"
```

---

### Task 4: 前端 — `/requirements` 派发选人 + 跳转

**Files:**

- Modify: `platform/frontend/app/requirements/page.tsx`（`dispatch()`、state、列表按钮）
- Verify: `cd platform/frontend && npx tsc --noEmit && npx eslint app/requirements/page.tsx`

**Interfaces:**

- Consumes: `GET /project-spaces/:id/members` → `{user_id, name, role}[]`（Task 1）；`POST /dispatch-code` body `{assignee}` → `{workspace_url}`（Task 3）。

- [ ] **Step 1: 加成员 + 当前用户 state，加载它们**

在 `page.tsx` 顶部 state 区（约 `const [apps, setApps]` 附近）加：

```tsx
type Member = { user_id: string; name: string; role: string };
const [members, setMembers] = useState<Member[]>([]);
const [selAssignee, setSelAssignee] = useState(""); // 派发给谁（name 口径）
const [me, setMe] = useState(""); // 当前登录用户名（name）
```

在 `useEffect`（加载 spaces 的那个，约第 40 行）里追加加载当前用户；在 `loadList` 里追加加载成员。把 `loadList` 改为：

```tsx
const loadList = (id: string) => {
  if (!id) return;
  fetch(`${API_BASE_URL}/project-spaces/${id}/requirements`)
    .then((r) => r.json())
    .then((r: Envelope<Requirement[]>) => setList(r.data ?? []))
    .catch(() => {});
  fetch(`${API_BASE_URL}/project-spaces/${id}/apps`)
    .then((r) => r.json())
    .then((r: Envelope<App[]>) => setApps(r.data ?? []))
    .catch(() => {});
  fetch(`${API_BASE_URL}/project-spaces/${id}/members`)
    .then((r) => r.json())
    .then((r: Envelope<Member[]>) => setMembers(r.data ?? []))
    .catch(() => {});
};
```

并在 spaces 的 `useEffect` 里加（取当前用户名，用于判断派发给自己时跳转）：

```tsx
fetch(`${API_BASE_URL}/auth/me`)
  .then((r) => r.json())
  .then((r: Envelope<{ user: string }>) => setMe(r.data?.user ?? ""))
  .catch(() => {});
```

> 若 `/auth/me` 返回结构不同，以 `platform/backend/internal/auth/handler.go` 的 `me` handler 实际返回为准（当前返回 `{user: CtxUserID}`）。

- [ ] **Step 2: 改 `dispatch()`（选人 + 跳转/提示）**

把 `dispatch`（page.tsx:136-169）整体替换为：

```tsx
async function dispatch(rid: string) {
  if (!psID || !selAssignee || dispatching || dispatchingRef.current) return;
  dispatchingRef.current = true;
  setDispatching(rid);
  setMsg("");
  try {
    const res = await fetch(
      `${API_BASE_URL}/project-spaces/${psID}/requirements/${rid}/dispatch-code`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ assignee: selAssignee }),
      }
    );
    const r = await res.json();
    if (r.data?.workspace_url) {
      if (selAssignee === me) {
        // 派给自己：直达该需求工作台
        window.location.href = r.data.workspace_url;
        return;
      }
      setMsg(`✅ 已派发给「${selAssignee}」。其可在「研发工作台」从「派给我的」进入编码工作台`);
    } else {
      setMsg(`✗ ${r.message ?? "派发失败"}`);
    }
  } catch (e) {
    setMsg(`✗ ${e}`);
  } finally {
    setDispatching("");
    dispatchingRef.current = false;
  }
}
```

- [ ] **Step 3: 加「派发给」选择控件 + 改按钮文案**

在「归属应用」选择控件（约 page.tsx:245-262）之后，加一个成员选择：

```tsx
<div>
  <label className="text-xs text-text-muted">派发给（开发人员）</label>
  <select
    value={selAssignee}
    onChange={(e) => setSelAssignee(e.target.value)}
    className="ml-2 rounded-md border border-border px-2 py-1 text-sm"
  >
    <option value="">— 选择开发人员 —</option>
    {members.map((m) => (
      <option key={m.user_id} value={m.name}>
        {m.name}（{m.role}）
      </option>
    ))}
  </select>
  {members.length === 0 && <span className="ml-2 text-xs text-text-muted">该项目空间暂无成员</span>}
</div>
```

把列表项的派发按钮文案（page.tsx:400）`"⚡ 派发编码"` 改为 `"👤 派发给开发"`；最新生成卡片里的按钮（page.tsx:353）`"⚡ ② 派发编码"` 改为 `"👤 派发给开发"`。两个派发按钮 `disabled` 条件追加 `!selAssignee`，例如：

```tsx
<button
  onClick={() => dispatch(r.id)}
  disabled={!!dispatching || !selAssignee}
  className="rounded bg-success px-2 py-1 text-xs text-white disabled:opacity-50"
  title={selAssignee ? `派发给 ${selAssignee}` : "先选开发人员"}
>
  {dispatching === r.id ? "派发中…" : "👤 派发给开发"}
</button>
```

- [ ] **Step 4: 类型检查 + lint**

Run: `cd platform/frontend && npx tsc --noEmit && npx eslint app/requirements/page.tsx && npx prettier --check app/requirements/page.tsx`
Expected: 无错（有则按提示修，常见：未用 import、`any`）

- [ ] **Step 5: Commit**

```bash
git add platform/frontend/app/requirements/page.tsx
git commit -m "feat(frontend): 需求派发改为选开发人员+跳转工作台

dispatch 调 /members 选人、body 带 assignee(name)，派给自己则
跳 /workspace；按钮改「派发给开发」。"
```

---

### Task 5: 前端 — `/workspace` 支持 `req` 参数直达

**Files:**

- Modify: `platform/frontend/app/workspace/workspace-frame.tsx`（读 URL `req`，自动选中+认领）

**Interfaces:**

- Consumes: URL `?req=<rid>`；现有 `onStartReq` 认领逻辑（workspace-frame.tsx:495）。

- [ ] **Step 1: 读 `req` 并在首次加载时自动认领选中**

在 `workspace-frame.tsx` 顶部 `sp.get` 区（约第 19-22 行）加：

```tsx
const initialReq = sp.get("req") || "";
```

在首次加载 detail 的 `useEffect`（约第 106-131 行）的 `.then` 成功分支里，若 `initialReq` 且未选中，触发认领（复用 `onStartReq` 的内联逻辑；为避免重复，抽一个 `startReq` 函数）。把 `onStartReq` 内联逻辑抽成可在 effect 调用的函数：

在组件内（`fetchDetail` 之后）加一个 `startReq`：

```tsx
const startReq = useCallback(
  async (id: string) => {
    try {
      const r = await fetch(`${API_BASE_URL}/project-spaces/${psID}/requirements/${id}/assign`, {
        method: "POST",
      }).then((rr) => rr.json());
      if (r.code !== 0) {
        setErr(r.message || "认领失败");
        return;
      }
    } catch (e) {
      setErr(String(e));
      return;
    }
    setSelectedReq(id);
    setTaskMsg("");
    setTestMsg("");
    setTestResults(null);
    setSubmitMsg("");
    try {
      setSubtasks(JSON.parse(detail?.requirements?.find((q) => q.id === id)?.tasks || "[]"));
    } catch {
      setSubtasks([]);
    }
    fetchDetail();
  },
  [psID, detail, fetchDetail]
);
```

把原 `onStartReq={(id) => {...}}`（Sidebar prop，约 495 行）改为 `onStartReq={startReq}`。

在首次加载 detail 的 `useEffect` 成功分支末尾加自动选中：

```tsx
if (initialReq && !selectedReq) {
  startReq(initialReq);
}
```

> `startReq` 对"已 assignee=本人"幂等（后端 `repo.Assign` 允许同人重复，开放点④）。需把 `initialReq`、`selectedReq`、`startReq` 加入该 effect 依赖数组；为避免 effect 反复触发，可用一个 `useRef` 守卫（`startedRef`）确保只自动认领一次。

- [ ] **Step 2: 类型检查 + lint**

Run: `cd platform/frontend && npx tsc --noEmit && npx eslint app/workspace/workspace-frame.tsx && npx prettier --check app/workspace/workspace-frame.tsx`
Expected: 无错

- [ ] **Step 3: Commit**

```bash
git add platform/frontend/app/workspace/workspace-frame.tsx
git commit -m "feat(frontend): workspace 支持 req 参数直达需求

派发跳转带 req 时自动认领+启动该需求工作台；抽 startReq 复用。"
```

---

### Task 6: 前端 — 「派给我的」入口 + 类型更新

**Files:**

- Modify: `platform/frontend/app/requirements/page.tsx`（列表项：派给我的需求加「进工作台」）
- Modify: `platform/frontend/lib/api-types-manual.ts`（dispatch-code 类型）

**Interfaces:**

- Consumes: 需求 `assignee`/`status`/`application_id`（列表已有）；`me`（Task 4）。

- [ ] **Step 1: 列表项加「进编码工作台」按钮（派给我的）**

在 `page.tsx` 需求列表项的按钮区（Task 4 改过的地方），对 `r.assignee === me && r.status === "developing"` 的需求，加一个跳转按钮（需要 `application_id`，派发后必有）：

```tsx
{
  r.assignee === me && r.status === "developing" && r.application_id && (
    <a
      href={`/workspace?app=${r.application_id}&ps=${psID}&req=${r.id}`}
      className="rounded bg-accent px-2 py-1 text-xs text-white"
      title="进入该需求的编码工作台，协同 AI 开发"
    >
      💻 进编码工作台
    </a>
  );
}
```

- [ ] **Step 2: 更新 `api-types-manual.ts` 的 dispatch-code 类型**

打开 `lib/api-types-manual.ts`，把 `dispatch-code` 相关请求/响应类型改为（手维护，匹配 Task 3 后端契约）：

```ts
// 派发给开发人员（进编码工作台）—— 不再自动编码
export interface DispatchCodeRequest {
  assignee: string; // 开发人员用户名 name
}
export interface DispatchCodeResponse {
  requirement_id: string;
  app_id: string;
  workspace_url: string; // /workspace?app=&ps=&req=
}
```

> 若该文件用其他命名/组织方式，按其现有风格调整；关键是把旧的 `{repo_dir, model}` 请求与 `{task_id, status}` 响应替换为上述契约。

- [ ] **Step 3: 类型检查 + lint + prettier**

Run: `cd platform/frontend && npx tsc --noEmit && npx eslint . && npx prettier --check .`
Expected: 无错

- [ ] **Step 4: Commit**

```bash
git add platform/frontend/app/requirements/page.tsx platform/frontend/lib/api-types-manual.ts
git commit -m "feat(frontend): 派给我的需求加进工作台入口 + 类型更新

列表对 assignee=me&&developing 加「进编码工作台」跳转；
api-types-manual 同步 dispatch-code 新契约。"
```

---

### Task 7: 全栈回归与手动验证

**Files:** 无（验证 only）

- [ ] **Step 1: 后端全量测试**

Run: `cd platform/backend && go test -p 1 -count=1 ./...`
Expected: 全包 PASS

- [ ] **Step 2: 前端构建**

Run: `cd platform/frontend && npx tsc --noEmit && npx eslint . && npx prettier --check .`
Expected: 无错

- [ ] **Step 3: 本地起服务，手动验流程**（需 PG；见 auto-memory `anp-backend-local-run`，用 `scripts/dev.sh` 或 `backend/.env`）

验证清单：

1. `/requirements` 创建需求 → 列表出现「👤 派发给开发」按钮（未选人时 disabled）
2. 选开发人员 → 点「派发给开发」→ 派给自己时跳 `/workspace`，否则提示「已派发给 X」
3. 被指派人进 `/workspace` → 自动落到该需求、启动 opencode 工作台
4. 在工作台「🤖AI编码」按子任务派 AI、实时可介入（现有能力，回归无破坏）
5. `/dev` 手动起编码任务仍可跑（`coder.Submit` 路径未动）
6. `/approvals` 审批链不受影响

- [ ] **Step 4: 推分支开 PR**

```bash
git push -u origin feat/manual-dispatch-to-workspace
gh pr create --title "feat: 需求派发改为指派开发人员+人工工作台" \
  --body "派发从启动AI自动编码改为指派开发人员+跳转/workspace人工工作台协同开发；自动路径保留作未来底座。spec: docs/superpowers/specs/2026-08-07-manual-dispatch-to-workspace-design.md"
```

---

## Self-Review

**1. Spec 覆盖**

- §4.1 状态机（specified→developing→delivered，Assign 自带 developing）→ Task 2/3 实现 + 测试断言 status。✓
- §4.2 service.Dispatch 重定义 → Task 2。✓
- §4.3 handler 返回 workspace_url + swag → Task 3。✓
- §4.4 前端选人 + 跳转 → Task 4。✓
- §4.5 /workspace req 直达 → Task 5。✓
- §4.6 「派给我的」入口 → Task 6。✓
- §6 开放点①（已指派再派发）→ Task 2 Step3 注释明确"不无声夺走，先释放再改派"。✓
- §6 开放点②（选人数据源）→ Task 1（成员接口补 name）+ Task 4（调 /members）。✓
- §6 开放点③（app 参数恒可用，EnsureApp 兜底）→ Task 2 EnsureApp 分支。✓
- §6 开放点④（/assign 幂等）→ 已天然满足（Assign SQL 同人可重复），Task 5 Step1 注明。✓
- §5 保留项（coder.Submit / /dev / approvals 不动）→ Task 7 Step5/6 回归。✓

**2. 占位符扫描**：无 TBD/TODO；helper 依赖（`newTestRepo`/`mkReq`/`mustCreateRepo`/`mustAssign`/`testutil.TestDB`/`testutil.Truncate`）均来自现有测试文件，已命名。

**3. 类型一致**：`Dispatch(ctx, psID, reqID, assignee) (appID, err)` 在 Task 2 定义、Task 3 调用一致；`Member.Name` 在 Task 1 定义、Task 4 消费一致；`workspace_url` 拼接格式（`/workspace?app=&ps=&req=`）Task 3 产出、Task 4/5/6 消费一致；`assignee` 全程 name 口径一致。

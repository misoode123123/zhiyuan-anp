# 应用全景视图 P1-c（全景聚合 + 前端看板）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 应用详情从「需求/变更/发布三栏」升级为五列全景看板——一个应用的需求/研发(编码会话+异步任务+变更+git)/部署(实例+URL+部署需求 needs★)/运行(资源+健康)/依赖全维度一目了然。后端 `Detail()` 聚合 codews_session/appdeploy_route/code_task/mwsupply 四源 + `.anp/deploy.yaml` needs。

**Architecture:** 后端单一 `AppFullView` 结构（原 `AppDetail` 改名+扩字段）。sessions/routes/tasks 三源是共享 DB 上的 SQL，作 `appdeploy.Store` 方法（避免跨包 store 依赖）；Deps 经 `MWReconciler.ListDeps` 接口在 **handler 层**填（appdeploy→mwsupply 是禁止依赖方向，沿用既有接口注入模式）；deploy needs 经 `LoadDeployManifest(a.RepoDir)` best-effort 读。前端把现有内联三列 grid（`page.tsx:1885`）重排为五列。

**Tech Stack:** Go（sqlx + PG）、`testing`（testutil.TestDB 跑迁移）、Next.js + TypeScript（client component）。

## Scope Note

只覆盖 P1-c。**部署需求 needs 显示为只读**（★ 标注「权威输入」）；needs 的**编辑**走既有 opencode 自治修复（[[opencode-self-heals-app-config]]），不在此期建编辑端点（follow-up）。P2（opencode 备料）、P3（部署历史/监控/健康 SLA 采集，需新表）另起计划。P1-c **不改表**。

## Global Constraints

- **不改表结构**（零迁移）。codews_session/appdeploy_route/code_task 表与列已齐备。
- **conventional commits**：type(scope): subject，中文 body 可，**body 每行 ≤ 100 字符**，`Co-Authored-By: Claude <noreply@anthropic.com>`。main 线性 ff（`git merge --ff-only feat/app-panorama-p1c`）。
- **分支**：`feat/app-panorama-p1c`。
- **后端测试**：`cd platform/backend && go test -p 1 -count=1 ./internal/appdeploy/...`（PG service 容器 + pgvector；testutil 跑迁移建全表）。全量 `go test -p 1 -count=1 ./...` 作 T3 闸。
- **前端验证**：`cd platform/frontend && pnpm exec tsc --noEmit`。
- **依赖方向**：appdeploy 不 import performance/codetask/mwsupply 的 store（用 SQL 直查共享表 / 接口注入）。appgw 已是 appdeploy 既有依赖（RouteWriter），可 import 但本计划用 SQL 避免。
- **module 路径**：`zhiyuan-anp/platform/backend`。前端无 `src/`，App Router 直接 `platform/frontend/app/`。

---

## File Structure

| 文件                                        | 改动                                                                                                                                                                                                                                        |
| ------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `internal/appdeploy/detail.go`              | `AppDetail`→`AppFullView` 改名+加 5 字段(Sessions/Tasks/Routes/Deps/DeployNeeds)；加 `AppSession/AppTask/AppRoute` 类型；加 3 store 方法 `ListSessionsByApp/ListRoutesByApp/ListTasksByApp`；`Detail()` 聚合 + nil 归一化 + 读 deploy needs |
| `internal/appdeploy/detail_test.go`（新建） | DB 集成测试：seed app+session+route+change+task，断言 `Detail()` 各维度聚合                                                                                                                                                                 |
| `internal/appdeploy/handler.go`             | `Detail` handler 在 store.Detail 后经 `h.mwReconciler.ListDeps` best-effort 填 Deps                                                                                                                                                         |
| `app/applications/page.tsx`                 | 扩内联 `Detail` 类型(加 sessions/tasks/routes/deps/deploy_needs)；三列 grid(:1885)→五列全景                                                                                                                                                 |

不改：store.go 既有方法、前端其它页、迁移。

---

## 事实基线（已核实）

1. **`Store.Detail`**（`detail.go:53`）现返 `*AppDetail{Application,Requirements,Changes,Releases,Commits,Instances}`（:10-17）；`:91-105` nil→空切片归一化（防 JSON null 致前端 `.length` 崩）——**新字段必须复刻**。
2. **Handler.Detail**（`handler.go:227-234`）：`d, err := h.store.Detail(...)` → `httpx.OK(c, d)`。端点 `GET /project-spaces/:id/apps/:aid/detail`（:147）。
3. **Handler 持有**：`store *Store`、`mwReconciler MWReconciler`（含 `ListDeps(ctx,appID)([]DepDeclaration,error)`，handler.go:93）、`codeWS`、`routeWriter`。`SetMwReconciler` 注入（:102）。
4. **codews_session**（迁移 000018）：`app_id TEXT NOT NULL`；列 id/project_space_id/app_id/user_id?/tool/repo_dir/port/session_id/started_at/ended_at?/prompt_count/message_count/created_at。**无 app_id 索引**（查询量小，不补索引，作 follow-up）。
5. **appdeploy_route**（迁移 000006+000010）：`app_id REFERENCES appdeploy_application(id) ON DELETE CASCADE`；列 id/app_id/project_space_id/app_code/env/upstream_host/upstream_port/status/auth_required/external_url(default '')/created_at/updated_at。`UNIQUE(app_code,env)`。
6. **code_task**（codetask/model.go:7-23）：无 app_id。派生路径 `task.change_id → change_request.id → change_request.source_id = appID`（直登）或 `→ requirement.application_id = appID`（派发）。镜像 codetask/store.go:44-54 的 JOIN。
7. **`DepDeclaration`**（deps.go:4-11）：Kind/Strategy/Status/Instance?/Token?/Error?。
8. **`NeedsSpec`**（deploy_manifest.go）：Mounts/EnvKeys/Ports/Command。`LoadDeployManifest(repoDir)(*DeployManifest,error)`（不存在返 nil,nil）。
9. **测试模式**（store_test.go:13-23）：`newTestStore(t)`=`testutil.TestDB(t)` 跑迁移+`testutil.Truncate(t,db,"appdeploy_env","appdeploy_instance","appdeploy_application")`+`NewStore(db)`；`mkApp(ps,name)`+`s.Create(ctx,a)` 填 `a.ID`。同包测试可访问 `s.db`。
10. **前端**（page.tsx 2082 行，client）：内联 `Detail` 类型(:78-97，无 sessions/tasks/routes/deps/needs)；三列 grid(:1885-1978) 需求/变更/发布，后接登记变更表单(:1979+)；`appStats`(Instance 资源/健康,:518)已每 30s 轮询(:613-627)；`STATUS_COLOR`(:99)/`healthBadge`(:124) 可复用；`Dep`/`Instance` 类型已定义(:14,:56)。

---

### Task 1: 后端 AppFullView 聚合（detail.go + handler.go + test）

**Files:**

- Modify: `platform/backend/internal/appdeploy/detail.go`（改名+扩字段+3 方法+聚合）
- Modify: `platform/backend/internal/appdeploy/handler.go`（Detail handler 填 Deps）
- Create: `platform/backend/internal/appdeploy/detail_test.go`

**Interfaces:**

- Consumes: `LoadDeployManifest`、`testutil.TestDB/Truncate`、既有 `Store.db`/`s.Get`/`s.ListInstancesByApp`/`Log`。
- Produces: `AppFullView`（替换 `AppDetail`）、`AppSession/AppTask/AppRoute`、`ListSessionsByApp/ListRoutesByApp/ListTasksByApp`。前端 T2 消费 `AppFullView` JSON。

- [ ] **Step 1: 写失败测试（DB 集成）**

新建 `detail_test.go`：

```go
package appdeploy

import (
	"context"
	"testing"
	"time"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// seedDetailStore 连 anp_test PG + 清 7 表隔离（appdeploy 三表 + Detail 聚合的 4 源表）。
func seedDetailStore(t *testing.T) *Store {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db,
		"appdeploy_env", "appdeploy_instance", "appdeploy_application",
		"codews_session", "appdeploy_route", "code_task",
		"change_request", "release_record", "requirement")
	return NewStore(db)
}

// TestDetail_AggregatesAllDimensions seed app + 编码会话 + 路由 + 变更+异步任务，
// 断言 Detail() 把四源聚合进 AppFullView，且 deploy needs（无 manifest 时）为 nil 不崩。
func TestDetail_AggregatesAllDimensions(t *testing.T) {
	s := seedDetailStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "panorama")
	if err := s.Create(ctx, a); err != nil {
		t.Fatalf("create app: %v", err)
	}
	// 编码会话（codews_session.app_id 直关联）
	mustExec(t, ctx, s,
		`INSERT INTO codews_session (id, project_space_id, app_id, tool, repo_dir, prompt_count)
		 VALUES ($1,'ps_1',$2,'opencode','/data/repos/panorama',3)`, "cws_1", a.ID)
	// 路由（appdeploy_route.app_id FK → app）
	mustExec(t, ctx, s,
		`INSERT INTO appdeploy_route (id, app_id, project_space_id, app_code, env, upstream_host, upstream_port, status, auth_required)
		 VALUES ($1,$2,'ps_1','panorama','test','10.10.0.28',9100,'active',false)`, "rt_1", a.ID)
	// 变更（change_request.source_id=appID 直登）+ 异步任务（code_task.change_id）
	mustExec(t, ctx, s,
		`INSERT INTO change_request (id, source_id, status, kind, created_at)
		 VALUES ($1,$2,'pending','code',$3)`, "ch_1", a.ID, time.Now())
	mustExec(t, ctx, s,
		`INSERT INTO code_task (id, project_space_id, kind, source_id, repo_dir, prompt, model, status, change_id)
		 VALUES ($1,'ps_1','dispatch',$2,'/data/repos/panorama','do','m','completed',$3)`,
		"ct_1", a.ID, "ch_1")

	d, err := s.Detail(ctx, "ps_1", a.ID)
	if err != nil || d == nil {
		t.Fatalf("Detail err=%v d=%v", err, d)
	}
	if len(d.Sessions) != 1 || d.Sessions[0].ID != "cws_1" {
		t.Fatalf("Sessions 聚合错 got=%+v", d.Sessions)
	}
	if len(d.Routes) != 1 || d.Routes[0].Env != "test" {
		t.Fatalf("Routes 聚合错 got=%+v", d.Routes)
	}
	if len(d.Tasks) != 1 || d.Tasks[0].ID != "ct_1" {
		t.Fatalf("Tasks 聚合错 got=%+v", d.Tasks)
	}
	// Deps 在 Store.Detail 不填（handler 层经 mwReconciler 填）；此处应为空切片（nil 归一化）
	if d.Deps == nil {
		t.Fatal("Deps 应 nil 归一化为空切片非 nil")
	}
	// 无 .anp/deploy.yaml（RepoDir=/data/repos/panorama 不存在）→ DeployNeeds nil 不崩
	if d.DeployNeeds != nil {
		t.Fatalf("无 manifest 时 DeployNeeds 应 nil got=%+v", d.DeployNeeds)
	}
}

// mustExec 测试辅助：ExecContext 失败即 fatal。
func mustExec(t *testing.T, ctx context.Context, s *Store, query string, args ...any) {
	t.Helper()
	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd platform/backend && go test -run TestDetail_AggregatesAllDimensions ./internal/appdeploy/`
Expected: 编译失败 `undefined: AppFullView 字段 / ListSessionsByApp 等`（Detail 仍返旧 AppDetail）。

- [ ] **Step 3: 实现 detail.go（改名 + 扩字段 + 类型 + 3 方法 + 聚合）**

把 `detail.go` 顶部 `AppDetail` 结构体（:10-17）替换为：

```go
// AppFullView 应用全景聚合：应用本体 + 需求/变更/发布/git历史/实例（原 AppDetail）
// + 编码会话/异步任务/路由/依赖/部署需求 needs（P1-c 全景扩展）。单一信息源，前端详情看板据此渲染。
type AppFullView struct {
	Application  Application     `json:"application"`
	Requirements []AppReqItem    `json:"requirements"`
	Changes      []AppChangeItem `json:"changes"`
	Releases     []AppRelItem    `json:"releases"`
	Commits      []CommitInfo    `json:"commits"`
	Instances    []AppInstance   `json:"instances"`
	// P1-c 全景维度
	Sessions    []AppSession     `json:"sessions"`     // 编码会话（codews_session by app_id）
	Tasks       []AppTask        `json:"tasks"`        // 异步编码任务（code_task 经 change→app 派生）
	Routes      []AppRoute       `json:"routes"`       // 路由（appdeploy_route by app_id）
	Deps        []DepDeclaration `json:"deps"`         // 中间件依赖（handler 经 mwReconciler 填）
	DeployNeeds *NeedsSpec       `json:"deploy_needs"` // .anp/deploy.yaml needs（权威输入，只读展示；无 manifest=nil）
}

// AppSession 编码会话摘要（codews_session 子集，前端研发列用）。
type AppSession struct {
	ID          string     `json:"id" db:"id"`
	Tool        string     `json:"tool" db:"tool"`
	StartedAt   time.Time  `json:"started_at" db:"started_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty" db:"ended_at"`
	PromptCount int        `json:"prompt_count" db:"prompt_count"`
}

// AppTask 异步编码任务摘要（code_task 子集 + 派生 req_title，前端研发列用）。
type AppTask struct {
	ID        string    `json:"id" db:"id"`
	Kind      string    `json:"kind" db:"kind"`
	Status    string    `json:"status" db:"status"`
	ReqTitle  string    `json:"req_title,omitempty" db:"req_title"`
	ChangeID  string    `json:"change_id,omitempty" db:"change_id"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// AppRoute 路由摘要（appdeploy_route 子集，前端部署列用）。
type AppRoute struct {
	Env          string `json:"env" db:"env"`
	AppCode      string `json:"app_code" db:"app_code"`
	UpstreamHost string `json:"upstream_host" db:"upstream_host"`
	UpstreamPort int    `json:"upstream_port" db:"upstream_port"`
	Status       string `json:"status" db:"status"`
	ExternalURL  string `json:"external_url,omitempty" db:"external_url"`
}
```

把 `Detail` 方法签名 + 聚合段（:53-107）替换为：

```go
// Detail 聚合某应用的全景视图（需求→变更→发布 + 实例 + 编码会话/异步任务/路由/部署需求）。
// Deps 留空（handler 经 mwReconciler.ListDeps 填，避 appdeploy→mwsupply 依赖）。
func (s *Store) Detail(ctx context.Context, psID, appID string) (*AppFullView, error) {
	a, err := s.Get(ctx, psID, appID)
	if err != nil || a == nil || a.ID == "" {
		return nil, err
	}
	d := &AppFullView{Application: *a}

	// 需求（直接归属，含详情字段供前端展开；按等级 P0→P1→P2 排序）
	if err := s.db.SelectContext(ctx, &d.Requirements,
		`SELECT id, COALESCE(title,'') AS title, status,
		        COALESCE(priority,'') AS priority, COALESCE(fixed_version,'') AS fixed_version, COALESCE(tasks,'') AS tasks, COALESCE(assignee,'') AS assignee,
		        COALESCE(description,'') AS description, COALESCE(user_story,'') AS user_story,
		        COALESCE(acceptance_criteria,'') AS acceptance_criteria
		 FROM requirement WHERE application_id=$1 ORDER BY COALESCE(NULLIF(priority,''),'P1'), created_at DESC`, appID); err != nil {
		return nil, err
	}
	// 变更：source_id=应用ID（交互编码登记，期2）OR source_id=需求ID（AI 编码派生）
	if err := s.db.SelectContext(ctx, &d.Changes,
		`SELECT id, status, COALESCE(source_id,'') AS source_id, COALESCE(kind,'') AS kind, COALESCE(output,'') AS output, created_at
		 FROM change_request
		 WHERE source_id = $1 OR source_id IN (SELECT id FROM requirement WHERE application_id=$2)
		 ORDER BY created_at DESC`, appID, appID); err != nil {
		return nil, err
	}
	// 发布（经 change_id→change→source_id→requirement→app 派生）
	if err := s.db.SelectContext(ctx, &d.Releases,
		`SELECT id, version, status, COALESCE(change_id,'') AS change_id, created_at
		 FROM release_record
		 WHERE change_id IN (SELECT id FROM change_request WHERE source_id IN (SELECT id FROM requirement WHERE application_id=$1))
		 ORDER BY created_at DESC`, appID); err != nil {
		return nil, err
	}
	// 托管仓库版本历史（git log = 应用代码版本）
	d.Commits, _ = Log(ctx, a.RepoDir, 10)
	// 各环境部署实例（test/prod）
	d.Instances, _ = s.ListInstancesByApp(ctx, appID)
	// P1-c：编码会话（codews_session.app_id 直关联）
	d.Sessions, _ = s.ListSessionsByApp(ctx, appID)
	// P1-c：路由（appdeploy_route.app_id）
	d.Routes, _ = s.ListRoutesByApp(ctx, appID)
	// P1-c：异步编码任务（code_task 经 change_request→app 派生）
	d.Tasks, _ = s.ListTasksByApp(ctx, appID)
	// P1-c：部署需求 needs（.anp/deploy.yaml 权威输入，只读；best-effort，无 manifest=nil）
	if mf, _ := LoadDeployManifest(a.RepoDir); mf != nil {
		d.DeployNeeds = &mf.Needs
	}
	// nil→空切片归一化（Go nil 切片序列化 JSON null，前端 .length 崩；统一空数组）
	d.Requirements = norm(d.Requirements)
	d.Changes = norm(d.Changes)
	d.Releases = norm(d.Releases)
	d.Commits = norm(d.Commits)
	d.Instances = norm(d.Instances)
	d.Sessions = norm(d.Sessions)
	d.Routes = norm(d.Routes)
	d.Tasks = norm(d.Tasks)
	d.Deps = norm(d.Deps)
	return d, nil
}

// norm nil 切片归一化为空切片（泛型，复用给 Detail 各维度）。
func norm[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// ListSessionsByApp 列某应用的编码会话（codews_session by app_id，最近 20 条）。
func (s *Store) ListSessionsByApp(ctx context.Context, appID string) ([]AppSession, error) {
	var list []AppSession
	err := s.db.SelectContext(ctx, &list,
		`SELECT id, COALESCE(tool,'') AS tool, started_at, ended_at, prompt_count
		 FROM codews_session WHERE app_id=$1 ORDER BY started_at DESC LIMIT 20`, appID)
	return list, err
}

// ListRoutesByApp 列某应用的路由（appdeploy_route by app_id，按 env 排）。
func (s *Store) ListRoutesByApp(ctx context.Context, appID string) ([]AppRoute, error) {
	var list []AppRoute
	err := s.db.SelectContext(ctx, &list,
		`SELECT env, COALESCE(app_code,'') AS app_code, COALESCE(upstream_host,'') AS upstream_host,
		        upstream_port, COALESCE(status,'') AS status, COALESCE(external_url,'') AS external_url
		 FROM appdeploy_route WHERE app_id=$1 ORDER BY env`, appID)
	return list, err
}

// ListTasksByApp 列某应用的异步编码任务（code_task 经 change_request 派生回 app，最近 20 条）。
// 派生路径镜像 codetask.ListByProjectSpace：task.change_id→change.id，change.source_id=appID（直登）
// 或 change.source_id∈requirement(appID)（AI 派发）。
func (s *Store) ListTasksByApp(ctx context.Context, appID string) ([]AppTask, error) {
	var list []AppTask
	err := s.db.SelectContext(ctx, &list,
		`SELECT t.id, COALESCE(t.kind,'') AS kind, t.status,
		        COALESCE((SELECT r.title FROM requirement r WHERE r.id = t.source_id),'') AS req_title,
		        COALESCE(t.change_id,'') AS change_id, t.created_at
		 FROM code_task t
		 JOIN change_request ch ON ch.id = t.change_id
		 WHERE ch.source_id = $1 OR ch.source_id IN (SELECT id FROM requirement WHERE application_id=$1)
		 ORDER BY t.created_at DESC LIMIT 20`, appID, appID)
	return list, err
}
```

- [ ] **Step 4: handler.go Detail 填 Deps**

`handler.go:227-234` 的 `Detail` handler 替换为：

```go
func (h *Handler) Detail(c *gin.Context) {
	ctx := c.Request.Context()
	d, err := h.store.Detail(ctx, c.Param("id"), c.Param("aid"))
	if err != nil || d == nil {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	// P1-c：Deps 经 MWReconciler 接口填（appdeploy 不直依 mwsupply）。best-effort，失败仅留空。
	if h.mwReconciler != nil {
		if deps, derr := h.mwReconciler.ListDeps(ctx, c.Param("aid")); derr == nil {
			d.Deps = deps
			if d.Deps == nil {
				d.Deps = []DepDeclaration{}
			}
		}
	}
	httpx.OK(c, d)
}
```

- [ ] **Step 5: 运行测试通过**

Run: `cd platform/backend && go test -run TestDetail_AggregatesAllDimensions ./internal/appdeploy/ -v`
Expected: PASS。若 seed INSERT 因某表 NOT NULL 列报错，按报错补列（change_request/code_task 的列见事实基线 6）。

- [ ] **Step 6: appdeploy 全包测试 + vet**

Run: `cd platform/backend && gofmt -w internal/appdeploy/detail.go internal/appdeploy/detail_test.go internal/appdeploy/handler.go && go vet ./internal/appdeploy/ && go test -count=1 ./internal/appdeploy/`
Expected: PASS（含既有 deploy_manifest/deployer 测试 + 新 Detail 测试）。

- [ ] **Step 7: 提交**

```bash
git add platform/backend/internal/appdeploy/detail.go platform/backend/internal/appdeploy/detail_test.go platform/backend/internal/appdeploy/handler.go
git commit -m "feat(appdeploy): Detail 扩 AppFullView 聚合五维度(P1-c)

AppDetail→AppFullView 加 Sessions/Tasks/Routes/Deps/DeployNeeds；
ListSessionsByApp/ListRoutesByApp/ListTasksByApp(SQL 直查共享表，
避跨包依赖)；Detail 读 deploy.yaml needs；handler 经 mwReconciler
填 Deps。nil 归一化泛型化。DB 集成测试覆盖四源聚合。

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: 前端五列全景看板（applications/page.tsx）

**Files:**

- Modify: `platform/frontend/app/applications/page.tsx`（Detail 类型 :78-97 扩字段；三列 grid :1885-1978 重排五列）

**Interfaces:**

- Consumes: 后端 `AppFullView` JSON（T1 产出，新字段 sessions/tasks/routes/deps/deploy_needs；instances 已在 application 上）。
- Produces: 五列全景看板（需求/研发/部署/运行/依赖）。

- [ ] **Step 1: 扩 Detail 类型**

`page.tsx:78-97` 的 `Detail` 类型，在 `commits` 后追加字段：

```ts
type Detail = {
  application: App;
  requirements: Req[];
  changes: {
    id: string;
    status: string;
    kind: string;
    source_id: string;
    created_at: string;
    output?: string;
  }[];
  releases: {
    id: string;
    version: string;
    status: string;
    change_id: string;
    created_at: string;
  }[];
  commits: { sha: string; message: string; date: string }[];
  // P1-c 全景维度
  instances: Instance[];
  sessions: {
    id: string;
    tool: string;
    started_at: string;
    ended_at?: string;
    prompt_count: number;
  }[];
  tasks: {
    id: string;
    kind: string;
    status: string;
    req_title?: string;
    change_id?: string;
    created_at: string;
  }[];
  routes: {
    env: string;
    app_code: string;
    upstream_host: string;
    upstream_port: number;
    status: string;
    external_url?: string;
  }[];
  deps: Dep[];
  deploy_needs?: {
    mounts: { src: string; dst: string; readonly?: boolean }[];
    env_keys: string[];
    ports: number[];
    command: string;
  };
};
```

- [ ] **Step 2: 三列 grid 重排为五列全景**

把 `page.tsx:1885-1978` 整个 `<div className="mt-2 grid ... md:grid-cols-3">…</div>`（三列：需求/变更/发布）替换为五列全景（需求/研发/部署/运行/依赖）。完整替换块：

```tsx
<div className="mt-2 grid grid-cols-1 gap-2 rounded bg-bg p-2 text-xs md:grid-cols-5">
  {/* 需求 */}
  <div>
    <div className="mb-1 font-medium text-text-muted">需求（{detail.requirements.length}）</div>
    {detail.requirements.map((q) => (
      <div key={q.id} className="truncate">
        <span className={q.status === "delivered" ? "text-success" : "text-text-muted"}>●</span>{" "}
        {q.title}
      </div>
    ))}
    {detail.requirements.length === 0 && <div className="text-text-muted">无</div>}
  </div>

  {/* 研发：编码会话 + 异步任务 + 变更（含闭环按钮）+ git */}
  <div>
    <div className="mb-1 font-medium text-text-muted">研发</div>
    <div className="mb-1 text-text-muted">编码会话（{detail.sessions.length}）</div>
    {detail.sessions.map((s) => (
      <div key={s.id} className="truncate">
        <span className="text-accent">●</span> {s.tool} · {s.prompt_count}轮
      </div>
    ))}
    <div className="mt-1 text-text-muted">异步任务（{detail.tasks.length}）</div>
    {detail.tasks.map((tk) => (
      <div key={tk.id} className="truncate">
        <span className={tk.status === "completed" ? "text-success" : "text-warn"}>●</span>{" "}
        {tk.kind} · {tk.status}
      </div>
    ))}
    <div className="mt-1 text-text-muted">变更（{detail.changes.length}）</div>
    {detail.changes.map((c) => (
      <div key={c.id} className="mb-1.5 rounded border border-border p-1.5">
        <div>
          <span
            className={
              c.status === "approved"
                ? "text-success"
                : c.status === "released"
                  ? "text-accent"
                  : "text-warn"
            }
          >
            ●
          </span>{" "}
          {c.kind} · {c.status}
        </div>
        {c.output && <ChangeOutput output={c.output} />}
        <div className="mt-1 flex flex-wrap gap-1">
          {c.status === "pending" && (
            <>
              <button
                onClick={() => approveChange(a.id, c.id)}
                className="rounded bg-success/10 px-1.5 py-0.5 text-success hover:bg-success/20"
              >
                审批通过
              </button>
              <button
                onClick={() => rejectChange(a.id, c.id)}
                className="rounded bg-warn/10 px-1.5 py-0.5 text-warn hover:bg-warn/20"
              >
                拒绝
              </button>
            </>
          )}
          {c.status === "approved" && (
            <>
              <button
                onClick={() => releaseChange(a.id, c.id)}
                className="rounded bg-accent/10 px-1.5 py-0.5 text-accent hover:bg-accent/20"
                title="建发布版本 + 标 delivered + 触发部署"
              >
                发布上线
              </button>
              <button
                onClick={() => mergeChange(a.id, c.id, c.source_id)}
                className="rounded bg-success/10 px-1.5 py-0.5 text-success hover:bg-success/20"
                title="合并 dev→main + 标 delivered + 释放认领"
              >
                合并main
              </button>
            </>
          )}
        </div>
      </div>
    ))}
    <div className="mt-1 text-text-muted">git（{detail.commits.length}）</div>
    {detail.commits.slice(0, 3).map((c) => (
      <div key={c.sha} className="truncate">
        <span className="text-text-muted">●</span> {c.message}
      </div>
    ))}
  </div>

  {/* 部署：实例 test/prod + URL + 发布版本 + 部署需求 needs★ */}
  <div>
    <div className="mb-1 font-medium text-text-muted">部署</div>
    {(detail.instances ?? []).map((ins) => (
      <div key={ins.env} className="truncate">
        <span className={ins.status === "running" ? "text-success" : "text-text-muted"}>●</span>{" "}
        {ins.env} · v{ins.version}
        {ins.url && (
          <a href={ins.url} target="_blank" rel="noreferrer" className="ml-1 text-accent">
            ↗
          </a>
        )}
      </div>
    ))}
    {(detail.instances ?? []).length === 0 && <div className="text-text-muted">无实例</div>}
    <div className="mt-1 text-text-muted">发布（{detail.releases.length}）</div>
    {detail.releases.slice(0, 3).map((r) => (
      <div key={r.id}>
        <span className="text-accent">●</span> {r.version} · {r.status}
      </div>
    ))}
    {detail.deploy_needs && (
      <div className="mt-1 rounded border border-warn/30 bg-warn/5 p-1">
        <div className="font-medium text-warn">★ 部署需求 needs</div>
        {detail.deploy_needs.ports.length > 0 && (
          <div>ports: {detail.deploy_needs.ports.join(",")}</div>
        )}
        {detail.deploy_needs.command && (
          <div className="truncate">cmd: {detail.deploy_needs.command}</div>
        )}
        {detail.deploy_needs.mounts.length > 0 && (
          <div>mounts: {detail.deploy_needs.mounts.length}条</div>
        )}
        {detail.deploy_needs.env_keys.length > 0 && (
          <div>env_keys: {detail.deploy_needs.env_keys.join(",")}</div>
        )}
      </div>
    )}
  </div>

  {/* 运行：资源当前 + 健康徽标（复用已轮询的 appStats） */}
  <div>
    <div className="mb-1 font-medium text-text-muted">运行</div>
    {(() => {
      const st = appStats[a.id];
      if (!st) return <div className="text-text-muted">无数据</div>;
      const hb = healthBadge(st.health || "");
      return (
        <>
          <div>
            <span className={hb.cls}>●</span> {hb.text}
          </div>
          {st.cpu && <div className="text-text-muted">CPU {st.cpu}</div>}
          {st.mem && <div className="text-text-muted">内存 {st.mem}</div>}
          {st.url && (
            <a href={st.url} target="_blank" rel="noreferrer" className="text-accent">
              访问 ↗
            </a>
          )}
        </>
      );
    })()}
  </div>

  {/* 依赖：中间件绑定 + deps 声明 */}
  <div>
    <div className="mb-1 font-medium text-text-muted">依赖（{detail.deps.length}）</div>
    {detail.deps.map((d, i) => (
      <div key={i} className="truncate">
        <span
          className={
            d.status === "bound"
              ? "text-success"
              : d.status === "failed"
                ? "text-danger"
                : "text-warn"
          }
        >
          ●
        </span>{" "}
        {d.kind} · {d.strategy}
      </div>
    ))}
    {detail.deps.length === 0 && <div className="text-text-muted">无</div>}
  </div>
</div>
```

- [ ] **Step 3: tsc 验证**

Run: `cd platform/frontend && pnpm exec tsc --noEmit`
Expected: 0 error。（注意 `detail.instances` 后端现返顶层字段；`appStats[a.id]`/`healthBadge` 已存在。）

- [ ] **Step 4: 提交**

```bash
git add platform/frontend/app/applications/page.tsx
git commit -m "feat(ui): 应用详情三列升级为五列全景看板(P1-c)

需求/研发(会话+任务+变更+git)/部署(实例+URL+发布+needs★)/
运行(资源+健康)/依赖 五列。Detail 类型扩 sessions/tasks/routes/
deps/deploy_needs/instances。复用 appStats 轮询与 healthBadge。

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: 全量验证 + 合 main + 部署 .28

- [ ] **Step 1: 全后端测试**

Run: `cd platform/backend && go test -p 1 -count=1 ./...`
Expected: 全 PASS（mwsupply 若因 DB 串行污染报错，单独重跑该包确认 ok——见 [[anp-ci-pipeline]] 已知隔离问题，非本改动）。

- [ ] **Step 2: 前端 tsc + build**

Run: `cd platform/frontend && pnpm exec tsc --noEmit`
Expected: 0 error。

- [ ] **Step 3: ff 合 main**

```bash
cd <仓库根>
git checkout main
git merge --ff-only feat/app-panorama-p1c
```

（origin 推送待用户确认。）

- [ ] **Step 4: 部署 backend + frontend 到 .28**

后端改 detail.go/handler.go/detail_test.go（test 不影响运行），前端改 page.tsx。

```bash
SSH="ssh -o PubkeyAcceptedAlgorithms=+ssh-rsa -o StrictHostKeyChecking=no -i ~/.ssh/miscode root@10.10.0.28"
SCP_OPTS="-o PubkeyAcceptedAlgorithms=+ssh-rsa -o StrictHostKeyChecking=no -i ~/.ssh/miscode"
# 后端源（test 不传）
scp $SCP_OPTS platform/backend/internal/appdeploy/detail.go platform/backend/internal/appdeploy/handler.go root@10.10.0.28:/opt/anp/platform/backend/internal/appdeploy/
# 前端
scp $SCP_OPTS platform/frontend/app/applications/page.tsx root@10.10.0.28:/opt/anp/platform/frontend/app/applications/page.tsx
# 重建 backend（docker-compose v1）+ frontend（next build）
$SSH "cd /opt/anp && docker-compose -f deploy/docker-compose.prod.yml up --build -d backend frontend"
```

验证 `deploy_backend_1`/`deploy_frontend_1` Up、backend 日志无 panic、PID 1 server LISTEN 8080。

- [ ] **Step 5: e2e 抽查（待用户）**

打开某应用详情 → 看到五列全景（需求/研发含编码会话/部署含实例+needs★/运行健康/依赖）。标记**待用户浏览器 e2e**。

---

## Self-Review

**1. Spec 覆盖**（spec §4.1 P1-c）：

- 扩 Store.Detail→AppFullView ✅（T1 Step3 改名+扩字段）
- ListSessionsByApp（复用 performance SQL 换 app_id）✅（T1 Step3，SQL 镜像 performance/store.go:96-99）
- 派生 code_task（经 source_id/change_id）✅（T1 Step3 ListTasksByApp JOIN change_request）
- ListRoutesByApp ✅（T1 Step3）
- 并入 ListDeps ✅（T1 Step4 handler 经 mwReconciler 接口）
- 前端五列全景布局（spec ASCII）✅（T2 Step2 五列 grid）
- 部署维度内嵌 needs★（权威输入）✅（T2 Step2 部署列 DeployNeeds 框，只读）
- Detail 实时聚合 + 延续 30s 轮询 ✅（后端实时；前端 appStats 30s 轮询既存 :613）
- 包括 ANP 自身登记为 application ✅（无代码——ANP 若登记为 app 记录则自动复用同视图，spec 表述为数据登记非代码）

**2. 占位符扫描**：无 TBD；每步完整代码；seed SQL 列明示；commit/测试完整。✅

**3. 类型/命名一致性**：

- `AppFullView`：T1 定义 → T1 Detail 返回 → T2 前端 Detail 类型镜像（字段名 application/requirements/changes/releases/commits/instances/sessions/tasks/routes/deps/deploy_needs 与 json tag 一致）。✅
- `AppSession/AppTask/AppRoute`：T1 定义 → T1 List* 返回 → 前端 sessions/tasks/routes 子类型字段一致（id/tool/started_at/...）。✅
- `ListSessionsByApp/ListRoutesByApp/ListTasksByApp`：T1 定义 + Detail 调用一致。✅
- `norm[T]` 泛型归一化：覆盖全部 10 个切片字段（含新 4 + Deps）。✅
- Deps 在 Store.Detail 留 nil→norm 成空切片；handler 填时再 norm 一次（防 mwReconciler 返 nil）。✅

**4. 依赖方向**：appdeploy 不 import performance/codetask/mwsupply（SQL 直查共享表 / mwReconciler 接口）。appgw 不新增 import（route 走 SQL）。✅

无遗留问题。

---

## Execution

inline 执行（用户已选模式 1）。逐 task：T1 TDD(后端) → T2(前端) → T3 验证+合 main+部署 .28。

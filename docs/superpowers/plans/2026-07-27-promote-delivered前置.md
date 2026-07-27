# Promote delivered 前置（AC7）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 `Promote` 上线 prod 前加「approved 变更关联的需求须已 delivered」前置,堵「跳过 release 直接 promote」的绕过(AC7),并对全路径补单测。

**Architecture:** 复用 `release.Create` delivered 回写的对偶反查链 `change_request.source_id → requirement.status`。在 `requirement.Repository` 加 `HasUnDeliveredApprovedByApp(appID)` 方法(JOIN change_request+requirement,双路径 appSourceCond),`Promote` handler 在变更闸门通过后、`MarkReleased` 前调用,命中则 `409/40921`。grandfather 由 `JOIN r ON r.id=c.source_id` 自然实现(source_id 解析不到需求 → 不计数 → 放行)。

**Tech Stack:** Go + gin + sqlx + PostgreSQL(anp_test);测试经 `testutil.TestDB` 连 `.28` anp_test 跑迁移建表。

## Global Constraints

- 禁 SQLite:所有测试连 `anp_test` PG(`testutil.TestDB`,默认 `postgres://anp:anp_dev_pwd@10.10.0.28:5432/anp_test`),可用 `ANP_TEST_DATABASE_URL` 覆盖。
- AC7 错误码固定 `409 / 40921`,文案「来源需求未交付,请先在发布中心发布上线后再 promote」(区别变更闸门 `40920`)。
- 不动 schema、不动前端、不动 release/merge 写入路径。
- HTTP 层只测拒绝路径(403/409),不测 200 通过路径(promote 成功会触发 `go buildAndDeploy` 异步 docker,回避);通过语义由 repository 数据层覆盖。200 端到端留 .28 dogfood(Task 3)。
- 不改共享 fixture `newHTTPHandler`(它的 `changes/reqRepo=nil` 被 `TestHandler_RegisterChange_changesNil` 的 nil-gate 测试依赖);promote 闸门测试用**新 fixture** `newHTTPHandlerWithGates`(细化 spec §6.3)。

---

## Task 1: `requirement.Repository.HasUnDeliveredApprovedByApp` + 数据层测试

**Files:**

- Modify: `platform/backend/internal/requirement/repository.go`(在 `UpdateStatus` 后新增方法)
- Test: `platform/backend/internal/requirement/repository_test.go`(新增一个测试函数 + `sqlx` import)

**Interfaces:**

- Produces: `func (r *Repository) HasUnDeliveredApprovedByApp(ctx context.Context, appID string) (bool, error)` —— Task 2 的 Promote handler 依赖此签名。

- [ ] **Step 1: 给 `repository_test.go` 加 `sqlx` import**

`platform/backend/internal/requirement/repository_test.go` 现有 import 块(line 3-10):

```go
import (
	"context"
	"strings"
	"testing"
	"time"

	"zhiyuan-anp/platform/backend/internal/testutil"
)
```

改为(加 `github.com/jmoiron/sqlx`,用于跨表 seed change_request):

```go
import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"zhiyuan-anp/platform/backend/internal/testutil"
)
```

- [ ] **Step 2: 写失败测试 `TestRepository_HasUnDeliveredApprovedByApp`**

在 `repository_test.go` 末尾(`mustAssign` helper 前,line 339 之前)追加:

```go
// TestRepository_HasUnDeliveredApprovedByApp promote 闸门(AC7):app 有 approved 变更且其来源需求
// 未 delivered → true(堵跳过 release 直接上线)。grandfather(source_id 解析不到需求)/跨 app 隔离覆盖。
// 反查链 change_request.source_id→requirement.status(双路径 appSourceCond,对称 release 回写)。
func TestRepository_HasUnDeliveredApprovedByApp(t *testing.T) {
	ctx := context.Background()

	// seedChg 直接插 change_request(本包无 change.Store,跨表 seed 走原始 SQL)。
	seedChg := func(db *sqlx.DB, id, psID, sourceID, status string) {
		t.Helper()
		if _, err := db.ExecContext(ctx,
			`INSERT INTO change_request (id, project_space_id, source_id, kind, output, status)
			 VALUES ($1, $2, $3, 'code', 'diff', $4)`,
			id, psID, sourceID, status); err != nil {
			t.Fatalf("seed change %s: %v", id, err)
		}
	}
	// seedReq 直接插 requirement(精确控制 application_id/status)。
	seedReq := func(db *sqlx.DB, id, psID, appID, status string) {
		t.Helper()
		if _, err := db.ExecContext(ctx,
			`INSERT INTO requirement (id, project_space_id, application_id, title, status)
			 VALUES ($1, $2, $3, 't', $4)`,
			id, psID, appID, status); err != nil {
			t.Fatalf("seed req %s: %v", id, err)
		}
	}

	cases := []struct {
		name  string
		setup func(db *sqlx.DB)
		appID string
		want  bool
	}{
		{
			name: "approved+未delivered→true(双路径经application_id)",
			setup: func(db *sqlx.DB) {
				seedReq(db, "req_1", "ps_1", "app_1", "developing")
				seedChg(db, "chg_1", "ps_1", "req_1", "approved")
			},
			appID: "app_1",
			want:  true,
		},
		{
			name: "approved+delivered→false",
			setup: func(db *sqlx.DB) {
				seedReq(db, "req_1", "ps_1", "app_1", "delivered")
				seedChg(db, "chg_1", "ps_1", "req_1", "approved")
			},
			appID: "app_1",
			want:  false,
		},
		{
			name: "无approved仅pending→false",
			setup: func(db *sqlx.DB) {
				seedReq(db, "req_1", "ps_1", "app_1", "developing")
				seedChg(db, "chg_1", "ps_1", "req_1", "pending")
			},
			appID: "app_1",
			want:  false,
		},
		{
			name: "source_id=旧appID解析不到需求→false(grandfather对称)",
			setup: func(db *sqlx.DB) {
				seedChg(db, "chg_1", "ps_1", "app_1", "approved") // source_id 直接是 appID,无对应 requirement
			},
			appID: "app_1",
			want:  false,
		},
		{
			name: "跨app隔离→false",
			setup: func(db *sqlx.DB) {
				seedReq(db, "req_1", "ps_1", "app_b", "developing")
				seedChg(db, "chg_1", "ps_1", "req_1", "approved")
			},
			appID: "app_a",
			want:  false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := testutil.TestDB(t)
			testutil.Truncate(t, db, "requirement", "change_request")
			c.setup(db)
			r := NewRepository(db)
			got, err := r.HasUnDeliveredApprovedByApp(ctx, c.appID)
			if err != nil {
				t.Fatalf("HasUnDeliveredApprovedByApp: %v", err)
			}
			if got != c.want {
				t.Fatalf("app=%s 应 %v,得到 %v", c.appID, c.want, got)
			}
		})
	}
}
```

- [ ] **Step 3: 跑测试验证它失败**

Run:

```bash
cd platform/backend && go test ./internal/requirement/ -run TestRepository_HasUnDeliveredApprovedByApp -v
```

Expected: 编译失败 `undefined: (*Repository).HasUnDeliveredApprovedByApp`(方法还没实现)。

- [ ] **Step 4: 实现 `HasUnDeliveredApprovedByApp`**

在 `platform/backend/internal/requirement/repository.go` 的 `UpdateStatus`(line 89)之后、`SetApplication`(line 91)之前追加:

```go
// HasUnDeliveredApprovedByApp 该 app 是否存在「approved 变更但来源需求未 delivered」的情形。
// promote 闸门(AC7)据此拒绝「跳过 release 直接上线」。
//
// SQL 要点:
//   - JOIN requirement r ON r.id=c.source_id:source_id 为旧 appID 路径时 JOIN 不到(r=NULL),
//     r.status<>delivered 不命中 → 天然 grandfather(对称 release 回写)。
//   - appSourceCond 双路径:c.source_id=appID 或 c.source_id 是该 app 的需求(requirement.application_id=appID)。
//   - 多个 approved 变更取并集,任一来源需求未 delivered 即 true(EXISTS 天然语义)。
func (r *Repository) HasUnDeliveredApprovedByApp(ctx context.Context, appID string) (bool, error) {
	var exists bool
	const q = `SELECT EXISTS (
		SELECT 1 FROM change_request c
		JOIN requirement r ON r.id = c.source_id
		WHERE (c.source_id = $1 OR c.source_id IN (SELECT id FROM requirement WHERE application_id = $1))
		  AND c.status = 'approved'
		  AND r.status <> 'delivered')`
	err := r.db.GetContext(ctx, &exists, q, appID)
	return exists, err
}
```

- [ ] **Step 5: 跑测试验证通过**

Run:

```bash
cd platform/backend && go test ./internal/requirement/ -run TestRepository_HasUnDeliveredApprovedByApp -v
```

Expected: PASS(5 个子测试全过)。

- [ ] **Step 6: 跑整个 requirement 包确认无回归**

Run:

```bash
cd platform/backend && go test ./internal/requirement/ -v
```

Expected: PASS(现有 TestRepository_* 全过)。

- [ ] **Step 7: Commit**

```bash
git add platform/backend/internal/requirement/repository.go platform/backend/internal/requirement/repository_test.go
git commit -F - <<'EOF'
feat(requirement): 加 HasUnDeliveredApprovedByApp(promote AC7 闸门数据层)

JOIN change_request+requirement(appSourceCond 双路径),
有 approved 变更且来源需求未 delivered→true。grandfather 由
JOIN r.id=c.source_id 自然实现(对称 release 回写)。
含 5 场景数据层单测。

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
```

---

## Task 2: `Promote` handler AC7 前置 + fixture + HTTP 拒绝测试

**Files:**

- Modify: `platform/backend/internal/appdeploy/handler.go`(`Promote`,line 1005-1014 块内插入)
- Test: `platform/backend/internal/appdeploy/handler_http_test.go`(加 `change`/`requirement` import + `newHTTPHandlerWithGates` fixture + 3 个测试)

**Interfaces:**

- Consumes: `requirement.Repository.HasUnDeliveredApprovedByApp(ctx, appID) (bool, error)`(Task 1 产出)、`change.Store.HasAny/HasApproved/MarkReleased`(已存在)。
- Produces: `Promote` 新增 `409/40921` 分支;`newHTTPHandlerWithGates(t) (*Handler, *sqlx.DB)` fixture。

- [ ] **Step 1: 给 `handler_http_test.go` 加 `change`/`requirement` import**

现有 import 块(line 3-15):

```go
import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"

	"zhiyuan-anp/platform/backend/internal/testutil"
)
```

改为(加 `change`、`requirement`):

```go
import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"

	"zhiyuan-anp/platform/backend/internal/change"
	"zhiyuan-anp/platform/backend/internal/requirement"
	"zhiyuan-anp/platform/backend/internal/testutil"
)
```

- [ ] **Step 2: 加 `newHTTPHandlerWithGates` fixture**

在 `handler_http_test.go` 的 `newHTTPHandlerWithTables`(line 431-433)之后追加。**不改** `newHTTPHandler`(保留其 `changes/reqRepo=nil` 给 `TestHandler_RegisterChange_changesNil` 等 nil-gate 测试用):

```go
// newHTTPHandlerWithGates 同 newHTTPHandler,但注入 changes+reqRepo,
// 供 Promote 变更闸门(40920)/AC7 delivered 前置(40921)测试——这两个依赖真实 Store/Repository。
// 不复用 newHTTPHandler:后者 changes=nil 被 TestHandler_RegisterChange_changesNil 的 nil-gate 测试依赖。
func newHTTPHandlerWithGates(t *testing.T) (*Handler, *sqlx.DB) {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db,
		"release_record", "change_request", "requirement",
		"appdeploy_env", "appdeploy_instance", "appdeploy_application",
	)
	store := NewStore(db)
	changes := change.NewStore(db)
	reqRepo := requirement.NewRepository(db)
	h := NewHandler(store, NewDeployer("test"), nil, changes, nil, reqRepo, nil, nil, nil, nil)
	return h, db
}
```

- [ ] **Step 3: 写 3 个失败 HTTP 测试**

在 `handler_http_test.go` 的 `TestHandler_Promote_appNotFound`(line 187-194)之后追加:

```go
// TestHandler_Promote_forbidden 非 gatekeeper/admin → 403(部署权限分离)。
// newRouterWith 固定注入 admin,此处 inline 一个空 roles 的 router 覆盖。
func TestHandler_Promote_forbidden(t *testing.T) {
	h := newHTTPHandlerWithGates(t)
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("roles", []string{}); c.Next() })
	h.Register(r.Group("/api/v1"))
	code, resp := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/promote", nil)
	if code != 403 {
		t.Fatalf("非 admin 应 403,得到 %d body=%v", code, resp)
	}
}

// TestHandler_Promote_changeGateRejected 登记变更未审批 → 409/40920(变更闸门)。
func TestHandler_Promote_changeGateRejected(t *testing.T) {
	h, db := newHTTPHandlerWithGates(t)
	r := newRouterWith(h)
	ctx := context.Background()
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	if _, err := db.ExecContext(ctx,
		`INSERT INTO change_request (id, project_space_id, source_id, kind, output, status)
		 VALUES ('chg_1', 'ps_1', $1, 'code', 'diff', 'pending')`, a.ID); err != nil {
		t.Fatalf("seed change: %v", err)
	}
	code, resp := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/promote", nil)
	if code != 409 {
		t.Fatalf("未审批变更应 409,得到 %d", code)
	}
	if resp["code"].(float64) != 40920 {
		t.Fatalf("业务码应 40920(变更闸门),得到 %v", resp["code"])
	}
}

// TestHandler_Promote_deliveredGateRejected approved 变更 + 来源需求未 delivered → 409/40921(AC7 核心)。
func TestHandler_Promote_deliveredGateRejected(t *testing.T) {
	h, db := newHTTPHandlerWithGates(t)
	r := newRouterWith(h)
	ctx := context.Background()
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	if _, err := db.ExecContext(ctx,
		`INSERT INTO requirement (id, project_space_id, application_id, title, status)
		 VALUES ('req_1', 'ps_1', $1, 't', 'developing')`, a.ID); err != nil {
		t.Fatalf("seed req: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO change_request (id, project_space_id, source_id, kind, output, status)
		 VALUES ('chg_1', 'ps_1', 'req_1', 'code', 'diff', 'approved')`); err != nil {
		t.Fatalf("seed change: %v", err)
	}
	code, resp := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/promote", nil)
	if code != 409 {
		t.Fatalf("approved+未delivered 应 409,得到 %d body=%v", code, resp)
	}
	if resp["code"].(float64) != 40921 {
		t.Fatalf("业务码应 40921(AC7 delivered),得到 %v", resp["code"])
	}
}
```

- [ ] **Step 4: 跑测试验证它失败**

Run:

```bash
cd platform/backend && go test ./internal/appdeploy/ -run TestHandler_Promote -v
```

Expected:

- `TestHandler_Promote_appNotFound` → PASS(已有,回归保护)
- `TestHandler_Promote_forbidden` → PASS(403 现有实现已有,回归保护)
- `TestHandler_Promote_changeGateRejected` → PASS(变更闸门 40920 现有实现已有,回归保护)
- `TestHandler_Promote_deliveredGateRejected` → **FAIL**(现无 AC7 检查,approved+未delivered 会过变更闸门 → 200;期望 409/40921)。此用例红阶段会触发 `go buildAndDeploy` 异步 docker(deployer host 空→失败但不 panic),测试输出可能伴随部署失败日志噪音——属预期,Step 5 实现后此路径在 buildAndDeploy 前 return,噪音消失。

(若 403/40920 也意外失败,先修到绿——它们是前置依赖。)

- [ ] **Step 5: 实现 `Promote` 的 AC7 delivered 前置**

`platform/backend/internal/appdeploy/handler.go` 现有变更闸门块(line 1005-1014):

```go
	if h.changes != nil {
		if hasAny, _ := h.changes.HasAny(c.Request.Context(), aid); hasAny {
			if ok, _ := h.changes.HasApproved(c.Request.Context(), aid); !ok {
				httpx.Err(c, 409, 40920, "需先登记变更并审批通过才能上线 prod（变更闸门）")
				return
			}
			// 上线后:把该应用的 approved 变更标记为 released（从待上线列表消失）
			_ = h.changes.MarkReleased(c.Request.Context(), aid) // 上线后标记 released;失败不阻塞(下次上线再标)
		}
	}
```

改为(在 `HasApproved` 通过分支内、`MarkReleased` 前插入 AC7 检查):

```go
	if h.changes != nil {
		if hasAny, _ := h.changes.HasAny(c.Request.Context(), aid); hasAny {
			if ok, _ := h.changes.HasApproved(c.Request.Context(), aid); !ok {
				httpx.Err(c, 409, 40920, "需先登记变更并审批通过才能上线 prod（变更闸门）")
				return
			}
			// 🚪 AC7 delivered 前置（PRD 2026-07-26 主线闭环收敛 AC7）：
			// approved 变更关联的需求须已 delivered（即已走 release/merge 发布），
			// 堵「变更一审批就 promote、跳过发布」的绕过。查不到需求时放行（grandfather，对称 release 回写）。
			if h.reqRepo != nil {
				if undelivered, _ := h.reqRepo.HasUnDeliveredApprovedByApp(c.Request.Context(), aid); undelivered {
					httpx.Err(c, 409, 40921, "来源需求未交付，请先在发布中心发布上线后再 promote")
					return
				}
			}
			// 上线后:把该应用的 approved 变更标记为 released（从待上线列表消失）
			_ = h.changes.MarkReleased(c.Request.Context(), aid) // 上线后标记 released;失败不阻塞(下次上线再标)
		}
	}
```

- [ ] **Step 6: 跑测试验证通过**

Run:

```bash
cd platform/backend && go test ./internal/appdeploy/ -run TestHandler_Promote -v
```

Expected: 全 PASS(appNotFound + forbidden + changeGateRejected + deliveredGateRejected)。

- [ ] **Step 7: 跑整个 appdeploy 包确认无回归**

Run:

```bash
cd platform/backend && go test ./internal/appdeploy/ -v
```

Expected: PASS(现有 `TestHandler_*` 全过——`newHTTPHandler` 未改,`RegisterChange_changesNil` 等 nil-gate 测试不受影响)。

- [ ] **Step 8: Commit**

```bash
git add platform/backend/internal/appdeploy/handler.go platform/backend/internal/appdeploy/handler_http_test.go
git commit -F - <<'EOF'
feat(appdeploy): Promote 加来源需求 delivered 前置(AC7)

变更闸门通过后、MarkReleased 前调
HasUnDeliveredApprovedByApp:approved 变更关联需求须 delivered,
否则 409/40921(堵跳过 release 直接上线)。grandfather 对称 release。
新 fixture newHTTPHandlerWithGates(不动 newHTTPHandler 的 nil-gate 测试)。
HTTP 拒绝路径全覆盖(403/40920/40921)。

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
```

---

## Task 3: 全量回归 + .28 端到端 dogfood(AC6)

**Files:** 无代码改动(验证 + 部署)。

- [ ] **Step 1: 全量后端单测回归**

Run:

```bash
cd platform/backend && go test ./...
```

Expected: 全 PASS。重点关注 `internal/requirement/`、`internal/appdeploy/`、`internal/release/`、`internal/change/` 四包(AC7 触及的反查链)。

- [ ] **Step 2: 部署到 .28(scp + 重建后端容器)**

按 memory `deploy-prod-10.10.0.28`:keyless SSH 到 10.10.0.28,源码在 `/opt/anp`,scp 改动 → `docker-compose` 重建后端容器,入口 8088。

```bash
# 本机:打包后端改动 scp 到 .28(或推 origin 后 .28 拉取)
scp -r platform/backend <28>:/opt/anp/platform/backend
ssh <28> 'cd /opt/anp && docker-compose up -d --build backend'
```

确认后端容器启动日志无报错。

- [ ] **Step 3: dogfood AC6-拒绝路径(approved 未 released → promote 409/40921)**

在 .28 上,取一条「approved 变更但来源需求未 delivered」的应用:

```bash
# 经前端或 curl,带 admin token
curl -X POST http://10.10.0.28:8088/api/v1/project-spaces/<ps>/apps/<aid>/promote -H "Authorization: Bearer <admin>"
```

Expected: `409`,body `code=40921`,msg「来源需求未交付,请先在发布中心发布上线后再 promote」。

- [ ] **Step 4: dogfood AC6-通过路径(走完 release 后 promote 200)**

对同一应用,在发布中心对该 approved 变更执行发布(需求 → delivered):

```bash
curl -X POST http://10.10.0.28:8088/api/v1/project-spaces/<ps>/releases -H "Authorization: Bearer <admin>" -d '{"change_id":"<chgid>"}'
# 确认 requirement.status=delivered
```

再 promote:

```bash
curl -X POST http://10.10.0.28:8088/api/v1/project-spaces/<ps>/apps/<aid>/promote -H "Authorization: Bearer <admin>"
```

Expected: `200`,`status=building`(delivered 检查放行,进入正常上线)。

- [ ] **Step 5: 记录验证结果**

把 AC6 拒绝/通过两路径的实际响应记入本计划末尾「验证记录」段(或 bug 文档目录),标注闭环。

---

## 验证记录

(Task 3 执行后填写)

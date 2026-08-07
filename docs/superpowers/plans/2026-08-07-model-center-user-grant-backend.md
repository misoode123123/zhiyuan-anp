# 模型中心 · 用户模型授权（后端）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 后端落地「管理员给用户授权模型 + 模型调用前授权校验」——新增 `user_model_grant` 表、Store 授权方法、Gateway/`/code` 两处防线授权校验、授权 HTTP API、service 透传 `model`+`userID`。

**Architecture:** 复用已支持 `req.Model` 的 `compute.Gateway`（`route.go:125`）。新增 `user_model_grant(user_id, model_id)` 授权表 + `compute.Store` 授权 CRUD。Gateway 在 `req.Model != ""` 时校验 `IsGranted(userID, model)`（越权即拒，不 fallback）。`/code` handler 同样校验。新增 `GrantHandler` 暴露授权管理 API。`requirement`/`qa` service 透传 `model`+`userID` 到 `ChatRequest`。

**Tech Stack:** Go 1.x · gin · sqlx · Postgres (pgvector/pg16) · 现有 `internal/compute` 包。

**Spec:** `docs/superpowers/specs/2026-08-07-model-center-user-grant-design.md`（§3 数据模型 / §4 后端设计）

## Global Constraints

（摘自 spec §6「开发标准与规范.md」，每个任务隐含遵守）

- **Go**：`golangci-lint` + `gofmt`；`context.Context` 贯穿；错误显式 `error` 返回；package 边界（跨域调 Service 接口）。
- **SQL**：迁移版本化（`000035`）、幂等 `IF NOT EXISTS`、审计字段（`created_at`）、`ON DELETE CASCADE` 防悬挂引用。
- **鉴权**：新端点的 RBAC 经 `auth/guard.go` 的 `routeOps` 集中登记（`config.manage`），由 `AutoRequire` 中间件统一校验——不在 handler 内手写鉴权。
- **安全**：越权即拒，**不 fallback 到路由默认**；错误信息不含 key / 内部细节。
- **提交**：Conventional Commits，subject 简短；body 每行 ≤100 字符；AI 提交带 `Co-Authored-By: Claude <noreply@anthropic.com>`。
- **测试**：连真实 PG（`testutil.TestDB(t)`，参照现有 `internal/compute/*_test.go`）；**串行** `go test -p 1 -count=1 ./internal/compute/... ./internal/requirement/... ./internal/qa/...`。

## File Structure

- **Create** `internal/db/migrations/pg/000035_user_model_grant.up.sql` / `.down.sql` — 授权表 DDL。
- **Create** `internal/compute/grant.go` — `Store` 的授权 CRUD（`ListGrants`/`GrantModels`/`RevokeModel`/`IsGranted`）。
- **Create** `internal/compute/grant_test.go` — 授权 Store 测试。
- **Modify** `internal/compute/route.go` — `ChatRequest` 增 `UserID`；`Chat` 增授权校验；定义 `ErrModelNotAuthorized`。
- **Create** `internal/compute/route_auth_test.go` — Gateway 授权校验测试。
- **Create** `internal/compute/grant_handler.go` — `GrantHandler`：4 个授权管理端点 + `Register`。
- **Modify** `internal/auth/guard.go` — `routeOps` 增授权端点映射。
- **Modify** `internal/requirement/service.go` — `CreateInput` 增 `Model`/`UserID`；`generateSpec` 透传。
- **Modify** `internal/qa/service.go` — `GenerateTests` 增 `model`/`userID` 参数。
- **Modify** `internal/dev/handler.go` — `Handler` 增 `compute.Store`；`Code` 加授权校验。
- **Modify** `cmd/server/main.go` — 装配 `GrantHandler` + 给 `dev.Handler` 注入 `computeStore`。
- **Modify** `platform/backend/docs/swagger.json` / `api-types.ts` — 同步新端点（Task 4 末尾）。

---

### Task 1: 授权表 migration `000035`

**Files:**

- Create: `platform/backend/internal/db/migrations/pg/000035_user_model_grant.up.sql`
- Create: `platform/backend/internal/db/migrations/pg/000035_user_model_grant.down.sql`

**Interfaces:**

- Produces: 表 `user_model_grant(user_id, model_id, granted_by, created_at)`，`PK(user_id, model_id)`，`model_id REFERENCES compute_model(id) ON DELETE CASCADE`。

- [ ] **Step 1: 写 up 迁移**

`000035_user_model_grant.up.sql`：

```sql
CREATE TABLE IF NOT EXISTS user_model_grant (
    user_id     TEXT        NOT NULL,
    model_id    TEXT        NOT NULL REFERENCES compute_model(id) ON DELETE CASCADE,
    granted_by  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, model_id)
);
```

- [ ] **Step 2: 写 down 迁移**

`000035_user_model_grant.down.sql`：

```sql
DROP TABLE IF EXISTS user_model_grant;
```

- [ ] **Step 3: 本地启动后端验证迁移生效**

确认迁移文件被 `internal/db/migrate.go` 的 `//go:embed migrations/pg/*.sql` 自动收录（按文件名排序）。起后端（或连 .28 PG 跑迁移）后验证表存在：

```bash
# 在 .28 上（或本地 PG）验证
psql "postgres://anp:***@localhost:5432/anp" -c "\d user_model_grant"
# 预期：列出 user_id/model_id/granted_by/created_at 列，model_id 有外键
```

- [ ] **Step 4: Commit**

```bash
git add platform/backend/internal/db/migrations/pg/000035_user_model_grant.up.sql \
        platform/backend/internal/db/migrations/pg/000035_user_model_grant.down.sql
git commit -m "feat(db): 用户模型授权表 user_model_grant (migration 000035)"
```

---

### Task 2: Store 授权方法（`grant.go`）

**Files:**

- Create: `platform/backend/internal/compute/grant.go`
- Test: `platform/backend/internal/compute/grant_test.go`

**Interfaces:**

- Consumes: `compute.Store`（`store.go:11`，持 `db *sqlx.DB`）、`compute.Model`（`provider.go`，字段 `id/provider_id/name/display_name/modality/...`）。
- Produces:
  - `func (s *Store) ListGrants(ctx context.Context, userID string) ([]Model, error)`
  - `func (s *Store) GrantModels(ctx context.Context, userID string, modelIDs []string, grantedBy string) error`
  - `func (s *Store) RevokeModel(ctx context.Context, userID, modelID string) error`
  - `func (s *Store) IsGranted(ctx context.Context, userID, modelID string) (bool, error)`

- [ ] **Step 1: 写失败测试（`grant_test.go`）**

参照现有 `internal/compute/*_test.go` 的 `testutil.TestDB(t)` 初始化。准备一个 provider + 一个 model 作为前置。

```go
package compute_test

import (
	"context"
	"testing"

	"zhiyuan-anp/platform/backend/internal/compute"
	"zhiyuan-anp/platform/backend/internal/testutil"
)

func TestGrantModels_AndListRevokeIsGranted(t *testing.T) {
	db := testutil.TestDB(t) // 连 .28 anp_test PG（参照现有 compute 测试）
	s := compute.NewStore(db)
	ctx := context.Background()

	// 前置：一个 provider + 一个 model（用 SeedProviders 已种入的，或此处插入）
	prov := &compute.Provider{ID: "t_prov_grant", Name: "t-prov", Type: "api", BaseURL: "http://x", APIKey: "k"}
	if err := s.CreateProvider(ctx, prov); err != nil {
		t.Fatal(err)
	}
	mdl := &compute.Model{ID: "t_mdl_grant", ProviderID: prov.ID, Name: "t-model", Modality: "text"}
	if err := s.CreateModel(ctx, mdl); err != nil {
		t.Fatal(err)
	}

	// 授权前：未授权
	if ok, _ := s.IsGranted(ctx, "u1", mdl.ID); ok {
		t.Fatal("授权前不应 IsGranted")
	}

	// 授权
	if err := s.GrantModels(ctx, "u1", []string{mdl.ID}, "admin"); err != nil {
		t.Fatalf("GrantModels: %v", err)
	}

	// 授权后：IsGranted=true
	ok, _ := s.IsGranted(ctx, "u1", mdl.ID)
	if !ok {
		t.Fatal("授权后应 IsGranted")
	}

	// ListGrants 返回该模型
	list, err := s.ListGrants(ctx, "u1")
	if err != nil || len(list) != 1 || list[0].ID != mdl.ID {
		t.Fatalf("ListGrants 期望 1 个 %s，got %+v err=%v", mdl.ID, list, err)
	}

	// 重复授权幂等（ON CONFLICT DO NOTHING）
	if err := s.GrantModels(ctx, "u1", []string{mdl.ID}, "admin"); err != nil {
		t.Fatalf("重复授权应幂等: %v", err)
	}
	list, _ = s.ListGrants(ctx, "u1")
	if len(list) != 1 {
		t.Fatalf("重复授权后仍应 1 个，got %d", len(list))
	}

	// 收回
	if err := s.RevokeModel(ctx, "u1", mdl.ID); err != nil {
		t.Fatalf("RevokeModel: %v", err)
	}
	ok, _ = s.IsGranted(ctx, "u1", mdl.ID)
	if ok {
		t.Fatal("收回后不应 IsGranted")
	}

	// 清理（CASCADE 测试见下）
	s.DeleteProvider(ctx, prov.ID) // 删 provider 级联删 model
}

func TestGrantModels_CascadeOnDeleteModel(t *testing.T) {
	db := testutil.TestDB(t)
	s := compute.NewStore(db)
	ctx := context.Background()
	prov := &compute.Provider{ID: "t_prov_casc", Name: "t-prov-c", Type: "api", BaseURL: "http://x", APIKey: "k"}
	s.CreateProvider(ctx, prov)
	mdl := &compute.Model{ID: "t_mdl_casc", ProviderID: prov.ID, Name: "t-c", Modality: "text"}
	s.CreateModel(ctx, mdl)
	s.GrantModels(ctx, "u2", []string{mdl.ID}, "admin")

	s.DeleteModel(ctx, mdl.ID) // 删 model → CASCADE 删授权

	if ok, _ := s.IsGranted(ctx, "u2", mdl.ID); ok {
		t.Fatal("删 model 后授权应被级联删除")
	}
	s.DeleteProvider(ctx, prov.ID)
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
cd platform/backend && go test -p 1 -count=1 -run TestGrantModels ./internal/compute/...
```

预期：FAIL（`ListGrants undefined` 等编译错误）。

- [ ] **Step 3: 实现 `grant.go`**

```go
package compute

import (
	"context"

	"github.com/jmoiron/sqlx"
)

// ListGrants 返回某用户已授权的模型（JOIN compute_model 取详情），按 model name 排序。
func (s *Store) ListGrants(ctx context.Context, userID string) ([]Model, error) {
	var out []Model
	const q = `SELECT m.id, m.provider_id, m.name, m.display_name, m.modality,
	                  m.context_window, m.max_output, m.cost_input, m.cost_output,
	                  m.capabilities, m.enabled
	           FROM user_model_grant g JOIN compute_model m ON m.id = g.model_id
	           WHERE g.user_id = $1 ORDER BY m.name`
	if err := sqlx.SelectContext(ctx, s.db, &out, q, userID); err != nil {
		return nil, err
	}
	return out, nil
}

// GrantModels 批量授权（幂等：ON CONFLICT DO NOTHING）。
func (s *Store) GrantModels(ctx context.Context, userID string, modelIDs []string, grantedBy string) error {
	if len(modelIDs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	const q = `INSERT INTO user_model_grant (user_id, model_id, granted_by)
	           VALUES ($1, $2, $3) ON CONFLICT (user_id, model_id) DO NOTHING`
	for _, mid := range modelIDs {
		if _, err := tx.ExecContext(ctx, q, userID, mid, grantedBy); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RevokeModel 收回单个授权。
func (s *Store) RevokeModel(ctx context.Context, userID, modelID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM user_model_grant WHERE user_id = $1 AND model_id = $2`, userID, modelID)
	return err
}

// IsGranted 单点校验：该用户是否被授权此模型。
func (s *Store) IsGranted(ctx context.Context, userID, modelID string) (bool, error) {
	var exists bool
	err := s.db.GetContext(ctx, &exists,
		`SELECT EXISTS(SELECT 1 FROM user_model_grant WHERE user_id = $1 AND model_id = $2)`,
		userID, modelID)
	return exists, err
}
```

> 注意 `SelectContext`/`GetContext`/`BeginTxx` 用法须与现有 `store.go`/`provider.go` 一致（现有代码用 `s.db.SelectContext`/`s.db.GetContext`——按实际调整为 `s.db.Select` 等现有惯例；若 Store 不暴露 `db` 字段，复用现有 helper）。

- [ ] **Step 4: 跑测试确认通过**

```bash
cd platform/backend && go test -p 1 -count=1 -run TestGrantModels ./internal/compute/...
```

预期：PASS。若 SQL/字段名报错，对照 `compute_model` 列名（`000011` 迁移）修正。

- [ ] **Step 5: Commit**

```bash
git add platform/backend/internal/compute/grant.go platform/backend/internal/compute/grant_test.go
git commit -m "feat(compute): Store 授权 CRUD (ListGrants/GrantModels/RevokeModel/IsGranted)"
```

---

### Task 3: Gateway 授权校验

**Files:**

- Modify: `platform/backend/internal/compute/route.go`（`ChatRequest` ~`:100-105`、`Chat` ~`:125-143`）
- Test: `platform/backend/internal/compute/route_auth_test.go`

**Interfaces:**

- Consumes: `Store.IsGranted`（Task 2）。
- Produces: `ChatRequest` 增 `UserID string`；导出 `var ErrModelNotAuthorized = errors.New(...)`；`Chat` 在 `req.Model != "" && req.UserID != ""` 时校验。

- [ ] **Step 1: 写失败测试（`route_auth_test.go`）**

```go
package compute_test

import (
	"context"
	"errors"
	"testing"

	"zhiyuan-anp/platform/backend/internal/compute"
	"zhiyuan-anp/platform/backend/internal/testutil"
)

func TestChat_RejectsUnauthorizedModel(t *testing.T) {
	db := testutil.TestDB(t)
	s := compute.NewStore(db)
	gw := compute.NewGateway(s)
	ctx := context.Background()

	prov := &compute.Provider{ID: "t_prov_auth", Name: "t-prov-a", Type: "api", BaseURL: "http://x", APIKey: "k"}
	s.CreateProvider(ctx, prov)
	mdl := &compute.Model{ID: "t_mdl_auth", ProviderID: prov.ID, Name: "t-a", Modality: "text"}
	s.CreateModel(ctx, mdl)
	// 注意：不给 u1 授权 mdl

	_, err := gw.Chat(ctx, compute.ChatRequest{
		TaskType: "spec",
		Model:    mdl.ID,   // 指定模型
		UserID:   "u1",     // 但未授权
		Messages: []map[string]interface{}{{"role": "user", "content": "hi"}},
	})
	if !errors.Is(err, compute.ErrModelNotAuthorized) {
		t.Fatalf("期望 ErrModelNotAuthorized，got %v", err)
	}
	s.DeleteProvider(ctx, prov.ID)
}

func TestChat_EmptyUserID_NoCheck(t *testing.T) {
	db := testutil.TestDB(t)
	s := compute.NewStore(db)
	gw := compute.NewGateway(s)
	// 空 UserID（兼容老调用）→ 不校验授权，走正常转发（会因 base_url 假而失败，但不是 ErrModelNotAuthorized）
	_, err := gw.Chat(context.Background(), compute.ChatRequest{
		TaskType: "spec",
		Model:    "any",
		UserID:   "", // 空 → 不校验
		Messages: []map[string]interface{}{{"role": "user", "content": "hi"}},
	})
	if errors.Is(err, compute.ErrModelNotAuthorized) {
		t.Fatal("空 UserID 不应触发授权拒绝")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
cd platform/backend && go test -p 1 -count=1 -run TestChat_ ./internal/compute/...
```

预期：FAIL（`ErrModelNotAuthorized undefined`、`ChatRequest` 无 `UserID`）。

- [ ] **Step 3: 实现——`ChatRequest` 增字段、定义错误、`Chat` 增校验**

`route.go` 顶部 import 加 `"errors"`，并在 `ChatRequest` 定义处（`:100-105`）增字段：

```go
type ChatRequest struct {
	TaskType       string
	Model          string
	Messages       []map[string]interface{}
	ProjectSpaceID string
	UserID         string // 新增：用于授权校验（空=兼容老调用，不校验）
}

// 授权拒绝哨兵错误（不 fallback 到路由，明确拒绝）。
var ErrModelNotAuthorized = errors.New("model not authorized for user")
```

在 `Chat`（`:125`）的 `req.Model != ""` 分支开头（`route.go:126` 附近，现有「if req.Model != "" → 用该 model」逻辑前）插入：

```go
func (g *Gateway) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// —— 授权校验（新增）——
	if req.Model != "" && req.UserID != "" {
		ok, err := g.store.IsGranted(ctx, req.UserID, req.Model)
		if err != nil {
			// 记 warn，不泄露内部细节；按未授权处理（保守拒绝）
			return nil, ErrModelNotAuthorized
		}
		if !ok {
			return nil, ErrModelNotAuthorized // 越权即拒，不 fallback 到路由
		}
	}
	// —— 以下为现有逻辑（req.Model 选模型 / 否则走 route）保持不变 ——
	...
}
```

> 具体插入点：现有 `Chat` 在 `req.Model != ""` 时直接确定 model。把授权校验放在最前（方法入口），保证无论走 model 还是 route，只要指定了 model+userID 就先校验。其余逻辑不动。

- [ ] **Step 4: 跑测试确认通过**

```bash
cd platform/backend && go test -p 1 -count=1 -run TestChat_ ./internal/compute/...
```

预期：PASS。

- [ ] **Step 5: Commit**

```bash
git add platform/backend/internal/compute/route.go platform/backend/internal/compute/route_auth_test.go
git commit -m "feat(compute): Gateway 授权校验 (ChatRequest.UserID + IsGranted)"
```

---

### Task 4: 授权管理 HTTP API（`GrantHandler`）

**Files:**

- Create: `platform/backend/internal/compute/grant_handler.go`
- Modify: `platform/backend/internal/auth/guard.go`（`routeOps` ~`:14`）
- Modify: `platform/backend/cmd/server/main.go`（注册 GrantHandler ~`:282-285`）

**Interfaces:**

- Consumes: `compute.Store`（授权方法）、`auth/guard.go` 的 `routeOps` + `AutoRequire`（已挂 `/api/v1`）、gin。
- Produces: 4 个端点：
  - `GET /users/:id/models` → 该用户已授权模型（管理员）
  - `POST /users/:id/models` `{model_ids:[]}` → 批量授权（管理员）
  - `DELETE /users/:id/models/:model_id` → 收回（管理员）
  - `GET /users/me/models` → 当前用户可用模型（登录即可）

- [ ] **Step 1: 写 `grant_handler.go`**

```go
package compute

import (
	"github.com/gin-gonic/gin"
	"zhiyuan-anp/platform/backend/internal/auth"
	"zhiyuan-anp/platform/backend/internal/httpx"
)

// GrantHandler 用户模型授权管理 HTTP 接口。
type GrantHandler struct {
	store *Store
}

func NewGrantHandler(store *Store) *GrantHandler { return &GrantHandler{store: store} }

// Register 注册授权路由（挂 v1 组，受 AutoRequire 保护）。
func (h *GrantHandler) Register(r gin.IRouter) {
	r.GET("/users/:id/models", h.list)
	r.POST("/users/:id/models", h.grant)
	r.DELETE("/users/:id/models/:model_id", h.revoke)
	r.GET("/users/me/models", h.listMine)
}

// list 查某用户已授权模型（管理员）。
func (h *GrantHandler) list(c *gin.Context) {
	list, err := h.store.ListGrants(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50003, err.Error())
		return
	}
	httpx.OK(c, list)
}

type grantReq struct {
	ModelIDs []string `json:"model_ids"`
}

// grant 批量授权（管理员）。granted_by 取当前用户。
func (h *GrantHandler) grant(c *gin.Context) {
	var body grantReq
	if err := c.ShouldBindJSON(&body); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	grantedBy := c.GetString(auth.CtxUserDBID)
	if err := h.store.GrantModels(c.Request.Context(), c.Param("id"), body.ModelIDs, grantedBy); err != nil {
		httpx.Err(c, 500, 50003, err.Error())
		return
	}
	httpx.OK(c, gin.H{"granted": len(body.ModelIDs)})
}

// revoke 收回单个授权（管理员）。
func (h *GrantHandler) revoke(c *gin.Context) {
	if err := h.store.RevokeModel(c.Request.Context(), c.Param("id"), c.Param("model_id")); err != nil {
		httpx.Err(c, 500, 50003, err.Error())
		return
	}
	httpx.OK(c, gin.H{"revoked": c.Param("model_id")})
}

// listMine 当前登录用户的可用模型（前端下拉用；仅需登录）。
func (h *GrantHandler) listMine(c *gin.Context) {
	uid := c.GetString(auth.CtxUserDBID)
	list, err := h.store.ListGrants(c.Request.Context(), uid)
	if err != nil {
		httpx.Err(c, 500, 50003, err.Error())
		return
	}
	httpx.OK(c, list)
}
```

> `CtxUserDBID` 为现有 gin context key（`dev/handler.go:53` 已用），确认常量名一致。

- [ ] **Step 2: 在 `guard.go` 的 `routeOps` 增映射（管理员授权操作 → `config.manage`）**

在 `// 算力中心...` 块附近（`:49` 一带）追加：

```go
	// 用户模型授权管理（admin；GET /users/:id/models 也是管理员视角，需保护）
	"GET /api/v1/users/:id/models":            "config.manage",
	"POST /api/v1/users/:id/models":           "config.manage",
	"DELETE /api/v1/users/:id/models/:model_id": "config.manage",
	// GET /users/me/models 不登记 → 任意登录用户放行（仅需登录态）
```

- [ ] **Step 3: 在 `main.go` 装配注册**

在 `compute.NewGateway(computeStore)`（`main.go:284`）附近，注册 GrantHandler（`computeStore` 变量已存在）：

```go
compute.NewGrantHandler(computeStore).Register(v1)
```

> 注册顺序：放在 `compute` 路由组注册之后（`main.go:282-283` 区域）。`v1` 为已鉴权的 `/api/v1` 路由组（`AutoRequire` 已挂）。

- [ ] **Step 4: 启动后端，curl 验证 4 端点**

```bash
# 登录拿 token（admin）
TOK=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" -d '{"name":"admin","password":"admin123"}' \
  | python -c "import sys,json;print(json.load(sys.stdin)['data']['token'])")

# me/models（当前用户可用模型）
curl -s -H "Authorization: Bearer $TOK" http://localhost:8080/api/v1/users/me/models
# 预期：{"code":0,"data":[...]}（admin 可能无授权→空数组）

# 授权（需先有 compute_model，用 SeedProviders 种入的 model id）
curl -s -X POST -H "Authorization: Bearer $TOK" \
  -H "Content-Type: application/json" \
  -d '{"model_ids":["<某 model id>"]}' \
  http://localhost:8080/api/v1/users/<某 user id>/models
# 预期：{"code":0,"data":{"granted":1}}

# 非 admin 调 POST /users/:id/models → 403（AutoRequire 拦）
```

- [ ] **Step 5: 同步 swagger / api-types（如团队约定前端类型由 swagger 生成）**

```bash
# 若用 swag，重新生成（参照团队现有生成命令）；否则手补 platform/backend/docs/swagger.json
# platform/frontend 的 api-types.ts 若由 swagger 生成则一并更新
```

- [ ] **Step 6: Commit**

```bash
git add platform/backend/internal/compute/grant_handler.go \
        platform/backend/internal/auth/guard.go \
        platform/backend/cmd/server/main.go
git commit -m "feat(compute): GrantHandler 授权管理 API + RBAC 映射"
```

---

### Task 5: service / dev 透传 model+userID 与 `/code` 校验

**Files:**

- Modify: `platform/backend/internal/requirement/service.go`（`CreateInput` `:49-54`、`generateSpec` 调 gateway `:297-302`）
- Modify: `platform/backend/internal/qa/service.go`（`GenerateTests` `:61`、调 gateway `:70-74`）
- Modify: `platform/backend/internal/dev/handler.go`（`Handler` `:11`、`Code` `:46-63`）
- Modify: `platform/backend/cmd/server/main.go`（`dev.Register` `:298` 改为注入 computeStore 的 Handler）

**Interfaces:**

- Consumes: `compute.ChatRequest.UserID/Model`（Task 3）、`compute.Store.IsGranted`（Task 2）、gin context `auth.CtxUserDBID`。
- Produces: `requirement.CreateInput` 增 `Model`/`UserID`；`qa.GenerateTests` 增 `model`/`userID` 形参；`dev.Handler` 持 `compute.Store` 并在 `Code` 校验。

- [ ] **Step 1: requirement — `CreateInput` 增字段 + `generateSpec` 透传**

`requirement/service.go`，`CreateInput` 增两字段：

```go
type CreateInput struct {
	ProjectSpaceID string
	ApplicationID  string
	Description    string
	Images         []string
	Model          string // 新增：用户选的模型（空=走 route）
	UserID         string // 新增：授权校验用
}
```

`generateSpec`（调用 gateway 处，`:297-302`）把 `Model`/`UserID` 透传进 `ChatRequest`。`generateSpec` 需能拿到 `in.Model`/`in.UserID`——把它作为参数传入，或改 `Create` 内联传参。最简：`generateSpec` 签名增 `model, userID string`：

```go
// Create 内（:75）：
spec, usage, err := s.generateSpec(ctx, in.Description, in.Images, in.Model, in.UserID)

// generateSpec 签名与 gateway 调用：
func (s *Service) generateSpec(ctx context.Context, desc string, images []string, model, userID string) (*specResult, *usageInfo, error) {
	...
	if s.gateway != nil && len(images) == 0 {
		resp, err := s.gateway.Chat(ctx, compute.ChatRequest{
			TaskType: "spec",
			Model:    model,  // 新增
			UserID:   userID, // 新增
			Messages: messages,
		})
		...
	}
}
```

- [ ] **Step 2: requirement handler 调 `Create` 时传 model/userID**

找到调用 `reqSvc.Create(ctx, requirement.CreateInput{...})` 的 handler（`internal/requirement/handler.go`），在构造 `CreateInput` 时增：

```go
Model:  c.GetString("model"),       // 前端 body 的 model 字段（若无则空）
UserID: c.GetString(auth.CtxUserDBID),
```

> handler 怎么读 body 里的 model：若 handler 用 `ShouldBindJSON` 绑到 struct，给该 struct 加 `Model string \`json:"model"\``；若前端没传 model 则空（走 route）。

- [ ] **Step 3: qa — `GenerateTests` 增参数 + 透传**

`qa/service.go:61` 签名增 `model, userID string`：

```go
func (s *Service) GenerateTests(ctx context.Context, projectSpaceID, requirementID, title, acceptanceCriteria, model, userID string) ([]TestCase, error) {
	...
	if s.gateway != nil {
		resp, err := s.gateway.Chat(ctx, compute.ChatRequest{
			TaskType: "test",
			Model:    model,  // 新增
			UserID:   userID, // 新增
			Messages: messages,
		})
		...
	}
}
```

调用方（qa handler）同步加传 `model`/`userID`（从 gin context 取 `auth.CtxUserDBID`；model 从 body 读，缺则空）。

- [ ] **Step 4: dev — `Handler` 注入 `compute.Store` + `Code` 加授权校验**

`dev/handler.go`：

```go
type Handler struct {
	agent *CodingAgent
	grant computeGrantChecker // 新增：授权校验（接口，便于测试）
}

// computeGrantChecker 局部接口，仅要 IsGranted（解耦对 compute.Store 的直接依赖）。
type computeGrantChecker interface {
	IsGranted(ctx context.Context, userID, modelID string) (bool, error)
}

func NewHandler(agent *CodingAgent, grant computeGrantChecker) *Handler {
	return &Handler{agent: agent, grant: grant}
}
```

`Code`（`:46`）在 `Submit` 前加授权校验（`req.Model` 非空时）：

```go
func (h *Handler) Code(c *gin.Context) {
	var req codeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	// 授权校验：dev 派发用指定模型时，先校验该用户是否被授权（防绕过 Gateway 的漏网入口）
	if req.Model != "" && h.grant != nil {
		uid := c.GetString(auth.CtxUserDBID)
		if ok, _ := h.grant.IsGranted(c.Request.Context(), uid, req.Model); !ok {
			httpx.Err(c, 403, 40302, "无权使用该模型")
			return
		}
	}
	psID := c.GetString("project_space_id")
	t, err := h.agent.Submit(c.Request.Context(), psID, c.GetString(auth.CtxUserDBID), "code", "", req.RepoDir, req.Prompt, req.Model)
	...
}
```

> `Register`（`:19`、`:22`）签名同步改：`func Register(r gin.IRouter, agent *CodingAgent, grant computeGrantChecker)`。

- [ ] **Step 5: `main.go` 装配——给 `dev` 注入 `computeStore`**

`main.go:298` 原 `dev.Register(v1, devAgent)` 改为：

```go
dev.Register(v1, devAgent, computeStore) // computeStore 实现 IsGranted
```

> `computeStore`（`*compute.Store`）已含 `IsGranted`（Task 2），满足 `computeGrantChecker` 接口。

- [ ] **Step 6: 编译 + 跑全量相关测试**

```bash
cd platform/backend && go build ./...
go test -p 1 -count=1 ./internal/compute/... ./internal/requirement/... ./internal/qa/... ./internal/dev/...
```

预期：编译通过，测试全绿。若 `requirement`/`qa` 现有测试因 `GenerateTests`/`generateSpec` 签名变更失败，补上空 `model`/`userID` 参数（"" 即走原 route 行为，兼容）。

- [ ] **Step 7: Commit**

```bash
git add platform/backend/internal/requirement/ platform/backend/internal/qa/ \
        platform/backend/internal/dev/handler.go platform/backend/cmd/server/main.go
git commit -m "feat: service/dev 透传 model+userID 并在 /code 前授权校验"
```

---

## Self-Review

**1. Spec 覆盖：**

- §3 数据模型（user_model_grant）→ Task 1 ✓
- §4.1 Store 授权方法 → Task 2 ✓
- §4.2 Gateway 授权校验 → Task 3 ✓
- §4.3 service 透传 model+userID、`/code` 校验 → Task 5 ✓
- §4.4 HTTP API（4 端点）→ Task 4 ✓
- §4.5 codews 编码工具模型控制 → **不在本 plan**（属 Plan 2：前端 + codews 注入），已声明。

**2. Placeholder 扫描：**

- Task 4 Step 5 swagger 生成——给的是"参照团队现有生成命令"，非占位；若团队无自动生成，手补 swagger（已在 step 注明）。
- 无 TBD/TODO。

**3. 类型一致性：**

- `IsGranted(ctx, userID, modelID) (bool, error)` 在 Task 2 定义，Task 3/5 调用一致 ✓
- `ChatRequest.UserID string` Task 3 定义，Task 5 透传一致 ✓
- `GrantHandler`/`NewGrantHandler` Task 4 定义，main.go 装配一致 ✓
- `dev.computeGrantChecker` 接口与 `compute.Store.IsGranted` 签名匹配 ✓
- `CreateInput.Model/UserID` 与 handler 透传一致 ✓

**已知实现时需对照实际处（plan 已给锚点，非占位）：**

- `testutil.TestDB` 的确切返回与现有 compute 测试初始化模式（Task 2/3 测试开头注明"参照现有"）。
- `s.db.SelectContext` vs 现有 `store.go` 的查询 helper 习惯（Task 2 Step 3 注明按现有惯例调整）。
- requirement/qa handler 读 body `model` 的具体绑定方式（Task 5 Step 2/3 注明）。

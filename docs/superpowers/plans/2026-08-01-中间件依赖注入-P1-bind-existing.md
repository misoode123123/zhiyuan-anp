# 中间件依赖注入 P1（bind_existing 闭环）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 opencode 适配后读取 `REDIS_ADDR`/`MILVUS_ADDR` 的应用，部署时由平台自动注入这些 env（指向 .28 已有 yxt-redis:6381 / yxt-milvus:19530），闭环「导入适配最后一公里」。

**Architecture:** 扩 pgsupply 范式到新模块 `internal/mwsupply/`：仓库根 `.anp/deps.yaml`（opencode 适配回写）声明依赖 → `buildAndDeploy` 内、`EnvPairs()` 读表前调 `mwsupply.Reconcile`，按 bind_existing 查实例注册表 → 经 `EnvWriter.UpsertEnv(source=platform)` 写 `appdeploy_env` → 现有 `docker run -e` 注入（部署主流程零改）。顺带给 `appdeploy_env` 加 `source` 列 + 部署 reconcile 平台优先，治用户面板误改隐患。

**Tech Stack:** Go（gin + sqlx + pgx）、PostgreSQL、`gopkg.in/yaml.v3`、embed 迁移、Next.js（前端 env 面板）。

**关联设计：** `docs/superpowers/specs/2026-08-01-中间件依赖供给与注入-design.md`

## Global Constraints

- **禁 SQLite，只用 PG**：测试用 `testutil.TestDB(t)`（连 anp_test PG，跑全迁移建表），不用 sqlite :memory:（PG 类型陷阱）。全量 `go test ./...` 用 `-p 1` 串行（防并发污染 anp_test 库）。
- **迁移版本 000028**：`migrations/pg/000028_mwsupply.up.sql` + `.down.sql`，embed 自动拾取（`//go:embed migrations/pg/*.sql`），`schema_migrations` 幂等。**前置检查**：000028 曾被已 revert 的 Analyzer 占用，实现前先 `SELECT * FROM schema_migrations WHERE version='000028'` 确认无孤儿记录；若有，改用 000029（同步改本计划文件名）。
- **PG 占位符 `$N`**：所有 SQL 用 `$1,$2,...`（PG 语法，非 `?`）。
- **`source` 取值**：`'user'`（用户面板）/'platform'（pgsupply/mwsupply 注入）。新列 `NOT NULL DEFAULT 'user'`（存量行回填 user）。
- **不破坏现有链路**：pgsupply 的 `DATABASE_URL` 注入路径只加 `source` 参数；`Deployer`/`EnvPairs`/`buildAndDeploy` 主流程不改（仅在 EnvPairs 前插一句 Reconcile）。
- **避免循环依赖**：appdeploy 不导入 mwsupply——在 appdeploy 定义 `MWReconciler` 接口注入（仿 `AdaptSubmitter`）；mwsupply 定义自己的 `EnvWriter` 接口，`appdeploy.Store` 满足之。

---

## File Structure

| 文件                                                             | 动作 | 责任                                                                                                                      |
| ---------------------------------------------------------------- | ---- | ------------------------------------------------------------------------------------------------------------------------- |
| `internal/db/migrations/pg/000028_mwsupply.up.sql` / `.down.sql` | 新增 | 2 表 + `appdeploy_env.source` + .28 种子                                                                                  |
| `internal/appdeploy/model.go`                                    | 改   | `EnvVar` 加 `Source` 字段                                                                                                 |
| `internal/appdeploy/store.go`                                    | 改   | `UpsertEnv` 加 `source` 参；`envCols`/`ListEnv` 带 source；新增 `GetEnvSource`                                            |
| `internal/appdeploy/handler.go`                                  | 改   | HTTP `UpsertEnv`/`DeleteEnv` 加 source + 平台保护；`MWReconciler` 接口 + `SetMwReconciler`；`buildAndDeploy` 插 Reconcile |
| `internal/pgsupply/provisioner.go`                               | 改   | `EnvWriter` 接口 + `Provision` 调用加 `source`                                                                            |
| `internal/mwsupply/model.go`                                     | 新增 | `ServiceInstance`/`ServiceBinding` struct + 常量                                                                          |
| `internal/mwsupply/store.go`                                     | 新增 | 2 表 CRUD + `LookupBindExisting`                                                                                          |
| `internal/mwsupply/manifest.go`                                  | 新增 | 读 `.anp/deps.yaml`                                                                                                       |
| `internal/mwsupply/connstr.go`                                   | 新增 | env_key 映射 + 连接串构造                                                                                                 |
| `internal/mwsupply/supply.go`                                    | 新增 | `Reconciler`（bind_existing 供给）                                                                                        |
| `internal/mwsupply/*_test.go`                                    | 新增 | TDD 测试                                                                                                                  |
| `internal/appdeploy/adapt_prompt.go`                             | 改   | 要求回写 `.anp/deps.yaml`                                                                                                 |
| `internal/standard/model.go`                                     | 改   | AGENTS.md 适配规范加「依赖声明」条                                                                                        |
| `cmd/server/main.go`                                             | 改   | 装配 mwsupply Store + Reconciler                                                                                          |
| `platform/frontend/app/applications/page.tsx`                    | 改   | env 面板：source=platform 只读 + 标注                                                                                     |

---

## Task 1: 迁移 000028 + appdeploy_env.source + UpsertEnv source 参数（全调用点）

**Files:**

- Create: `platform/backend/internal/db/migrations/pg/000028_mwsupply.up.sql`
- Create: `platform/backend/internal/db/migrations/pg/000028_mwsupply.down.sql`
- Modify: `platform/backend/internal/appdeploy/model.go:80-87`（EnvVar）
- Modify: `platform/backend/internal/appdeploy/store.go:150-169`（envCols / ListEnv / UpsertEnv）
- Modify: `platform/backend/internal/pgsupply/provisioner.go:16-19, 87`（EnvWriter 接口 + 调用）
- Modify: `platform/backend/internal/pgsupply/provisioner_test.go:9-20`（fakeEnvWriter mock）
- Modify: `platform/backend/internal/appdeploy/handler.go:802`（HTTP UpsertEnv 传 source=user）
- Modify（机械加参）: 全部 `UpsertEnv(` 调用点（见 Step 4 清单）

**Interfaces:**

- Produces: `Store.UpsertEnv(ctx, appID, key, value string, isSecret bool, source string) error`；`EnvVar.Source string`；`pgsupply.EnvWriter.UpsertEnv(..., source string)`

- [ ] **Step 1: 前置检查迁移版本无冲突**

连 .28 dev PG（或本地 dev PG）：

```bash
psql "$DATABASE_URL" -c "SELECT version FROM schema_migrations WHERE version='000028';"
```

Expected: 0 行。若有记录（Analyzer 残留），本计划文件名/版本号改用 000029。

- [ ] **Step 2: 写迁移 up/down**

`000028_mwsupply.up.sql`：

```sql
-- 000028_mwsupply.up.sql
-- 中间件依赖供给与注入（P1：bind_existing 闭环）
-- 实例注册表 + 每应用绑定 + appdeploy_env.source（区分平台/用户注入）

CREATE TABLE appdeploy_service_instance (
    id               TEXT PRIMARY KEY,
    project_space_id TEXT REFERENCES project_space(id) ON DELETE CASCADE, -- NULL=平台全局
    kind             TEXT NOT NULL,                 -- redis / milvus / ...
    name             TEXT NOT NULL,
    supply_mode      TEXT NOT NULL,                 -- bind_existing / shared / dedicated
    host             TEXT NOT NULL,
    port             INT  NOT NULL,
    auth_ref         TEXT,                          -- 密码/token 引用（明文，同 pgsupply I1 债；阶段3 vault/KMS）
    isolation        JSONB,                         -- 隔离配置（shared 用：redis db_range / milvus prefix）
    status           TEXT NOT NULL DEFAULT 'active',
    created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_svinst_ps_kind ON appdeploy_service_instance(project_space_id, kind, supply_mode);
CREATE INDEX idx_svinst_status ON appdeploy_service_instance(status);

CREATE TABLE appdeploy_service_binding (
    id                  TEXT PRIMARY KEY,
    app_id              TEXT NOT NULL REFERENCES appdeploy_application(id) ON DELETE CASCADE,
    project_space_id    TEXT NOT NULL,
    service_kind        TEXT NOT NULL,              -- redis / milvus / ...
    strategy            TEXT NOT NULL,              -- bind_existing / shared / dedicated（解析后）
    service_instance_id TEXT REFERENCES appdeploy_service_instance(id),
    isolation_token     TEXT,                       -- 分配的隔离 token（redis db号 / milvus 前缀）
    env_key             TEXT NOT NULL,              -- REDIS_ADDR / MILVUS_ADDR / ...
    status              TEXT NOT NULL DEFAULT 'declared', -- declared / bound / failed
    last_error          TEXT,
    created_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (app_id, service_kind)
);
CREATE INDEX idx_svbind_app ON appdeploy_service_binding(app_id);
CREATE INDEX idx_svbind_ps  ON appdeploy_service_binding(project_space_id);

ALTER TABLE appdeploy_env ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'user';

-- 种子：.28 已有中间件（bind_existing 目标）
INSERT INTO appdeploy_service_instance (id, project_space_id, kind, name, supply_mode, host, port, isolation, status) VALUES
  ('svinst-redis-28',  NULL, 'redis',  'yxt-redis',  'bind_existing', '10.10.0.28', 6381,  '{"default_db":0}'::jsonb, 'active'),
  ('svinst-milvus-28', NULL, 'milvus', 'yxt-milvus', 'bind_existing', '10.10.0.28', 19530, NULL, 'active')
ON CONFLICT (id) DO NOTHING;
```

`000028_mwsupply.down.sql`：

```sql
DROP TABLE IF EXISTS appdeploy_service_binding;
DROP TABLE IF EXISTS appdeploy_service_instance;
ALTER TABLE appdeploy_env DROP COLUMN IF EXISTS source;
```

- [ ] **Step 3: 改 EnvVar struct 加 Source**

`internal/appdeploy/model.go`，把 EnvVar 改为：

```go
// EnvVar 应用运行时环境变量（部署时 docker run -e 注入；is_secret 时接口 mask 显示，不泄露）。
// Source: user(用户面板填) / platform(平台 pgsupply/mwsupply 注入，部署 reconcile 保障，前端只读)。
type EnvVar struct {
	ID        string    `json:"id" db:"id"`
	AppID     string    `json:"app_id" db:"app_id"`
	Key       string    `json:"key" db:"key"`
	Value     string    `json:"value" db:"value"`
	IsSecret  bool      `json:"is_secret" db:"is_secret"`
	Source    string    `json:"source" db:"source"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}
```

- [ ] **Step 4: 改 store.go envCols / ListEnv / UpsertEnv**

`internal/appdeploy/store.go`：

`envCols` 常量改为：

```go
const envCols = `id, app_id, key, COALESCE(value,'') AS value, is_secret, COALESCE(source,'user') AS source, created_at`
```

`UpsertEnv` 改为（加 source 参，ON CONFLICT 同步写 source）：

```go
// UpsertEnv 新增或更新环境变量（按 app_id+key 唯一）。
// source: "user"(用户面板) / "platform"(平台注入，部署 reconcile 保障)。
func (s *Store) UpsertEnv(ctx context.Context, appID, key, value string, isSecret bool, source string) error {
	if source == "" {
		source = "user"
	}
	id := "env_" + uuid.NewString()[:20]
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO appdeploy_env (id, app_id, key, value, is_secret, source) VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT(app_id, key) DO UPDATE SET value=excluded.value, is_secret=excluded.is_secret, source=excluded.source`,
		id, appID, key, value, isSecret, source)
	return err
}
```

`ListEnv` 不变（envCols 已带 source，sqlx 自动填 EnvVar.Source）。

- [ ] **Step 5: 改 pgsupply EnvWriter 接口 + Provision 调用 + mock**

`internal/pgsupply/provisioner.go` 接口改为：

```go
// EnvWriter 写应用环境变量（由 appdeploy.Store 实现，避免 pgsupply→appdeploy 循环依赖）。
type EnvWriter interface {
	UpsertEnv(ctx context.Context, appID, key, value string, isSecret bool, source string) error
}
```

`Provision` 内（原 line 87）改为：

```go
	if err := p.env.UpsertEnv(ctx, appID, "DATABASE_URL", dsn, true, "platform"); err != nil {
```

`internal/pgsupply/provisioner_test.go` 的 `fakeEnvWriter`：先读其 struct 定义（line 9 附近），把 `UpsertEnv` 方法签名加 `source string` 形参（记录里可加可不加，签名必须匹配）：

```go
func (f *fakeEnvWriter) UpsertEnv(_ context.Context, appID, key, value string, secret bool, _ string) error {
	f.calls = append(f.calls, envCall{appID: appID, Key: key, Value: value, Secret: secret})
	return nil
}
```

（按现有 envCall 字段调整；只要签名匹配接口即可）

- [ ] **Step 6: 改 HTTP UpsertEnv 传 source=user**

`internal/appdeploy/handler.go:802` 改为：

```go
	if err := h.store.UpsertEnv(c.Request.Context(), c.Param("aid"), in.Key, in.Value, in.IsSecret, "user"); err != nil {
```

（平台保护逻辑在 Task 2 加）

- [ ] **Step 7: 机械更新其余 UpsertEnv 调用点（加 `, "user"` 实参）**

逐一加第 6 个实参 `"user"`：

- `internal/appdeploy/handler_http_test.go:358` → `..., false, "user")`
- `internal/appdeploy/handler_http_test.go:359` → `..., true, "user")`
- `internal/appdeploy/handler_http_test.go:415` → `..., false, "user")`
- `internal/appdeploy/store_test.go:372` → `..., true, "user")`
- `internal/appdeploy/store_test.go:376` → `..., false, "user")`
- `internal/appdeploy/store_test.go:397,398,399` → `..., false, "user")`
- `internal/appdeploy/store_test.go:419,420` → `..., false, "user")`
- `internal/appdeploy/store_test.go:441,442` → `..., false, "user")`（442 是 true）

- [ ] **Step 8: 写测试——source 持久化 + 读回**

在 `internal/appdeploy/store_test.go` 末尾追加：

```go
// TestStore_UpsertEnv_source 验证 source 列持久化（platform/user 区分）。
func TestStore_UpsertEnv_source(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "envsrc")
	if err := s.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.UpsertEnv(ctx, a.ID, "REDIS_ADDR", "10.10.0.28:6381", false, "platform"); err != nil {
		t.Fatalf("upsert platform: %v", err)
	}
	if err := s.UpsertEnv(ctx, a.ID, "MY_KEY", "v", false, "user"); err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	vars, err := s.ListEnv(ctx, a.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := map[string]string{}
	for _, v := range vars {
		got[v.Key] = v.Source
	}
	if got["REDIS_ADDR"] != "platform" {
		t.Fatalf("REDIS_ADDR source 应 platform，得 %q", got["REDIS_ADDR"])
	}
	if got["MY_KEY"] != "user" {
		t.Fatalf("MY_KEY source 应 user，得 %q", got["MY_KEY"])
	}
}
```

- [ ] **Step 9: 跑测试 + 全量编译**

```bash
cd platform/backend
GOPATH=C:/Users/yxt/go go test ./internal/appdeploy/ ./internal/pgsupply/ -run 'UpsertEnv|Env' -v
```

Expected: PASS（含新 TestStore_UpsertEnv_source + 现有 UpsertEnv 测试）。

全量编译确认无漏改调用点：

```bash
GOPATH=C:/Users/yxt/go go build ./...
```

Expected: 无编译错（若有 `not enough arguments in call to UpsertEnv` → 按 Step 7 清单补）。

- [ ] **Step 10: Commit**

```bash
git add platform/backend/internal/db/migrations/pg/000028_mwsupply.up.sql platform/backend/internal/db/migrations/pg/000028_mwsupply.down.sql platform/backend/internal/appdeploy/model.go platform/backend/internal/appdeploy/store.go platform/backend/internal/appdeploy/store_test.go platform/backend/internal/appdeploy/handler.go platform/backend/internal/appdeploy/handler_http_test.go platform/backend/internal/pgsupply/provisioner.go platform/backend/internal/pgsupply/provisioner_test.go
git commit -m "feat(mwsupply): 迁移000028+appdeploy_env.source+UpsertEnv加source参(全调用点)"
```

---

## Task 2: HTTP env 面板平台保护（拒绝改 source=platform 的 key）

**Files:**

- Modify: `platform/backend/internal/appdeploy/store.go`（新增 `GetEnvSource`）
- Modify: `platform/backend/internal/appdeploy/handler.go:792-826`（UpsertEnv/DeleteEnv 加保护）
- Test: `platform/backend/internal/appdeploy/handler_http_test.go`

**Interfaces:**

- Consumes: `Store.UpsertEnv(...)`（Task 1）
- Produces: `Store.GetEnvSource(ctx, appID, key) (string, error)`；HTTP 409 保护

- [ ] **Step 1: 写失败测试——改平台 env 返回 409**

`handler_http_test.go` 追加（参照现有 TestHandler_UpsertEnv_ok 的 gin 测试搭建）：

```go
// TestHandler_UpsertEnv_platformProtected 平台注入的 key 用户改不了（409）。
func TestHandler_UpsertEnv_platformProtected(t *testing.T) {
	h := newTestHTTP(t) // 现有 helper：起 gin + 注入 handler（参照 TestHandler_UpsertEnv_ok 用的搭建）
	ctx := context.Background()
	a := mkApp("ps_1", "prot")
	_ = h.store.Create(ctx, a)
	_ = h.store.UpsertEnv(ctx, a.ID, "REDIS_ADDR", "10.10.0.28:6381", false, "platform")

	body := `{"key":"REDIS_ADDR","value":"hacked","is_secret":false}`
	w := postJSON(t, h, "/project-spaces/ps_1/apps/"+a.ID+"/env", body)
	if w.Code != 409 {
		t.Fatalf("改平台 env 应 409，得 %d body=%s", w.Code, w.Body.String())
	}
}
```

（`newTestHTTP`/`postJSON` 沿用该文件已有 helper；若命名不同，照搬 TestHandler_UpsertEnv_ok 的搭建代码。）

- [ ] **Step 2: 跑测试确认失败**

```bash
GOPATH=C:/Users/yxt/go go test ./internal/appdeploy/ -run TestHandler_UpsertEnv_platformProtected -v
```

Expected: FAIL（当前返回 200）。

- [ ] **Step 3: 加 GetEnvSource**

`store.go` 在 `GetEnvValue` 附近追加：

```go
// GetEnvSource 取应用某 env key 的 source（不存在返回 'user'，不报错）。
// 供 HTTP 面板判断是否平台托管（platform 不可手改/删）。
func (s *Store) GetEnvSource(ctx context.Context, appID, key string) (string, error) {
	var src string
	err := s.db.GetContext(ctx, &src,
		`SELECT COALESCE((SELECT source FROM appdeploy_env WHERE app_id=$1 AND key=$2),'user')`,
		appID, key)
	return src, err
}
```

- [ ] **Step 4: HTTP UpsertEnv/DeleteEnv 加保护**

`handler.go` UpsertEnv（在 `h.store.UpsertEnv` 之前）插入：

```go
	if src, _ := h.store.GetEnvSource(c.Request.Context(), c.Param("aid"), in.Key); src == "platform" {
		httpx.Err(c, 409, 40930, "平台托管的环境变量不可修改（由部署供给）")
		return
	}
```

DeleteEnv（在 `h.store.DeleteEnv` 之前）同理：

```go
	if src, _ := h.store.GetEnvSource(c.Request.Context(), c.Param("aid"), c.Param("key")); src == "platform" {
		httpx.Err(c, 409, 40930, "平台托管的环境变量不可删除（由部署供给）")
		return
	}
```

（错误码 40930 沿用 409xx 段；若该码已占用，查 httpx 取下一个空闲）

- [ ] **Step 5: 跑测试通过**

```bash
GOPATH=C:/Users/yxt/go go test ./internal/appdeploy/ -run 'UpsertEnv|DeleteEnv|Env' -v
```

Expected: PASS（含 platformProtected + 现有 ok 用例，user key 仍 200）。

- [ ] **Step 6: Commit**

```bash
git add platform/backend/internal/appdeploy/store.go platform/backend/internal/appdeploy/handler.go platform/backend/internal/appdeploy/handler_http_test.go
git commit -m "feat(appdeploy): env面板保护平台注入key(source=platform不可改删,409)"
```

---

## Task 3: mwsupply model + Store（2 表 CRUD + LookupBindExisting）

**Files:**

- Create: `platform/backend/internal/mwsupply/model.go`
- Create: `platform/backend/internal/mwsupply/store.go`
- Create: `platform/backend/internal/mwsupply/store_test.go`

**Interfaces:**

- Consumes: 迁移 000028 的 2 表
- Produces: `mwsupply.NewStore(db)`、`Store.LookupBindExisting(ctx, psID, kind) (*ServiceInstance, error)`、`Store.UpsertBinding(ctx, *ServiceBinding)`、`Store.ListBindingsByApp(ctx, appID) ([]ServiceBinding, error)`

- [ ] **Step 1: 写 model.go**

`internal/mwsupply/model.go`：

```go
// Package mwsupply 是「中间件依赖供给」限界上下文 ——
// 按应用声明的依赖（.anp/deps.yaml）供给中间件连接信息并注入 env。
// P1 仅 bind_existing（绑定已运行服务）；shared/dedicated 见 P2/P3。
package mwsupply

import "time"

// 供给策略。
const (
	ModeBindExisting = "bind_existing"
	ModeShared       = "shared"
	ModeDedicated    = "dedicated"
)

// 绑定状态。
const (
	StatusDeclared = "declared"
	StatusBound    = "bound"
	StatusFailed   = "failed"
)

// ServiceInstance 可绑定的中间件实例（注册表：bind_existing 目标 / shared 池 / dedicated 供给出来的）。
type ServiceInstance struct {
	ID             string  `json:"id" db:"id"`
	ProjectSpaceID *string `json:"project_space_id,omitempty" db:"project_space_id"` // NULL=平台全局
	Kind           string  `json:"kind" db:"kind"`                                   // redis/milvus/...
	Name           string  `json:"name" db:"name"`
	SupplyMode     string  `json:"supply_mode" db:"supply_mode"`
	Host           string  `json:"host" db:"host"`
	Port           int     `json:"port" db:"port"`
	AuthRef        string  `json:"auth_ref,omitempty" db:"auth_ref"`
	Isolation      string  `json:"isolation,omitempty" db:"isolation"` // raw jsonb text
	Status         string  `json:"status" db:"status"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}

// ServiceBinding 每应用对某中间件的绑定（声明 + 供给结果）。
type ServiceBinding struct {
	ID                string    `json:"id" db:"id"`
	AppID             string    `json:"app_id" db:"app_id"`
	ProjectSpaceID    string    `json:"project_space_id" db:"project_space_id"`
	ServiceKind       string    `json:"service_kind" db:"service_kind"`
	Strategy          string    `json:"strategy" db:"strategy"`
	ServiceInstanceID string    `json:"service_instance_id,omitempty" db:"service_instance_id"`
	IsolationToken    string    `json:"isolation_token,omitempty" db:"isolation_token"`
	EnvKey            string    `json:"env_key" db:"env_key"`
	Status            string    `json:"status" db:"status"`
	LastError         string    `json:"last_error,omitempty" db:"last_error"`
	CreatedAt         time.Time `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time `json:"updated_at" db:"updated_at"`
}
```

- [ ] **Step 2: 写失败测试——LookupBindExisting 命中 .28 种子**

`internal/mwsupply/store_test.go`：

```go
package mwsupply

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
	"zhiyuan-anp/platform/backend/internal/appdeploy"
	"zhiyuan-anp/platform/backend/internal/testutil"
)

func newTestStore(t *testing.T) (*Store, *sqlx.DB) {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "appdeploy_service_binding", "appdeploy_env", "appdeploy_application")
	// 重新插入 .28 种子（Truncate 不含 service_instance；迁移种子已在 TestDB 建好，保留）
	return NewStore(db), db
}

func mkAppRow(t *testing.T, db *sqlx.DB, id, ps string) {
	t.Helper()
	a := &appdeploy.Application{ProjectSpaceID: ps, Name: id, RepoDir: "/data/repos/" + id, InternalPort: 8080}
	if err := appdeploy.NewStore(db).Create(context.Background(), a); err != nil {
		t.Fatalf("create app: %v", err)
	}
}

// TestStore_LookupBindExisting_seed 迁移种子 svinst-redis-28 可被查到。
func TestStore_LookupBindExisting_seed(t *testing.T) {
	s, _ := newTestStore(t)
	got, err := s.LookupBindExisting(context.Background(), "ps_1", "redis")
	if err != nil || got == nil {
		t.Fatalf("应命中 .28 redis 种子，err=%v got=%+v", err, got)
	}
	if got.Host != "10.10.0.28" || got.Port != 6381 {
		t.Fatalf("redis 种子地址不对: %s:%d", got.Host, got.Port)
	}
}
```

- [ ] **Step 3: 跑测试确认失败**

```bash
GOPATH=C:/Users/yxt/go go test ./internal/mwsupply/ -run TestStore_LookupBindExisting_seed -v
```

Expected: FAIL（NewStore/LookupBindExisting 未定义，编译错）。

- [ ] **Step 4: 写 store.go**

`internal/mwsupply/store.go`：

```go
package mwsupply

import (
	"context"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

const instCols = `id, project_space_id, kind, name, supply_mode, host, port,
 COALESCE(auth_ref,'') AS auth_ref, COALESCE(isolation::text,'') AS isolation, status, created_at, updated_at`

const bindCols = `id, app_id, project_space_id, service_kind, strategy,
 COALESCE(service_instance_id,'') AS service_instance_id, COALESCE(isolation_token,'') AS isolation_token,
 env_key, status, COALESCE(last_error,'') AS last_error, created_at, updated_at`

// Store 中间件实例注册表 + 绑定记录数据访问。
type Store struct {
	db *sqlx.DB
}

func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

// LookupBindExisting 取某 kind 的 bind_existing 实例（项目级优先 → 平台级）。
// 无则返回 nil,nil。
func (s *Store) LookupBindExisting(ctx context.Context, psID, kind string) (*ServiceInstance, error) {
	var inst ServiceInstance
	err := s.db.GetContext(ctx, &inst,
		`SELECT `+instCols+` FROM appdeploy_service_instance
		 WHERE kind=$1 AND supply_mode='bind_existing' AND status='active'
		   AND (project_space_id=$2 OR project_space_id IS NULL)
		 ORDER BY (project_space_id IS NOT NULL) DESC
		 LIMIT 1`, kind, psID)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return &inst, nil
}

// UpsertBinding 新增或更新绑定（按 app_id+service_kind 唯一）。
func (s *Store) UpsertBinding(ctx context.Context, b *ServiceBinding) error {
	if b.ID == "" {
		b.ID = "svb_" + uuid.NewString()[:20]
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO appdeploy_service_binding (id, app_id, project_space_id, service_kind, strategy, service_instance_id, isolation_token, env_key, status, last_error)
		 VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''),$8,$9,NULLIF($10,''))
		 ON CONFLICT(app_id, service_kind) DO UPDATE SET strategy=excluded.strategy,
		   service_instance_id=excluded.service_instance_id, isolation_token=excluded.isolation_token,
		   env_key=excluded.env_key, status=excluded.status, last_error=excluded.last_error,
		   updated_at=CURRENT_TIMESTAMP`,
		b.ID, b.AppID, b.ProjectSpaceID, b.ServiceKind, b.Strategy, b.ServiceInstanceID, b.IsolationToken, b.EnvKey, b.Status, b.LastError)
	return err
}

// ListBindingsByApp 列应用全部绑定。
func (s *Store) ListBindingsByApp(ctx context.Context, appID string) ([]ServiceBinding, error) {
	var list []ServiceBinding
	err := s.db.SelectContext(ctx, &list,
		`SELECT `+bindCols+` FROM appdeploy_service_binding WHERE app_id=$1 ORDER BY service_kind`, appID)
	return list, err
}
```

`internal/mwsupply/norows.go`（小工具，避免每个文件 import errors/database-sql）：

```go
package mwsupply

import "database/sql"

func isNoRows(err error) bool { return err == sql.ErrNoRows }
```

- [ ] **Step 5: 跑测试通过**

```bash
GOPATH=C:/Users/yxt/go go test ./internal/mwsupply/ -run TestStore_LookupBindExisting_seed -v
```

Expected: PASS。

- [ ] **Step 6: 写 UpsertBinding 测试**

`store_test.go` 追加：

```go
// TestStore_UpsertBinding_upsert 绑定按 app+kind 幂等 upsert。
func TestStore_UpsertBinding_upsert(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	mkAppRow(t, db, "app_b1", "ps_1")
	// 用 appdeploy.Application 的 ID（Create 生成 app_ 前缀）；为简单，先查回
	var appID string
	_ = db.Get(&appID, `SELECT id FROM appdeploy_application WHERE name='app_b1'`)

	b := &ServiceBinding{AppID: appID, ProjectSpaceID: "ps_1", ServiceKind: "redis",
		Strategy: ModeBindExisting, ServiceInstanceID: "svinst-redis-28", EnvKey: "REDIS_ADDR", Status: StatusBound}
	if err := s.UpsertBinding(ctx, b); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	b.Status = StatusFailed
	b.LastError = "x"
	if err := s.UpsertBinding(ctx, b); err != nil { // 第二次走 ON CONFLICT
		t.Fatalf("upsert2: %v", err)
	}
	list, _ := s.ListBindingsByApp(ctx, appID)
	if len(list) != 1 || list[0].Status != StatusFailed {
		t.Fatalf("应 1 条 bound→failed，得 %+v", list)
	}
}
```

跑：`go test ./internal/mwsupply/ -run UpsertBinding -v` → PASS。

- [ ] **Step 7: Commit**

```bash
git add platform/backend/internal/mwsupply/
git commit -m "feat(mwsupply): model+store(实例注册表CRUD+LookupBindExisting+绑定upsert)"
```

---

## Task 4: mwsupply manifest 读取（.anp/deps.yaml）

**Files:**

- Create: `platform/backend/internal/mwsupply/manifest.go`
- Create: `platform/backend/internal/mwsupply/manifest_test.go`

**Interfaces:**

- Produces: `LoadDepsManifest(repoDir string) (*DepsManifest, error)`；`DepsManifest{Services []DepService}`；`DepService{Kind, Strategy string}`

- [ ] **Step 1: 写失败测试**

`internal/mwsupply/manifest_test.go`：

```go
package mwsupply

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDepsManifest_parses(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".anp"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".anp", "deps.yaml"),
		[]byte("services:\n  - kind: redis\n  - kind: milvus\n    strategy: bind_existing\n"), 0o644)

	m, err := LoadDepsManifest(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(m.Services) != 2 {
		t.Fatalf("应 2 个服务，得 %d", len(m.Services))
	}
	if m.Services[0].Kind != "redis" || m.Services[0].Strategy != "" {
		t.Fatalf("redis 解析错: %+v", m.Services[0])
	}
	if m.Services[1].Kind != "milvus" || m.Services[1].Strategy != "bind_existing" {
		t.Fatalf("milvus 解析错: %+v", m.Services[1])
	}
}

func TestLoadDepsManifest_missingIsEmpty(t *testing.T) {
	m, err := LoadDepsManifest(t.TempDir()) // 无 .anp/deps.yaml
	if err != nil {
		t.Fatalf("缺失清单应不报错: %v", err)
	}
	if m == nil || len(m.Services) != 0 {
		t.Fatalf("缺失应空清单，得 %+v", m)
	}
}
```

- [ ] **Step 2: 跑确认失败** → `go test ./internal/mwsupply/ -run LoadDepsManifest -v` → FAIL（未定义）。

- [ ] **Step 3: 写 manifest.go**

`internal/mwsupply/manifest.go`：

```go
package mwsupply

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DepsManifest 仓库根 .anp/deps.yaml 的依赖声明（opencode 适配回写）。
type DepsManifest struct {
	Services []DepService `yaml:"services"`
}

// DepService 单个中间件依赖声明。Strategy 可空（走默认 bind_existing）。
type DepService struct {
	Kind     string `yaml:"kind"`
	Strategy string `yaml:"strategy"`
}

// LoadDepsManifest 读 repoDir/.anp/deps.yaml。无文件=空清单（不报错，应用无额外中间件依赖）。
func LoadDepsManifest(repoDir string) (*DepsManifest, error) {
	data, err := os.ReadFile(filepath.Join(repoDir, ".anp", "deps.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return &DepsManifest{}, nil
		}
		return nil, err
	}
	var m DepsManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return &DepsManifest{}, nil // 解析失败按空清单（best-effort，不阻塞部署）
	}
	return &m, nil
}
```

- [ ] **Step 4: 跑通过** → `go test ./internal/mwsupply/ -run LoadDepsManifest -v` → PASS。

- [ ] **Step 5: Commit**

```bash
git add platform/backend/internal/mwsupply/manifest.go platform/backend/internal/mwsupply/manifest_test.go
git commit -m "feat(mwsupply): 读.anp/deps.yaml依赖清单(yaml.v3,缺失=空)"
```

---

## Task 5: mwsupply connstr + Reconciler（bind_existing 供给核心）

**Files:**

- Create: `platform/backend/internal/mwsupply/connstr.go`
- Create: `platform/backend/internal/mwsupply/supply.go`
- Create: `platform/backend/internal/mwsupply/supply_test.go`

**Interfaces:**

- Consumes: `Store.LookupBindExisting`/`UpsertBinding`（Task 3）、`LoadDepsManifest`（Task 4）、`EnvWriter`（新定义）
- Produces: `Reconciler`（方法 `Reconcile(ctx, appID, psID, repoDir) error`，满足 appdeploy.MWReconciler 接口）

- [ ] **Step 1: 写 connstr.go**

`internal/mwsupply/connstr.go`：

```go
package mwsupply

import "fmt"

// EnvKeyFor 把 service kind 映射到注入的 env key。
func EnvKeyFor(kind string) string {
	switch kind {
	case "redis":
		return "REDIS_ADDR"
	case "milvus":
		return "MILVUS_ADDR"
	default:
		return kind + "_ADDR"
	}
}

// ConnStr 构造连接地址串（P1 bind_existing：host:port；redis 无鉴权即裸地址）。
func ConnStr(inst *ServiceInstance) string {
	return fmt.Sprintf("%s:%d", inst.Host, inst.Port)
}
```

- [ ] **Step 2: 写失败测试——Reconcile 注入 REDIS_ADDR/MILVUS_ADDR**

`internal/mwsupply/supply_test.go`：

```go
package mwsupply

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
	"zhiyuan-anp/platform/backend/internal/appdeploy"
	"zhiyuan-anp/platform/backend/internal/testutil"
)

func newReconcilerTest(t *testing.T) (*Reconciler, *appdeploy.Store, *sqlx.DB, string) {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "appdeploy_service_binding", "appdeploy_env", "appdeploy_application")
	appStore := appdeploy.NewStore(db)
	// 重建 .28 种子（Truncate 不动 service_instance，种子由迁移保留；若被清则重建）
	ensureSeed(t, db)
	return NewReconciler(NewStore(db), appStore), appStore, db, ""
}

func ensureSeed(t *testing.T, db *sqlx.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO appdeploy_service_instance (id, project_space_id, kind, name, supply_mode, host, port, isolation, status) VALUES
	  ('svinst-redis-28',NULL,'redis','yxt-redis','bind_existing','10.10.0.28',6381,'{"default_db":0}'::jsonb,'active'),
	  ('svinst-milvus-28',NULL,'milvus','yxt-milvus','bind_existing','10.10.0.28',19530,NULL,'active')
	  ON CONFLICT (id) DO NOTHING`)
	if err != nil {
		t.Fatalf("ensureSeed: %v", err)
	}
}

// TestReconcile_bindExisting 写了 deps.yaml → 注入 REDIS_ADDR/MILVUS_ADDR + binding bound。
func TestReconcile_bindExisting(t *testing.T) {
	r, appStore, db, _ := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "rcapp", RepoDir: "/data/repos/rcapp", InternalPort: 8080}
	if err := appStore.Create(ctx, a); err != nil {
		t.Fatalf("create app: %v", err)
	}
	// 造 repo dir 含 .anp/deps.yaml
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".anp"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".anp", "deps.yaml"),
		[]byte("services:\n  - kind: redis\n  - kind: milvus\n"), 0o644)

	if err := r.Reconcile(ctx, a.ID, "ps_1", dir); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// 校验 env 写入 source=platform
	ra, _ := appStore.GetEnvValue(ctx, a.ID, "REDIS_ADDR")
	if ra != "10.10.0.28:6381" {
		t.Fatalf("REDIS_ADDR 应 10.10.0.28:6381，得 %q", ra)
	}
	ma, _ := appStore.GetEnvValue(ctx, a.ID, "MILVUS_ADDR")
	if ma != "10.10.0.28:19530" {
		t.Fatalf("MILVUS_ADDR 应 10.10.0.28:19530，得 %q", ma)
	}
	src, _ := appStore.GetEnvSource(ctx, a.ID, "REDIS_ADDR")
	if src != "platform" {
		t.Fatalf("REDIS_ADDR source 应 platform，得 %q", src)
	}
	// 校验 binding bound
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 2 {
		t.Fatalf("应 2 binding，得 %d", len(binds))
	}
}

// TestReconcile_idempotent 再跑一次不报错、env 仍正确。
func TestReconcile_idempotent(t *testing.T) {
	r, appStore, _, _ := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "rcapp2", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".anp"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".anp", "deps.yaml"), []byte("services:\n  - kind: redis\n"), 0o644)
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	if err := r.Reconcile(ctx, a.ID, "ps_1", dir); err != nil {
		t.Fatalf("二次 reconcile 不应报错: %v", err)
	}
}

// TestReconcile_missingInstanceKind 未注册的 kind → binding failed，不 panic。
func TestReconcile_missingInstanceKind(t *testing.T) {
	r, appStore, db, _ := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "rcapp3", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".anp"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".anp", "deps.yaml"), []byte("services:\n  - kind: mongodb\n"), 0o644)
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 1 || binds[0].Status != StatusFailed {
		t.Fatalf("未注册 kind 应 binding failed，得 %+v", binds)
	}
}
```

> 注：`appStore.GetEnvSource` 在 Task 2 加。若先做 Task 5 再 Task 2，把 GetEnvSource 断言移除或先做 Task 2。建议按本计划顺序（Task 2 在前）。

- [ ] **Step 3: 跑确认失败** → `go test ./internal/mwsupply/ -run Reconcile -v` → FAIL（Reconciler 未定义）。

- [ ] **Step 4: 写 supply.go**

`internal/mwsupply/supply.go`（**只 import `context`**；不 import appdeploy——EnvWriter 是本包自定义接口，appdeploy.Store 鸭子类型满足之，避免循环依赖）：

```go
package mwsupply

import "context"

// EnvWriter 写应用 env（由 appdeploy.Store 实现，避免 mwsupply→appdeploy 循环依赖）。
type EnvWriter interface {
	UpsertEnv(ctx context.Context, appID, key, value string, isSecret bool, source string) error
}

// Reconciler 中间件依赖供给（P1：bind_existing）。best-effort：失败记 binding，不阻塞部署。
type Reconciler struct {
	store *Store
	env   EnvWriter
}

func NewReconciler(store *Store, env EnvWriter) *Reconciler {
	return &Reconciler{store: store, env: env}
}

// Reconcile 读 repoDir 的 .anp/deps.yaml → 对每个声明服务按策略供给 → 写 env + binding。
// 幂等。读清单失败=空清单（不报错）。总不返回错（不阻塞部署）。
func (r *Reconciler) Reconcile(ctx context.Context, appID, psID, repoDir string) error {
	m, err := LoadDepsManifest(repoDir)
	if err != nil || m == nil {
		return nil
	}
	for _, dep := range m.Services {
		if dep.Kind == "" {
			continue
		}
		r.supplyOne(ctx, appID, psID, dep)
	}
	return nil
}

func (r *Reconciler) supplyOne(ctx context.Context, appID, psID string, dep DepService) {
	strategy := dep.Strategy
	if strategy == "" {
		strategy = ModeBindExisting
	}
	envKey := EnvKeyFor(dep.Kind)
	mkBind := func(status, instID, lastErr string) {
		_ = r.store.UpsertBinding(ctx, &ServiceBinding{
			AppID: appID, ProjectSpaceID: psID, ServiceKind: dep.Kind,
			Strategy: strategy, ServiceInstanceID: instID, EnvKey: envKey,
			Status: status, LastError: lastErr,
		})
	}
	// P1 仅 bind_existing；shared/dedicated 留 P2/P3
	if strategy != ModeBindExisting {
		mkBind(StatusFailed, "", "策略 "+strategy+" 暂未实现（P1 仅 bind_existing）")
		return
	}
	inst, err := r.store.LookupBindExisting(ctx, psID, dep.Kind)
	if err != nil || inst == nil {
		mkBind(StatusFailed, "", "无可绑定的 "+dep.Kind+" 实例")
		return
	}
	connStr := ConnStr(inst)
	isSecret := inst.AuthRef != ""
	if err := r.env.UpsertEnv(ctx, appID, envKey, connStr, isSecret, "platform"); err != nil {
		mkBind(StatusFailed, inst.ID, err.Error())
		return
	}
	mkBind(StatusBound, inst.ID, "")
}
```

- [ ] **Step 5: 跑测试通过**

```bash
GOPATH=C:/Users/yxt/go go test ./internal/mwsupply/ -v
```

Expected: 全 PASS（manifest + store + reconcile）。

- [ ] **Step 6: Commit**

```bash
git add platform/backend/internal/mwsupply/connstr.go platform/backend/internal/mwsupply/supply.go platform/backend/internal/mwsupply/supply_test.go
git commit -m "feat(mwsupply): Reconciler bind_existing供给(读清单→查实例→写env+binding,幂等)"
```

---

## Task 6: appdeploy buildAndDeploy 接入 Reconcile（MWReconciler 接口注入）

**Files:**

- Modify: `platform/backend/internal/appdeploy/handler.go:41-63`（Handler 加字段）、`72` 附近（加 SetMwReconciler）、`1607` 附近（buildAndDeploy 插 Reconcile）
- Test: `platform/backend/internal/appdeploy/handler_test.go`（若无则 handler_http_test.go）

**Interfaces:**

- Produces: `appdeploy.MWReconciler` 接口（`Reconcile(ctx, appID, psID, repoDir) error`）；`Handler.SetMwReconciler`；buildAndDeploy 调用点
- mwsupply.Reconciler 隐式满足 MWReconciler（鸭子类型，无需 import）

- [ ] **Step 1: 加 MWReconciler 接口 + 字段 + setter**

`handler.go`，在 `AdaptSubmitter` 接口定义后（line 69 后）追加：

```go
// MWReconciler 中间件依赖供给（部署前读 .anp/deps.yaml 注入 REDIS_ADDR 等）。
// 由 mwsupply.Reconciler 实现（经 main.go SetMwReconciler 注入，避免 appdeploy→mwsupply 依赖）。
type MWReconciler interface {
	Reconcile(ctx context.Context, appID, psID, repoDir string) error
}

// SetMwReconciler 注入中间件供给器（main.go 在 Register 后调）。
func (h *Handler) SetMwReconciler(r MWReconciler) { h.mwReconciler = r }
```

Handler struct（line 41-63）加字段（在 adaptSubmitter 旁）：

```go
	mwReconciler  MWReconciler            // 中间件依赖供给（部署前注入 REDIS_ADDR 等）；nil=不注入
```

- [ ] **Step 2: buildAndDeploy 插 Reconcile（EnvPairs 之前）**

`handler.go` 约 line 1607（`envPairs, _ := h.store.EnvPairs(...)` 之前）插入：

```go
	// 中间件依赖供给（P1 bind_existing）：读 repo 的 .anp/deps.yaml → 注入 REDIS_ADDR 等 env。
	// best-effort，失败不阻塞部署。用 buildDir（已 checkout 到目标版本，读该 commit 的清单）。
	if h.mwReconciler != nil {
		dir := buildDir
		if dir == "" {
			dir = a.RepoDir
		}
		_ = h.mwReconciler.Reconcile(ctx, a.ID, a.ProjectSpaceID, dir)
	}
	envPairs, _ := h.store.EnvPairs(ctx, a.ID) // 应用运行时环境变量（含密钥）注入容器
```

- [ ] **Step 3: 写测试——buildAndDeploy 调 Reconcile（用 fake reconciler 记录调用）**

在 `handler_http_test.go`（或新 `handler_test.go`）追加。用 fake MWReconciler 验证被调用 + repoDir 传对：

```go
type fakeMWReconciler struct {
	called bool
	dir    string
}

func (f *fakeMWReconciler) Reconcile(_ context.Context, _, _, repoDir string) error {
	f.called = true
	f.dir = repoDir
	return nil
}
```

（完整 buildAndDeploy 是异步 goroutine + docker，单测代价高。**最小验证**：直接断言 `h.mwReconciler` 字段被 SetMwReconciler 注入 + 接口可调；端到端在 Task 10 .28 验。若已有 buildAndDeploy 的测试基建，扩展之；否则本步只测 setter 注入：）

```go
func TestHandler_SetMwReconciler(t *testing.T) {
	h := newTestHTTP(t)
	f := &fakeMWReconciler{}
	h.SetMwReconciler(f)
	// 触发一次 reconcile 调用验证注入生效（不跑 docker）
	if err := f2 := h.mwReconciler; f2 == nil {
		t.Fatalf("SetMwReconciler 未注入")
	}
}
```

> 若 `h.mwReconciler` 私有不可从 test 包访问：把测试放 `package appdeploy`（同包，handler_http_test.go 已是同包）。`newTestHTTP` 沿用已有 helper。

- [ ] **Step 4: 跑编译 + 测试**

```bash
GOPATH=C:/Users/yxt/go go build ./...
GOPATH=C:/Users/yxt/go go test ./internal/appdeploy/ -run SetMwReconciler -v
```

Expected: 编译过 + 测试 PASS。

- [ ] **Step 5: Commit**

```bash
git add platform/backend/internal/appdeploy/handler.go platform/backend/internal/appdeploy/handler_http_test.go
git commit -m "feat(appdeploy): buildAndDeploy接入MWReconcile(EnvPairs前注入中间件env)"
```

---

## Task 7: main.go 装配 mwsupply

**Files:**

- Modify: `platform/backend/cmd/server/main.go:174-181`（pgsupply 旁）、`264-265`（注入）

**Interfaces:**

- Consumes: `mwsupply.NewStore`/`NewReconciler`（Task 3/5）、`appDeployStore`（满足 mwsupply.EnvWriter）

- [ ] **Step 1: 加 import**

`main.go` import 块加：

```go
	"zhiyuan-anp/platform/backend/internal/mwsupply"
```

- [ ] **Step 2: 构造 Reconciler（pgsupply 旁，约 line 181 后）**

```go
	// ---- 中间件依赖供给（mwsupply）：适配回写 .anp/deps.yaml → 部署注入 REDIS_ADDR 等 ----
	mwStore := mwsupply.NewStore(database)
	mwReconciler := mwsupply.NewReconciler(mwStore, appDeployStore) // appDeployStore 满足 mwsupply.EnvWriter
```

- [ ] **Step 3: 注入 Handler（约 line 265 后，SetAdaptSubmitter 旁）**

```go
	appDeployHandler.SetMwReconciler(mwReconciler) // 部署前注入中间件连接 env
```

- [ ] **Step 4: 编译**

```bash
cd platform/backend
GOPATH=C:/Users/yxt/go go build ./...
```

Expected: 编译过（appDeployStore.UpsertEnv 签名经 Task 1 已含 source，满足 mwsupply.EnvWriter）。

- [ ] **Step 5: Commit**

```bash
git add platform/backend/cmd/server/main.go
git commit -m "feat(main): 装配mwsupply Reconciler(注入appDeployHandler)"
```

---

## Task 8: AGENTS.md 适配规范 + AdaptPrompt 要求回写 .anp/deps.yaml

**Files:**

- Modify: `platform/backend/internal/standard/model.go:97-106`（BuildAgentsMarkdown 适配段）
- Modify: `platform/backend/internal/appdeploy/adapt_prompt.go`
- Modify: `platform/backend/internal/standard/store_test.go:151`（断言加 .anp/deps.yaml）

- [ ] **Step 1: 写失败测试——AGENTS.md 含依赖声明条**

`standard/store_test.go` 第 151 行的 want 列表加入 `".anp/deps.yaml"`：

```go
	for _, want := range []string{"ANP 部署适配规范", "env-over-config", "禁止硬编码", "DATABASE_URL", ".anp/deps.yaml"} {
```

跑：`go test ./internal/standard/ -run RefreshAgentsMD -v` → FAIL（当前 markdown 无 .anp/deps.yaml）。

- [ ] **Step 2: model.go 适配段加「依赖声明」条**

`standard/model.go` 在「依赖」那条（line 102）后追加一条：

````go
	b.WriteString("- **依赖声明（回写 `.anp/deps.yaml`）**：若应用用到 redis/milvus 等中间件，在仓库根写 `.anp/deps.yaml` 声明依赖，格式：\n  ```yaml\n  services:\n    - kind: redis       # redis/milvus/...\n    - kind: milvus\n      # strategy: bind_existing  # 可选，不写走默认 bind_existing\n  ```\n  ANP 据此注入连接 env（`REDIS_ADDR`/`MILVUS_ADDR`）。无中间件依赖则不写此文件。\n")
````

- [ ] **Step 3: AdaptPrompt 加回写要求**

`adapt_prompt.go` 的返回串中（"所需中间件不要写死地址..." 那句后）加：

```go
		"若用到 redis/milvus 等中间件，按 AGENTS.md 规范在仓库根写 `.anp/deps.yaml` 声明（services 列 kind），ANP 据此注入连接 env。" +
```

- [ ] **Step 4: 跑测试通过**

```bash
GOPATH=C:/Users/yxt/go go test ./internal/standard/ -run RefreshAgentsMD -v
```

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add platform/backend/internal/standard/model.go platform/backend/internal/standard/store_test.go platform/backend/internal/appdeploy/adapt_prompt.go
git commit -m "feat(standard): AGENTS.md适配规范+AdaptPrompt要求回写.anp/deps.yaml依赖声明"
```

---

## Task 9: 前端 env 面板——source=platform 只读 + 标注（UI polish）

> 后端 409（Task 2）已是硬保护；本任务是前端体验：平台托管行只读、加「平台托管」徽标。优先级低于后端，可放最后。

**Files:**

- Modify: `platform/frontend/app/applications/page.tsx:22`（EnvVar 类型）、`625-650`（fetch）、`1470-1501`（渲染）

- [ ] **Step 1: EnvVar 类型加 source**

`page.tsx:22`：

```tsx
type EnvVar = { id: string; key: string; value: string; is_secret: boolean; source?: string };
```

- [ ] **Step 2: 渲染——平台行加徽标 + 禁用编辑/删除**

在 env 列表渲染处（约 1470-1501），对 `e.source === "platform"` 的行：

- 显示「平台托管」徽标（如 `<span className="badge">平台托管</span>`）
- 隐藏/禁用「编辑」「删除」按钮（`disabled` 或不渲染）
- value 仍按 is_secret 隐藏

示例（按现有 JSX 结构调整）：

```tsx
{appEnvs.map((e) => (
  <div key={e.id} className="...">
    <span>{e.key}</span>
    {e.source === "platform" && <span className="text-info">平台托管</span>}
    <span className={e.is_secret ? "text-warn" : "text-text-muted"}>
      {e.is_secret ? "🔒 已隐藏" : e.value || "(空)"}
    </span>
    {e.source !== "platform" && (
      <>
        <button onClick={() => /* 现有编辑 */}>编辑</button>
        <button onClick={() => /* 现有删除 */}>删除</button>
      </>
    )}
  </div>
))}
```

- [ ] **Step 3: 本地起前端验证（或直接 .28 验）**

按 `stale-next-build-trap` 记忆：改完 `rm -rf .next` 重 build。验证：平台注入的 REDIS_ADDR/MILVUS_ADDR 行显示「平台托管」且无编辑/删除。

- [ ] **Step 4: Commit**

```bash
git add platform/frontend/app/applications/page.tsx
git commit -m "feat(frontend): env面板平台注入行只读+平台托管徽标"
```

---

## Task 10: .28 端到端验证

> 按 `deploy-28-no-local-test` / `deploy-prod-10.10.0.28` 记忆：本机不跑功能测试，commit→push→scp+.28 重建→触发验证。

- [ ] **Step 1: push**

```bash
git push origin main
```

- [ ] **Step 2: scp + .28 重建**

按 `deploy-prod-10.10.0.28` 记忆：scp 源码到 `/opt/anp`，docker-compose 重建 backend（迁移 000028 随启动自动跑）。

- [ ] **Step 3: 确认迁移已跑 + 种子在**

```bash
ssh 10.10.0.28 "docker exec <pg容器> psql -U <user> -d anp -c \"SELECT version FROM schema_migrations WHERE version='000028';\""
ssh 10.10.0.28 "docker exec <pg容器> psql -U <user> -d anp -c \"SELECT id,kind,host,port FROM appdeploy_service_instance;\""
```

Expected: 000028 在；redis/milvus 两条种子。

- [ ] **Step 4: 造一个依赖 redis/milvus 的测试应用并导入**

放一个最小 Go 应用到 `宿主/opt/anp/data/mwsupply-e2e`（=容器 `/data/mwsupply-e2e`），含 `config.yaml` 硬编码 `127.0.0.1`、无 Dockerfile。POST `/project-spaces/:id/import/apps {source:dir, server_path}` 触发导入 → opencode 适配。

- [ ] **Step 5: 验证适配回写 .anp/deps.yaml**

```bash
ssh 10.10.0.28 "cat /opt/anp/data/repos/<app>/.anp/deps.yaml"
```

Expected: 含 redis/milvus 声明（若 opencode 没回写，手动创建该文件验证后续链路：`echo 'services:\n  - kind: redis\n  - kind: milvus' > .../.anp/deps.yaml`）。

- [ ] **Step 6: 部署 + 验容器内 env**

触发部署后：

```bash
ssh 10.10.0.28 "docker exec <app容器> env | grep -E 'REDIS_ADDR|MILVUS_ADDR|DATABASE_URL'"
```

Expected: `REDIS_ADDR=10.10.0.28:6381`、`MILVUS_ADDR=10.10.0.28:19530`、`DATABASE_URL=...`（pgsupply）均在。

- [ ] **Step 7: 验证平台保护**

前端 env 面板看 REDIS_ADDR 行显「平台托管」只读；curl 改它应 409。

- [ ] **Step 8: 清理测试产物**

删测试 app + 容器 + repo，留干净环境。

- [ ] **Step 9: 更新记忆**

闭环后更新 `import-adapt-reuse-coding` 记忆：补「依赖注入 P1 已落地（REDIS_ADDR/MILVUS_ADDR bind_existing 闭环）」+ 关联本计划/spec。

---

## Self-Review 记录

- **Spec 覆盖**：§1 断点→Task 1/5/6；§2 决策（全量/适配回写/source 保护/双轨）→Task 1/2/8 + P2/P3 注明后续；§4 数据模型 2 表+source+种子→Task 1；§5 清单→Task 4/8；§6 reconcile+保护→Task 2/5/6；§7 模块→Task 3/4/5；§9 改动点逐条对应 Task 1-8；§13 验收 P1→Task 10。**spec §5.3「adapt 完成钩子落 binding」在本计划简化为 deploy 时读清单（Task 5/6）**——理由：避免 dev 包侵入 + 单一咽喉 + 适配回写仍驱动（manifest 在 repo）。已在此注明，属计划级实现细化。
- **占位符扫描**：无 TBD/TODO；Task 5 supply.go 的 import 已明确「只 import context」。
- **类型一致**：`UpsertEnv(..., source)` 全链一致（Store/EnvWriter×2/HTTP/测试）；`MWReconciler.Reconcile(ctx, appID, psID, repoDir)` 与 mwsupply.Reconciler.Reconcile 签名一致；EnvKeyFor/ConnStr 命名一致。
- **P2/P3 未覆盖（按设计分期，非本 plan）**：shared 隔离 token 分配/配额/回收；dedicated docker run 容器+端口+Cleanup。

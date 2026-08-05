# 依赖供给统一 P4：dedicated 实例数配额 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use `- [ ]`.

**Goal:** 给 quota.Service 加第 5 维度 `MaxDedicatedInstances`（默认 5/项目空间，redis/milvus/pg dedicated 合计），在 `mwsupply.supplyDedicated` 起容器前 hard-block，admin 可调 + 看板可见。

**Architecture:** quota 加维度 + `CheckDedicatedInstances`（count = active dedicated `service_instance` JOIN binding JOIN app WHERE app.ps）+ 迁移 000035 加列；mwsupply 经 `DedicatedQuotaChecker` 接口注入 `NewReconciler`，`supplyDedicated` reuse 后/起容器前调，超限 binding failed 不起容器；admin Set/handler 加这维。

**Tech Stack:** Go 1.24（quota + mwsupply + main）；PG（.28 anp_test）；TDD + 回归。

**Spec:** `docs/superpowers/specs/2026-08-05-依赖供给统一P4-dedicated配额-design.md`

## Global Constraints

- go 命令前缀 `GOPATH=C:/Users/yxt/go`；禁 SQLite；PG 测试连 .28 anp_test，用 `-p 1`。
- commit `feat(<pkg>): 中文` + body 每行 ≤100 字符 + `Co-Authored-By: Claude <noreply@anthropic.com>`。
- **redis/milvus shared/bind_existing 零行为变化**：每 task 末 `go test -p 1` 全绿 + `go build ./cmd/server` 通过。
- 自编译：每 task 末 build 通过。
- 迁移幂等（`ADD COLUMN IF NOT EXISTS`）。

## File Structure

| 文件                                                             | 责任                                                                                  | 动作        |
| ---------------------------------------------------------------- | ------------------------------------------------------------------------------------- | ----------- |
| `internal/quota/model.go`                                        | +`MaxDedicatedInstances` 字段/默认/维度常量；Usage +used                              | 改（T1）    |
| `internal/quota/errors.go`                                       | dimLabel +dedicated                                                                   | 改（T1）    |
| `internal/quota/store.go`                                        | quotaCols/GetOrCreate 加列（T1）；Set 加参数（T3）                                    | 改（T1/T3） |
| `internal/quota/service.go`                                      | +CheckDedicatedInstances/+count+Usage（T1）；Set 加参（T3）                           | 改（T1/T3） |
| `internal/db/migrations/pg/000035_quota_dedicated.{up,down}.sql` | 加列                                                                                  | 新增（T1）  |
| `internal/quota/handler.go`                                      | updateBody + UpdateQuota delta 加维                                                   | 改（T3）    |
| `internal/mwsupply/supply.go`                                    | +DedicatedQuotaChecker 接口；Reconciler 字段+NewReconciler 参；supplyDedicated 强制点 | 改（T2）    |
| `internal/mwsupply/supply_test.go`                               | newReconcilerTestWithPgDed 加 nil 参 + fake 强制测                                    | 改（T2）    |
| `cmd/server/main.go`                                             | NewReconciler 传 quotaSvc                                                             | 改（T2）    |

---

### Task 1: quota 第 5 维度（字段 + Check + count + 迁移 + Usage）

**Files:**

- Modify: `internal/quota/model.go`、`internal/quota/errors.go`、`internal/quota/store.go`、`internal/quota/service.go`
- Create: `internal/db/migrations/pg/000035_quota_dedicated.up.sql`、`000035_quota_dedicated.down.sql`
- Test: `internal/quota/service_test.go`、`internal/quota/store_test.go`

**Interfaces:**

- Produces: `quota.Service.CheckDedicatedInstances(ctx, psID string) error`（T2 mwsupply 调）；`DefaultMaxDedicatedInstances=5`；`Quota.MaxDedicatedInstances`；`DimensionDedicated="dedicated"`。
- 不改 `store.Set` / `Service.Set` 签名（留 T3）——T1 后 Set 仍写 4 列，第 5 列保持默认 5。

- [ ] **Step 1: 迁移文件** — 创建 `internal/db/migrations/pg/000035_quota_dedicated.up.sql`：

```sql
-- 000035_quota_dedicated.up.sql
ALTER TABLE project_quota ADD COLUMN IF NOT EXISTS max_dedicated_instances INT NOT NULL DEFAULT 5;
```

创建 `internal/db/migrations/pg/000035_quota_dedicated.down.sql`：

```sql
-- 000035_quota_dedicated.down.sql
ALTER TABLE project_quota DROP COLUMN IF EXISTS max_dedicated_instances;
```

- [ ] **Step 2: model.go** — const 块（`DefaultMaxApps` 旁）加：

```go
	DefaultMaxDedicatedInstances  = 5
```

`Quota` 结构加字段（`MaxCapabilityCallsPerDay` 下一行）：

```go
	MaxDedicatedInstances  int       `json:"max_dedicated_instances" db:"max_dedicated_instances"`
```

`Usage` 结构加字段（`UsedCapabilityToday` 下一行）：

```go
	UsedDedicatedInstances int   `json:"used_dedicated_instances"`
```

维度常量块（`DimensionDBSize` 旁）加：

```go
	DimensionDedicated            = "dedicated"
```

- [ ] **Step 3: errors.go** — `QuotaExceededError.Error()` 的 dimLabel map 加键：

```go
			DimensionDedicated:          "专属实例数",
```

- [ ] **Step 4: store.go** — `quotaCols` 改为（在 `max_capability_calls_per_day` 后插入 `max_dedicated_instances`）：

```go
const quotaCols = `project_space_id, max_apps, max_databases, max_total_db_mb, max_capability_calls_per_day, max_dedicated_instances, updated_at`
```

`GetOrCreate` 的 INSERT 改为（加列 + 默认值占位）：

```go
	err := s.db.QueryRowxContext(ctx,
		`INSERT INTO project_quota (project_space_id, max_apps, max_databases, max_total_db_mb, max_capability_calls_per_day, max_dedicated_instances)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (project_space_id) DO UPDATE SET project_space_id=EXCLUDED.project_space_id
		 RETURNING `+quotaCols,
		psID, DefaultMaxApps, DefaultMaxDatabases, DefaultMaxTotalDBMb, DefaultMaxCapabilityCallsPerDay, DefaultMaxDedicatedInstances).StructScan(&q)
```

（`Set` 本任务不动 —— 仍 4 列；T3 加第 5 列。）

- [ ] **Step 5: service.go** — 文件末尾（`countDatabases` 附近）加：

```go
// CheckDedicatedInstances 专属实例数 check（redis/milvus/pg dedicated 容器合计，per 项目空间）。
// 超限返回 *QuotaExceededError（mwsupply.supplyDedicated 起容器前调）。
func (s *Service) CheckDedicatedInstances(ctx context.Context, psID string) error {
	q, err := s.store.GetOrCreate(ctx, psID)
	if err != nil {
		return err
	}
	used, err := s.countDedicatedInstances(ctx, psID)
	if err != nil {
		return err
	}
	if used >= q.MaxDedicatedInstances {
		return &QuotaExceededError{Dimension: DimensionDedicated, Used: used, Limit: q.MaxDedicatedInstances, Unit: "个"}
	}
	return nil
}

// countDedicatedInstances 该 ps 下 active dedicated 实例数（distinct；
// dedicated 实例 project_space_id=NULL，经 binding→app 归属 ps）。
func (s *Service) countDedicatedInstances(ctx context.Context, psID string) (int, error) {
	var n int
	err := s.store.db.GetContext(ctx, &n,
		`SELECT COUNT(DISTINCT si.id)
		   FROM appdeploy_service_instance si
		   JOIN appdeploy_service_binding b ON b.service_instance_id = si.id
		   JOIN appdeploy_application a ON a.id = b.app_id
		  WHERE si.supply_mode='dedicated' AND si.status='active' AND a.project_space_id=$1`, psID)
	return n, err
}
```

`Usage` 方法在 `UsedCapabilityToday` 赋值后加：

```go
	if u.UsedDedicatedInstances, err = s.countDedicatedInstances(ctx, psID); err != nil {
		return nil, err
	}
```

- [ ] **Step 6: 写失败测试** — `service_test.go` 加（复用既有 `setupPS`/`insertApp` 套路 + 新 `insertDedicated` helper）：

```go
// insertDedicated 建 1 个 active dedicated service_instance + app + binding（binding→app→ps 归属 psID）。
// 自清理：binding.service_instance_id RESTRICT instance → 先删 binding 再删 instance。
func insertDedicated(t *testing.T, psID, kind string) {
	t.Helper()
	db := testutil.TestDB(t)
	insID := "svinst-ded-" + uuid.NewString()[:14]
	appID := "app_" + uuid.NewString()[:18]
	bndID := "bnd_" + uuid.NewString()[:14]
	must := func(q string, args ...any) {
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	must(`INSERT INTO appdeploy_service_instance (id, kind, name, supply_mode, host, port, status) VALUES ($1,$2,$3,'dedicated','h',0,'active')`, insID, kind, insID)
	must(`INSERT INTO appdeploy_application (id, project_space_id, name, internal_port, status) VALUES ($1,$2,$3,8080,'running')`, appID, psID, appID)
	must(`INSERT INTO appdeploy_service_binding (id, app_id, service_kind, strategy, service_instance_id, status) VALUES ($1,$2,$3,'dedicated',$4,'bound')`, bndID, appID, kind, insID)
	t.Cleanup(func() {
		db.Exec(`DELETE FROM appdeploy_service_binding WHERE id=$1`, bndID)
		db.Exec(`DELETE FROM appdeploy_service_instance WHERE id=$1`, insID)
		db.Exec(`DELETE FROM appdeploy_application WHERE id=$1`, appID)
	})
}

// TestService_CheckDedicatedInstances 边界：N<5 通过；N=5 超限（默认上限 5）。
// 跨 kind 合计；非 active 不算。qe.Limit==5 亦验证 GetOrCreate 默认值。
func TestService_CheckDedicatedInstances(t *testing.T) {
	psID, svc := setupPS(t)
	ctx := context.Background()
	for _, k := range []string{"redis", "milvus", "pg", "redis"} {
		insertDedicated(t, psID, k)
	}
	if err := svc.CheckDedicatedInstances(ctx, psID); err != nil {
		t.Fatalf("4 active dedicated 应通过（默认上限 5）: %v", err)
	}
	insertDedicated(t, psID, "milvus") // 第 5 个 → used=5 >= 5
	err := svc.CheckDedicatedInstances(ctx, psID)
	qe, ok := err.(*QuotaExceededError)
	if !ok {
		t.Fatalf("5 active dedicated 应 *QuotaExceededError，得 %v", err)
	}
	if qe.Dimension != DimensionDedicated || qe.Limit != 5 {
		t.Fatalf("维度/上限不符: %+v", qe)
	}
	// 非 active 不算：把一个转 draining → 计数回 4
	db := testutil.TestDB(t)
	if _, err := db.Exec(`UPDATE appdeploy_service_instance SET status='draining' WHERE id IN (SELECT b.service_instance_id FROM appdeploy_service_binding b JOIN appdeploy_application a ON a.id=b.app_id WHERE a.project_space_id=$1 LIMIT 1)`, psID); err != nil {
		t.Fatalf("转 draining: %v", err)
	}
	if n, _ := svc.countDedicatedInstances(ctx, psID); n != 4 {
		t.Fatalf("1 个转 draining 后应计 4，得 %d", n)
	}
}
```

（`setupPS`/`testutil`/`uuid` 均为本测试文件既有 import；`insertDedicated` 自清理 dedicated 三表，不依赖 project_space 级联。）

- [ ] **Step 7: 跑确认** → Run: `GOPATH=C:/Users/yxt/go go test -p 1 -run TestService_CheckDedicated ./internal/quota/...`
      Expected: PASS（含 count 跨 kind 合计 + 非 active 不算）。

- [ ] **Step 8: 回归 + build** → Run: `GOPATH=C:/Users/yxt/go go test -p 1 ./internal/quota/... ./internal/mwsupply/... ./internal/pgsupply/... ./internal/appdeploy/...` + `GOPATH=C:/Users/yxt/go go build ./cmd/server`
      Expected: 全绿（quota 加字段不破坏既有 4 维度；Set 仍 4 列兼容；GetOrCreate 默认 5 由 Step 6 的 qe.Limit==5 覆盖）。

- [ ] **Step 9: Commit** — `feat(quota): 加 MaxDedicatedInstances 维度(默认5)+CheckDedicatedInstances+迁移000035`

---

### Task 2: mwsupply supplyDedicated 起容器前 hard-block 配额

**Files:**

- Modify: `internal/mwsupply/supply.go`（接口 + Reconciler 字段 + NewReconciler 参 + supplyDedicated 强制点）
- Modify: `internal/mwsupply/supply_test.go`（`newReconcilerTestWithPgDed` 加 nil 参 + fake 强制测）
- Modify: `cmd/server/main.go`（NewReconciler 传 quotaSvc）

**Interfaces:**

- Consumes: `quota.Service.CheckDedicatedInstances`（T1 产出）。
- Produces: `mwsupply.DedicatedQuotaChecker` 接口；`NewReconciler` 末尾加 `dedQuota DedicatedQuotaChecker` 参。

> NewReconciler 签名变更波及：main.go（生产）+ `newReconcilerTestWithPgDed`（测试 helper，~40 用例经它）。helper 加 `nil` 第 9 参即恢复旧行为（不强制）。

- [ ] **Step 1: 写失败测试** — `supply_test.go`（或新建 `supply_ded_quota_test.go`）加：

```go
// fakeDedQuota 可控 CheckDedicatedInstances（返 err 模拟超限）。
type fakeDedQuota struct{ err error; calls int }
func (f *fakeDedQuota) CheckDedicatedInstances(context.Context, string) error { f.calls++; return f.err }

// TestSupplyDedicated_QuotaExceeded fake dedQuota 返超限 → supplyDedicated mkBind failed、
// 不调 LaunchDedicated（fake docker.runCalls 不增）/ spec.SupplyDedicated（pg ded fake provCalls 不增）。
func TestSupplyDedicated_QuotaExceeded(t *testing.T) {
	resetRegistry(t)
	k := "fakeweb"
	RegisterKind(KindSpec{Kind: k, AddrEnv: "F_URL",
		PortRange: func() (int, int) { return 9100, 9199 },
		ContainerName: func(short string) string { return "fake-" + short },
		LaunchDedicated: func(ctx context.Context, name string, port int) (string, error) { return "pw", nil },
	})
	r, _, _, _, dk := newReconcilerTest(t)
	r.dedQuota = &fakeDedQuota{err: errors.New("专属实例数已达上限: 5个 / 5个")} // 注入（同包字段）

	var gotStatus, gotErr string
	mkBind := func(status, _, _, lastErr string) { gotStatus, gotErr = status, lastErr }
	r.supplyDedicated(context.Background(), "app_q", "ps_q", DepService{Kind: k, Strategy: ModeDedicated}, kindRegistry[k], mkBind)

	if gotStatus != StatusFailed || !strings.Contains(gotErr, "专属实例数已达上限") {
		t.Fatalf("应 failed+lastErr 含超限，得 (%q,%q)", gotStatus, gotErr)
	}
	if len(dk.runCalls) != 0 { t.Fatalf("超限不应起容器，LaunchDedicated 被调 %d 次", len(dk.runCalls)) }
}

// TestSupplyDedicated_QuotaNil 不注入(nil) → 不拦（回归既有 dedicated 行为）。
func TestSupplyDedicated_QuotaNil(t *testing.T) {
	// newReconcilerTest 默认 dedQuota=nil；既有 dedicated 用例（如 TestSupplyDedicated_*）已覆盖起容器路径，
	// 此处仅断言 nil 时不 panic、能进到 launch。用 fake kind 触发默认 PortRange 路径。
	resetRegistry(t)
	k := "fakeweb"
	called := false
	RegisterKind(KindSpec{Kind: k, AddrEnv: "F_URL",
		PortRange: func() (int, int) { return 9100, 9199 },
		ContainerName: func(short string) string { return "fake-" + short },
		LaunchDedicated: func(ctx context.Context, name string, port int) (string, error) { called = true; return "pw", nil },
		ReadyDedicated: func(context.Context, string, string, int, string) error { return nil },
	})
	r, _, _, _, _ := newReconcilerTest(t)
	r.supplyDedicated(context.Background(), "app_q2", "ps_q", DepService{Kind: k, Strategy: ModeDedicated}, kindRegistry[k],
		func(status, _, _, _ string) {})
	if !called { t.Fatal("nil dedQuota 不应拦，应进入 LaunchDedicated") }
}
```

- [ ] **Step 2: 跑确认失败** → Run: `GOPATH=C:/Users/yxt/go go test -p 1 -run TestSupplyDedicated_Quota ./internal/mwsupply/...`
      Expected: 编译失败（`r.dedQuota` 字段未定义）。

- [ ] **Step 3: 加接口 + Reconciler 字段 + NewReconciler 参** — `supply.go` 在 `EnvWriter` 接口附近加：

```go
// DedicatedQuotaChecker 专属实例数配额检查（quota.Service 实现）。
// nil=不强制（开发/测试或未注入 quota）。
type DedicatedQuotaChecker interface {
	CheckDedicatedInstances(ctx context.Context, psID string) error
}
```

`Reconciler` 结构加字段（`pgProv`/host 旁）：

```go
	dedQuota DedicatedQuotaChecker // P4：dedicated 起容器前查配额；nil=不强制
```

`NewReconciler` 签名末尾加参 + 赋值（函数体 `r := &Reconciler{...}` 加 `dedQuota`，或赋值语句）：

```go
func NewReconciler(store *Store, env EnvWriter, flusher DBFlusher, ready ReadyChecker, docker MWDockerRunner, host string, pgProv *pgsupply.Provisioner, pgDed PgDedicatedRunner, dedQuota DedicatedQuotaChecker) *Reconciler {
	r := &Reconciler{store: store, env: env, flusher: flusher, ready: ready, docker: docker, host: host, dedQuota: dedQuota}
	BuildSpecs(store, env, flusher, ready, docker, pgProv, pgDed)
	return r
}
```

- [ ] **Step 4: supplyDedicated 强制点** — `supply.go` `supplyDedicated` 里，紧跟现有 reuse 复用判定块（`if b, e := r.store.GetBinding...` 已 bound 同实例→return）之后、`if spec.SupplyDedicated != nil {` 之前插入：

```go
	// P4 配额：起容器前查 dedicated 实例数（reuse 已 bound 不耗新配额，故在 reuse 之后）。
	if r.dedQuota != nil {
		if err := r.dedQuota.CheckDedicatedInstances(ctx, psID); err != nil {
			mkBind(StatusFailed, "", "", "专属中间件实例数已达上限: "+err.Error())
			return
		}
	}
```

- [ ] **Step 5: 测试 helper + main.go** — `supply_test.go` `newReconcilerTestWithPgDed` 末尾 NewReconciler 调用加 `nil` 第 9 参：

```go
	return NewReconciler(NewStore(db), appStore, f, f, dk, "testdeploy", nil, pgDed, nil), appStore, db, f, dk
```

`cmd/server/main.go`（约 :186）NewReconciler 调用加 `quotaSvc`：

```go
	mwReconciler := mwsupply.NewReconciler(mwStore, appDeployStore, mwProbe, mwProbe, mwsupply.NewOSDocker(), cfg.AppDeployHost, pgProvisioner, instanceMgr, quotaSvc)
```

（`quotaSvc` 在 :181 已构造，满足 `DedicatedQuotaChecker`——T1 加了 `CheckDedicatedInstances` 方法。）

- [ ] **Step 6: 跑确认通过 + 回归** → Run: `GOPATH=C:/Users/yxt/go go test -p 1 ./internal/mwsupply/... ./internal/quota/... ./internal/pgsupply/... ./internal/appdeploy/...` + `GOPATH=C:/Users/yxt/go go build ./cmd/server`
      Expected: 全绿（QuotaExceeded→failed 不起容器；nil→不拦；reuse→不调 check；既有 dedicated 用例 helper 加 nil 后回归绿）。

- [ ] **Step 7: Commit** — `feat(mwsupply): supplyDedicated 起容器前 hard-block dedicated 实例数配额`

---

### Task 3: quota Set/handler 支持 admin 调 max_dedicated_instances

**Files:**

- Modify: `internal/quota/store.go`（`Set` 加参数）、`internal/quota/service.go`（`Set` 加参数）
- Modify: `internal/quota/handler.go`（`updateBody` + `UpdateQuota` delta）

**Interfaces:**

- Changes: `store.Set(ctx, psID, maxApps, maxDatabases, maxTotalDBMb, maxCapabilityCallsPerDay, maxDedicatedInstances int)`；`Service.Set` 同加末参。调用方：`handler.UpdateQuota`。

- [ ] **Step 1: 写失败测试** — `service_test.go` 加（仿既有 Set 测试）：

```go
// TestService_Set_Dedicated Set 能改 max_dedicated_instances，Usage 读回。
func TestService_Set_Dedicated(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	psID := "ps_set_ded"
	defer s.db.MustExec(`DELETE FROM project_quota WHERE project_space_id=$1`, psID)
	if _, err := s.GetOrCreate(ctx, psID); err != nil { t.Fatalf("GetOrCreate: %v", err) }
	if _, err := s.Set(ctx, psID, 20, 20, 10240, 10000, 8); err != nil { t.Fatalf("Set: %v", err) }
	q, _ := s.store.Get(ctx, psID)
	if q.MaxDedicatedInstances != 8 { t.Fatalf("Set 后应 8，得 %d", q.MaxDedicatedInstances) }
}
```

- [ ] **Step 2: 跑确认失败** → Run: `GOPATH=C:/Users/yxt/go go test -p 1 -run TestService_Set_Dedicated ./internal/quota/...`
      Expected: 编译失败（`s.Set` 参数数不符）。

- [ ] **Step 3: store.Set 加参** — `store.go` `Set` 改为：

```go
func (s *Store) Set(ctx context.Context, psID string, maxApps, maxDatabases, maxTotalDBMb, maxCapabilityCallsPerDay, maxDedicatedInstances int) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE project_quota
		 SET max_apps=$1, max_databases=$2, max_total_db_mb=$3, max_capability_calls_per_day=$4, max_dedicated_instances=$5, updated_at=CURRENT_TIMESTAMP
		 WHERE project_space_id=$6`,
		maxApps, maxDatabases, maxTotalDBMb, maxCapabilityCallsPerDay, maxDedicatedInstances, psID)
	if err != nil { return err }
	if n, _ := res.RowsAffected(); n == 0 { return ErrNotExists }
	return nil
}
```

- [ ] **Step 4: service.Set 加参** — `service.go` `Set` 改为：

```go
func (s *Service) Set(ctx context.Context, psID string, maxApps, maxDatabases, maxTotalDBMb, maxCapabilityCallsPerDay, maxDedicatedInstances int) (*Quota, error) {
	if _, err := s.store.GetOrCreate(ctx, psID); err != nil { return nil, err }
	if err := s.store.Set(ctx, psID, maxApps, maxDatabases, maxTotalDBMb, maxCapabilityCallsPerDay, maxDedicatedInstances); err != nil { return nil, err }
	return s.store.Get(ctx, psID)
}
```

- [ ] **Step 5: handler.go** — `updateBody` 加字段（`MaxCapabilityCallsPerDay` 下一行）：

```go
	MaxDedicatedInstances *int `json:"max_dedicated_instances" validate:"omitempty,min=0,max=1000"`
```

`UpdateQuota` 在 `maxCap` delta 块后加：

```go
	maxDed := cur.MaxDedicatedInstances
	if in.MaxDedicatedInstances != nil {
		maxDed = *in.MaxDedicatedInstances
	}
```

`Set` 调用改为：

```go
	q, err := h.svc.Set(c.Request.Context(), psID, maxApps, maxDatabases, maxTotalDBMb, maxCap, maxDed)
```

- [ ] **Step 6: 跑确认通过 + 回归** → Run: `GOPATH=C:/Users/yxt/go go test -p 1 ./internal/quota/... ./internal/mwsupply/... ./internal/appdeploy/...` + `GOPATH=C:/Users/yxt/go go build ./cmd/server`
      Expected: 全绿（Set 写第 5 列；handler UpdateQuota 透传；Usage 已含 used_dedicated_instances——T1）。

- [ ] **Step 7: Commit** — `feat(quota): Set/handler 支持 admin 调 max_dedicated_instances`

---

### Task 4: .28 e2e（迁移 + 5 过/6 拦/admin 调高）

**Files:** 无代码改动（验收 runbook）。deploy 流程见 `deploy/README.md`；ssh `-i ~/.ssh/miscode -o PubkeyAcceptedAlgorithms=+ssh-rsa`，只动 `deploy_` 容器。

- [ ] **Step 1: 本地全量绿** → `GOPATH=C:/Users/yxt/go go test -p 1 ./internal/...` + `go build ./cmd/server` → 全绿。

- [ ] **Step 2: 推送** → `git push origin feat/unified-dep-supply`（续攒 PR #3）。

- [ ] **Step 3: scp + 重建 .28 backend** → 改动文件（quota/{model,errors,store,service,handler}.go、mwsupply/supply.go、mwsupply/supply_test.go、cmd/server/main.go、迁移 000035）scp 到 `/opt/anp`；`docker-compose -f deploy/docker-compose.prod.yml up --build -d backend`（重建时自动跑迁移 000035 加列）。

- [ ] **Step 4: e2e dedicated 配额** — 建一项目空间（或复用），循环声明 5 个不同 kind/pg dedicated 应用并部署 → 前 5 个 binding bound、各起一 dedicated 容器；第 6 个 → binding **failed**（last_error 含「专属中间件实例数已达上限」）且**不起容器**（docker ps 无新容器）。

- [ ] **Step 5: e2e admin 调高** → `PUT /project-spaces/:id/quota {max_dedicated_instances: 10}` → 第 6 个 dedicated 重新部署 → 通过、起容器。

- [ ] **Step 6: e2e Usage 可见** → `GET /project-spaces/:id/quota` → 响应含 `used_dedicated_instances` 与 `max_dedicated_instances`。

- [ ] **Step 7: 清理** → 删 e2e dedicated 应用（触发 CleanupDedicated docker rm）+ 复位 quota。

- [ ] **Step 8: 记录** → e2e 结果写进 `.superpowers/sdd/progress.md` 的 P4 段。

---

## Self-Review

**1. Spec 覆盖：** §4.1 model/errors/store（T1）→T1；§4.2 迁移 000035→T1；§4.3 Check/count/Usage→T1；§4.4 handler→T3；§4.5 mwsupply 接口+强制点+main→T2；§4.6 行为→T2/T4；§5 测试→各 task。Set 配置（§4.4）→T3。✓
**2. 占位：** 每步完整代码/命令/SQL；T4 为 runbook（验收）。✓
**3. 类型一致：** `CheckDedicatedInstances(ctx,psID)error` T1 定义、T2 调用一致；`NewReconciler(...,dedQuota DedicatedQuotaChecker)` T2 定义，main.go + helper 两处调用加参一致；`store.Set`/`Service.Set` T3 加 `maxDedicatedInstances int` 末参，handler 调用一致；`DimensionDedicated`/`MaxDedicatedInstances`/`UsedDedicatedInstances`/`DefaultMaxDedicatedInstances` 名字全 task 一致。✓
**4. 自编译：** T1（quota 内部，Set 不动 4 列兼容）独立；T2（NewReconciler 签名变更波及 main+helper，同 task 改全）依赖 T1 的 CheckDedicatedInstances；T3（Set 签名变更波及 handler，同 task 改全）独立；T4 验收。✓
**5. 风险：** (a) NewReconciler 加参须同 task 改 main.go + newReconcilerTestWithPgDed（否则编译断）；(b) Set 加参须同 task 改 handler（否则编译断）；(c) count 查询依赖 binding.service_instance_id JOIN app（P2b 既定关系），实例 ps=NULL 经 app 归属；(d) T1 不动 Set，第 5 列保持默认 5，T3 才让 admin 改——T1 后 T2 即可强制（默认 5）。

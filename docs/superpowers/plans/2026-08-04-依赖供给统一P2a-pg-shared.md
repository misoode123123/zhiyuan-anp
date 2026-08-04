# 依赖供给统一 P2a：pg shared 接入 registry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 pg 作为第一个新 kind 接入 P1 的 KindSpec registry，实现 pg **shared** 策略（复用 pgsupply.Provision 全套：项目空间 PG 实例 + 建库/role + 写 DATABASE_URL + 写 appdeploy_database），让 pg 变成声明驱动。验证 registry 扩展性（加 kind 零改供给主逻辑）。bind_existing/dedicated 留 P2b。

**Architecture:** KindSpec 加可选 `SupplyShared` 字段——pg 设它（自管全套供给 + 写 env + 写明细），redis/milvus 不设（走原 LookupShared+AllocSharedToken+writeSpecEnv 路径，P1 不变）。`supplyShared` 顶部分支：`spec.SupplyShared != nil` → pg 路径（含 reuse：binding 已 bound 则跳过）；否则原路径。pgsupply.Provision 加幂等（已 ready 则复用），使 auto-provision（P3 才移除）与 declared 共存不冲突。

**Tech Stack:** Go 1.24（`internal/mwsupply` + `internal/pgsupply`）；PG（anp_test，禁 SQLite）；TDD + 既有测试回归。

**Spec:** `docs/superpowers/specs/2026-08-04-依赖供给统一三策略-design.md`（§5 pg 收编）

## Global Constraints

- 禁 SQLite，只用 PG；纯函数单测本机跑，PG 测试连 .28 anp_test（`GOPATH=C:/Users/yxt/go`）。
- go 命令前缀 `GOPATH=C:/Users/yxt/go`；全量回归 `go test -p 1 ./internal/mwsupply/... ./internal/pgsupply/...`。
- commit `feat(mwsupply): 中文` + 末尾 `Co-Authored-By: Claude <noreply@anthropic.com>`。
- **零行为变化（redis/milvus）**：P1 的供给路径不动，既有 mwsupply/pgsupply 测试全绿。
- **自编译**：每 task 末 `go build ./cmd/server` 通过。
- pg 的 binding 不存 `service_instance_id`（该列 FK→service_instance(id)，而 pg 实例在 pg_instance 表）——pg binding 留空，实例/库明细在 appdeploy_database。

## File Structure

| 文件                                           | 责任                                                          | 动作           |
| ---------------------------------------------- | ------------------------------------------------------------- | -------------- |
| `internal/mwsupply/kindspec.go`                | 加 `SupplyShared` 可选字段                                    | 改（Task 1）   |
| `internal/mwsupply/supply.go`                  | `supplyShared` 加 pg 分支（`spec.SupplyShared != nil`）       | 改（Task 1）   |
| `internal/mwsupply/supply_shared_test.go`      | pg 分支测（fake SupplyShared）                                | 新建（Task 1） |
| `internal/pgsupply/provisioner.go`             | `Provision` 加幂等（GetAppDBByApp 已 ready 则复用）           | 改（Task 2）   |
| `internal/pgsupply/provisioner_test.go`        | 幂等复用测                                                    | 改（Task 2）   |
| `internal/mwsupply/spec_pg.go`                 | pg KindSpec（SupplyShared 调 pgsupply.Provision）             | 新建（Task 3） |
| `internal/mwsupply/spec_pg_test.go`            | pg spec 字段测                                                | 新建（Task 3） |
| `internal/mwsupply/specs.go`                   | `BuildSpecs` 加 provisioner 参数，注册 pgSpec                 | 改（Task 3）   |
| `internal/mwsupply/supply.go`（NewReconciler） | 加 provisioner 参数透传 BuildSpecs；DepsCatalog kinds 加 "pg" | 改（Task 3）   |
| `cmd/server/main.go`                           | NewReconciler 调用点传 pgProvisioner                          | 改（Task 3）   |

---

### Task 1: KindSpec.SupplyShared 字段 + supplyShared pg 分支

**Files:**

- Modify: `internal/mwsupply/kindspec.go`（加字段）
- Modify: `internal/mwsupply/supply.go`（supplyShared 加分支）
- Test: `internal/mwsupply/supply_shared_test.go`（新建）

**Interfaces:**

- Produces: `KindSpec.SupplyShared func(ctx context.Context, appID, psID string) (instanceID, token string, err error)`（可选，nil=走原 redis/milvus 路径）。`supplyShared` 顶部分支用之。

- [ ] **Step 1: 写失败测试** — `supply_shared_test.go`

```go
package mwsupply

import (
	"context"
	"errors"
	"testing"
)

// TestSupplyShared_SelfSupplyBranch 验证 spec.SupplyShared != nil 时走自管路径：
// SupplyShared 返回 (instID, token) → binding bound；不调 LookupShared/AllocSharedToken。
func TestSupplyShared_SelfSupplyBranch(t *testing.T) {
	resetRegistry(t)
	k := "fakepg"
	called := false
	RegisterKind(KindSpec{
		Kind: k, AddrEnv: "FAKEPG_URL",
		SupplyShared: func(ctx context.Context, appID, psID string) (string, string, error) {
			called = true
			return "", "db-token-1", nil
		},
	})
	// 用真 Reconciler（store 连 anp_test）+ 一个 declared binding 触发 supplyShared。
	// 最小验证：supplyShared 调 spec.SupplyShared 且 mkBind bound。
	r := newTestReconciler(t) // 复用既有测试 helper（连 anp_test + truncate service_binding）
	mk := captureBind(t)
	r.supplyShared(context.Background(), "app_fake", "ps_default", DepService{Kind: k, Strategy: ModeShared},
		kindRegistry[k], mk.fn)
	if !called {
		t.Fatal("spec.SupplyShared 未被调用")
	}
	mk.want(t, StatusBound, "", "db-token-1")
}

// TestSupplyShared_SelfSupplyReuse 验证 binding 已 bound → 不再调 SupplyShared（防重复建库）。
func TestSupplyShared_SelfSupplyReuse(t *testing.T) {
	resetRegistry(t)
	k := "fakepg"
	calls := 0
	RegisterKind(KindSpec{
		Kind: k, AddrEnv: "FAKEPG_URL",
		SupplyShared: func(ctx context.Context, appID, psID string) (string, string, error) {
			calls++
			if calls > 1 {
				return "", "", errors.New("不应被二次调用")
			}
			return "", "db-token-1", nil
		},
	})
	r := newTestReconciler(t)
	// 第一次供给 → bound
	r.supplyShared(context.Background(), "app_fake2", "ps_default", DepService{Kind: k, Strategy: ModeShared},
		kindRegistry[k], func(string, string, string, string) {})
	// 第二次 → 应复用，不调 SupplyShared
	r.supplyShared(context.Background(), "app_fake2", "ps_default", DepService{Kind: k, Strategy: ModeShared},
		kindRegistry[k], func(string, string, string, string) {})
	if calls != 1 {
		t.Fatalf("SupplyShared 应只调 1 次（reuse），实际 %d", calls)
	}
}
```

> `newTestReconciler` / `captureBind` 若 supply_test.go 已有类似 helper 则复用；否则在本测试文件内写最小版（连 anp_test + truncate service_binding 的 store + 真 Reconciler）。实现者读 supply_test.go 既有 helper 决定。

- [ ] **Step 2: 跑确认失败** — `cd platform/backend && GOPATH=C:/Users/yxt/go go test ./internal/mwsupply/ -run 'TestSupplyShared_SelfSupply' -v` → FAIL（`SupplyShared` 字段未定义）。

- [ ] **Step 3: 加字段** — `kindspec.go` 的 `KindSpec` struct，在 `AllocSharedToken` 字段后加：

```go
	// SupplyShared（可选）：自管全套 shared 供给的 kind（如 pg：实例+库+role+写 env+写明细记录）。
	// 非 nil 时 supplyShared 走此分支（含 reuse 判定），跳过默认 LookupShared+AllocSharedToken+writeSpecEnv 路径。
	// 返回 (instanceID, token)；env 由实现内部写。instanceID 可空（如 pg 实例在它表，binding 不存）。
	SupplyShared func(ctx context.Context, appID, psID string) (instanceID, token string, err error)
```

- [ ] **Step 4: supplyShared 加分支** — `supply.go` 的 `supplyShared` 函数体最前面（在现有 `inst, err := r.store.LookupShared(...)` 之前）插入：

```go
	// pg-style 自管供给（spec.SupplyShared 自管全套：实例+库+role+env+明细；不走 LookupShared/ConnStr）。
	if spec.SupplyShared != nil {
		// reuse：binding 已 bound → 不重复供给（pgsupply.Provision 虽幂等，仍避免无谓调用）。
		if existing, e := r.store.GetBinding(ctx, appID, dep.Kind); e == nil && existing != nil &&
			existing.Status == StatusBound && existing.ServiceInstanceID != "" {
			mkBind(StatusBound, existing.ServiceInstanceID, existing.IsolationToken, "")
			return
		}
		// ⚠️ pg reuse 修正：pg 的 binding 不存 service_instance_id（FK 约束），改用 token 判 reuse。
		if existing, e := r.store.GetBinding(ctx, appID, dep.Kind); e == nil && existing != nil &&
			existing.Status == StatusBound && existing.IsolationToken != "" {
			mkBind(StatusBound, existing.ServiceInstanceID, existing.IsolationToken, "")
			return
		}
		instID, token, err := spec.SupplyShared(ctx, appID, psID)
		if err != nil {
			mkBind(StatusFailed, "", "", err.Error())
			return
		}
		mkBind(StatusBound, instID, token, "")
		return
	}
```

> 注：上面两个 reuse 判定合并为一个（用 `IsolationToken != ""` 判已供给，兼容 pg 的空 instID 与 redis/milvus 的非空 instID）。实现时合并成单个 `if`（去掉重复）：

```go
	if existing, e := r.store.GetBinding(ctx, appID, dep.Kind); e == nil && existing != nil &&
		existing.Status == StatusBound && existing.IsolationToken != "" {
		mkBind(StatusBound, existing.ServiceInstanceID, existing.IsolationToken, "")
		return
	}
```

- [ ] **Step 5: 跑确认通过** — `go test ./internal/mwsupply/ -run 'TestSupplyShared_SelfSupply' -v` → PASS。再跑 `go test -p 1 ./internal/mwsupply/...`（redis/milvus 既有测零回归）+ `go build ./cmd/server` → 全绿。

- [ ] **Step 6: Commit** — `feat(mwsupply): KindSpec 加 SupplyShared 字段+supplyShared 自管分支`

---

### Task 2: pgsupply.Provision 加幂等（已 ready 则复用）

**Files:**

- Modify: `internal/pgsupply/provisioner.go`（`Provision` 开头加 reuse）
- Test: `internal/pgsupply/provisioner_test.go`

**Interfaces:**

- Produces: `Provision(ctx, psID, appID)` 在开头 `GetAppDBByApp(appID)`：若已存在且 `status in (ready)` → 直接返回该 `*AppDatabase`（不重建库/role/不重写 env）。使并发/重复调用（auto-provision + declared 共存期）安全。

- [ ] **Step 1: 写失败测试** — `provisioner_test.go` 追加。用既有 fake admin/instance（读 provisioner_test.go 现有构造法），调 Provision 两次，断言第二次未再 CreateDatabase/Role（复用）：

```go
func TestProvision_IdempotentReuse(t *testing.T) {
	// 复用 provisioner_test.go 既有 fake admin/store/instance 构造（读该文件照抄）。
	p, fakeAdm, store := newFakeProvisioner(t) // 既有 helper；若无则照现有 TestProvision_* 写最小版
	psID, appID := "ps_default", "app_idem"

	first, err := p.Provision(context.Background(), psID, appID)
	if err != nil {
		t.Fatalf("首次 Provision: %v", err)
	}
	createsBefore := fakeAdm.createDBCalls() // fake admin 计数（读现有 fake 加计数器，或断言 createDBCalls）

	_, err = p.Provision(context.Background(), psID, appID) // 二次
	if err != nil {
		t.Fatalf("二次 Provision: %v", err)
	}
	if fakeAdm.createDBCalls() != createsBefore {
		t.Fatalf("二次 Provision 应复用未再建库，createDB 调用数变化 %d→%d", createsBefore, fakeAdm.createDBCalls())
	}
	// 复用返回的是同一条记录
	got, _ := store.GetAppDBByApp(context.Background(), appID)
	if got.DBName != first.DBName {
		t.Fatalf("复用库名不一致 %q vs %q", got.DBName, first.DBName)
	}
}
```

> `newFakeProvisioner` / `fakeAdm.createDBCalls()`：读 provisioner_test.go 既有 fake（PGAdmin 的 fake 实现）。若现有 fake 无计数，加一个计数字段（fake 是测试代码，可改）。实现者照现有 fake 模式扩展。

- [ ] **Step 2: 跑确认失败** — `go test ./internal/pgsupply/ -run TestProvision_IdempotentReuse -v` → FAIL（二次 Provision 又建了一次库 / 无 reuse）。

- [ ] **Step 3: 实现** — `provisioner.go` 的 `Provision` 函数体最前面（配额检查之前或之后均可，建议配额之后、GetOrCreate 之前）加：

```go
	// 幂等：同 app 已有 ready 库 → 复用，不重建（auto-provision 与 declared 共存期安全）。
	if existing, err := p.store.GetAppDBByApp(ctx, appID); err == nil && existing != nil &&
		(existing.Status == StatusReady || existing.Status == StatusProvisioning) {
		// ready 直接返回；provisioning（并发中）也返回既有，避免重复建。
		if existing.Status == StatusReady {
			return existing, nil
		}
	}
```

> 放在配额检查之后（`p.quota.CheckDatabases/CheckDBSize` 之后），避免复用也消耗配额检查（虽然 check 是只读，但语义上 reuse 不该再卡配额）。实现者按 provisioner.go 现有结构定位插入点（GetOrCreate 调用之前）。

- [ ] **Step 4: 跑确认通过** — `go test ./internal/pgsupply/ -run TestProvision_IdempotentReuse -v` → PASS。再 `go test -p 1 ./internal/pgsupply/...`（既有测零回归）。

- [ ] **Step 5: Commit** — `feat(pgsupply): Provision 幂等(已ready复用,避免重复建库)`

---

### Task 3: pg KindSpec + BuildSpecs/NewReconciler 装配 + DepsCatalog 加 pg

**Files:**

- Create: `internal/mwsupply/spec_pg.go`, `internal/mwsupply/spec_pg_test.go`
- Modify: `internal/mwsupply/specs.go`（BuildSpecs 加 provisioner 参数 + 注册 pg）
- Modify: `internal/mwsupply/supply.go`（NewReconciler 加 provisioner 参数；DepsCatalog Kinds 加 "pg"）
- Modify: `cmd/server/main.go`（NewReconciler 调用传 pgProvisioner）

**Interfaces:**

- Consumes: Task 1 的 `SupplyShared` 字段；Task 2 的幂等 `pgsupply.Provisioner.Provision(ctx, psID, appID) (*pgsupply.AppDatabase, error)`；`AppDatabase.PGInstanceID`/`DBName`。
- Produces: `pgSpec(prov *pgsupply.Provisioner) KindSpec`；`BuildSpecs(store, flusher, ready, docker, pgProv)`；`NewReconciler(..., pgProv)`。

- [ ] **Step 1: 写失败测试** — `spec_pg_test.go`

```go
package mwsupply

import "testing"

func TestPgSpec_Fields(t *testing.T) {
	s := pgSpec(nil) // provisioner 传 nil（本测只验静态字段）
	if s.Kind != "pg" || s.AddrEnv != "DATABASE_URL" {
		t.Fatalf("pg 基本字段错: %+v", s)
	}
	// pg 自管 env：SharedEnv/DedicatedEnv 应为 nil（DATABASE_URL 由 pgsupply.Provision 写）
	if s.SharedEnv != nil || s.DedicatedEnv != nil {
		t.Fatalf("pg SharedEnv/DedicatedEnv 应 nil（自管 env），得 %+v/%+v", s.SharedEnv, s.DedicatedEnv)
	}
	// SupplyShared 必须设（pg 走自管路径）
	if s.SupplyShared == nil {
		t.Fatal("pg spec 必须设 SupplyShared")
	}
	// AllocSharedToken 应为 nil（pg 不走默认 token 路径）
	if s.AllocSharedToken != nil {
		t.Fatal("pg AllocSharedToken 应 nil（走 SupplyShared 自管路径）")
	}
}
```

- [ ] **Step 2: 跑确认失败** — `go test ./internal/mwsupply/ -run TestPgSpec_Fields -v` → FAIL（`pgSpec` 未定义）。

- [ ] **Step 3: 实现 pg spec** — `spec_pg.go`

```go
package mwsupply

import (
	"context"

	"zhiyuan-anp/platform/backend/internal/pgsupply"
)

// pgSpec 构造 pg 的 KindSpec（P2a：仅 shared；bind_existing/dedicated 留 P2b）。
// shared 走自管路径 SupplyShared：调 pgsupply.Provisioner.Provision（全套：项目空间 PG 实例 + 建库/role
// + 写 DATABASE_URL env + 写 appdeploy_database），返回 (instanceID="", token=库名)。
// binding 不存 service_instance_id（FK→service_instance，而 pg 实例在 pg_instance 表）；明细在 appdeploy_database。
func pgSpec(prov *pgsupply.Provisioner) KindSpec {
	return KindSpec{
		Kind: "pg", DisplayName: "PostgreSQL", AddrEnv: "DATABASE_URL", Token: "database-name",
		// pg 自管 env（pgsupply.Provision 写 DATABASE_URL）：不设 SharedEnv/DedicatedEnv/AllocSharedToken。
		SupplyShared: func(ctx context.Context, appID, psID string) (string, string, error) {
			appDB, err := prov.Provision(ctx, psID, appID)
			if err != nil {
				return "", "", err
			}
			return "", appDB.DBName, nil // instanceID 空（pg binding 不存）；token=库名
		},
	}
}
```

> `Token` 字段类型是 `TokenSemantics`（string 别名）。spec §4.1 列了 `TokenDatabaseName` 常量但 P1 未定义——这里用字面量 `"database-name"`，或先在 kindspec.go 加 `TokenDatabaseName TokenSemantics = "database-name"` 常量再用。**推荐加常量**（在 Task 3 Step 3 一并加到 kindspec.go 的 TokenSemantics const 块）。

- [ ] **Step 4: BuildSpecs/NewReconciler 加 provisioner** — `specs.go`：

```go
package mwsupply

import "zhiyuan-anp/platform/backend/internal/pgsupply"

// BuildSpecs 构造并注册 redis/milvus/pg 的 KindSpec。NewReconciler 调一次。
func BuildSpecs(store *Store, flusher DBFlusher, ready ReadyChecker, docker MWDockerRunner, pgProv *pgsupply.Provisioner) {
	RegisterKind(redisSpec(store, flusher, ready, docker))
	RegisterKind(milvusSpec(store, docker))
	RegisterKind(pgSpec(pgProv))
}
```

`supply.go` NewReconciler 加参数：

```go
func NewReconciler(store *Store, env EnvWriter, flusher DBFlusher, ready ReadyChecker, docker MWDockerRunner, host string, pgProv *pgsupply.Provisioner) *Reconciler {
	r := &Reconciler{store: store, env: env, flusher: flusher, ready: ready, docker: docker, host: host}
	BuildSpecs(store, flusher, ready, docker, pgProv)
	return r
}
```

> **注意 import 环**：mwsupply → pgsupply 是否成环？pgsupply 不 import mwsupply（pgsupply 只 import appdeploy/dConfig）。读 pgsupply 的 import 确认无 mwsupply 引用——若无环，直接 import；若有环，改 pgSpec 接受接口（`type PgProvisioner interface{ Provision(ctx,psID,appID)(*AppDB,error) }`）并在 main.go 用 adapter。**先按非环实现**，`go build` 验证。

- [ ] **Step 5: DepsCatalog 加 pg** — `supply.go` 的 `DepsCatalog`，`Kinds` 改：

```go
		Kinds: []string{"redis", "milvus", "pg"},
```

> P2a pg 仅 shared：strategy 选项仍三选一（catalog 通用），但 pg 选 bind_existing/dedicated 时 supplyOne 会走对应分支——pg 没设 dedicated/bind_existing 实现（无 LaunchDedicated 等），supplyDedicated 会 `spec.PortRange()==nil` panic 或 LookupBindExisting 返空。**P2a 限制**：在 `supplyOne` 对 pg 的 bind_existing/dedicated 给明确失败（不 panic）——加一行兜底：若 `spec.SupplyShared != nil && strategy != ModeShared` → mkBind failed "pg 仅支持 shared（P2a）"。在 supplyOne 的 strategy 分派处加。

- [ ] **Step 6: main.go 装配** — `cmd/server/main.go:186` NewReconciler 调用加 `pgProvisioner`（:182 已构造）：

```go
	mwReconciler := mwsupply.NewReconciler(mwStore, appDeployStore, mwProbe, mwProbe, mwsupply.NewOSDocker(), cfg.AppDeployHost, pgProvisioner)
```

- [ ] **Step 7: 跑确认通过** — `go test ./internal/mwsupply/ -run TestPgSpec_Fields -v` → PASS。再 `go test -p 1 ./internal/mwsupply/... ./internal/pgsupply/...` + `go build ./cmd/server` → 全绿。

- [ ] **Step 8: Commit** — `feat(mwsupply): pg shared 接入 KindSpec registry(复用 pgsupply.Provision)`

---

### Task 4: 端到端验证（声明 pg=shared → 部署 → binding+DATABASE_URL）

**Files:** 无（验证 task；可加 e2e 测或手工 .28 验证）

- [ ] **Step 1: 单测级 e2e** — 在 mwsupply 加一个集成测（连 anp_test）：建 app + declared binding(kind=pg, shared) → Reconcile → 断言 binding status=bound, isolation_token=非空（库名）, service_instance_id=""（空），且 appdeploy_database 有一条该 app 的记录, DATABASE_URL env 已写。用真 pgsupply.Provisioner（连 anp_test 的 PG 实例——若测试环境无项目 PG 实例，InstanceManager.GetOrCreate 会 docker 起，测试可能受限；若不可行，用 fake provisioner 验 supplyShared→SupplyShared→binding 链路，跳过真 PG）。实现者按测试环境能力选择真/fake。

- [ ] **Step 2: 全量回归** — `go test -p 1 ./internal/mwsupply/... ./internal/pgsupply/... ./internal/appdeploy/...` → 全绿。

- [ ] **Step 3: .28 部署暂缓**（与 P1 一致，等 P2b/P3 一起 scp 重建 backend）。push 分支续攒。

---

## Self-Review

**1. Spec 覆盖：** §5 pg shared（复用 pgsupply.Provision）→Task3；§4.1 KindSpec 扩展（SupplyShared）→Task1；§6 数据模型（pg binding 不存 service_instance_id，明细在 appdeploy_database）→Task3 注释+设计；§8 迁移（auto→declared）→**P3 非 P2a**（P2a 靠 Task2 幂等让 auto+declared 共存）。bind_existing/dedicated→P2b。✓

**2. 占位扫描：** Task1 两个 reuse `if` 已合并为一个（Step4 注明）；Task2 插入点定位明确（GetOrCreate 之前）；Task3 import 环有应对（非环/接口）。helper（newTestReconciler/newFakeProvisioner）指引读既有测试照抄，非 TODO。✓

**3. 类型一致：** `SupplyShared` 签名 Task1 定义 `(ctx,appID,psID)(instanceID,token,err)`，Task3 pgSpec 用同签名。`BuildSpecs`/`NewReconciler` 加 `pgProv *pgsupply.Provisioner` 一致。`TokenDatabaseName` 常量 Task3 加到 kindspec.go。✓

**4. 自编译：** Task1（字段+分支，redis/milvus 不受影响）独立编译；Task2（pgsupply 幂等）独立；Task3（pg spec+装配+catalog）依赖 Task1 字段 + Task2 幂等，但同 task 内完成所有引用 → 每 task 末 build 通过。✓

**5. 风险：** (a) mwsupply→pgsupply import 环——Task3 Step4 已给应对；(b) pg binding 的 service_instance_id FK——设计为空（Task3 注释），明细在 appdeploy_database；(c) 测试环境无项目 PG 实例时真 e2e 受限——Task4 Step1 给 fake fallback。

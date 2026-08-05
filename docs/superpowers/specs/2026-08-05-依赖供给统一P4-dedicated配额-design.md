# 依赖供给统一 P4：dedicated 实例数配额 设计

> 日期：2026-08-05 ｜ 状态：设计（待评审）｜ 上游 spec：`2026-08-04-依赖供给统一三策略-design.md`（§9）
> 相关代码：`internal/quota/`、`internal/mwsupply/supply.go`、`cmd/server/main.go`、迁移 `internal/db/migrations/pg/`

## 1. 背景

P1–P3 完成依赖供给统一：pg 三策略齐全 + 声明驱动。dedicated 策略每声明一个就起一个独立容器（redis / milvus / pgvector），是**重资源**。当前 dedicated 供给**无任何数量限制**——一个项目空间可无限起 dedicated 容器，撑爆 .28 共享机（端口 / 内存 / 容器数）。

上游 spec §9 已规划：dedicated 实例数 **per 项目空间** 上限，redis/milvus/pg dedicated 容器**合计封顶**，复用 `quota.Service` 模式，供给前拦。P4 落地它。

## 2. 目标 / 非目标

**目标**

1. `quota.Service` 加第 5 维度 `MaxDedicatedInstances`（默认 5/项目空间，admin 可调）。
2. `mwsupply.supplyDedicated` 起容器**前**硬拦：超限→binding failed、不起容器（hard-block，spec §9「供给前拦」）。
3. 计数口径：该 ps 下所有 active dedicated `service_instance`（redis/milvus/pg 合计），经 binding→app 归属 ps。
4. admin 可配置 + 看板可见（Set/Usage 加这维）。

**非目标**

- 不做 per-kind 配额（合计封顶，spec §9）。
- 不改 shared / bind_existing（它们复用实例，无容器爆炸风险）。
- 不做 dedicated 容器的内存/CPU 配额（只数容器数；资源粒度配额未来）。

## 3. 关键决策

| 决策     | 选定                                                                      | 理由                                                                                   |
| -------- | ------------------------------------------------------------------------- | -------------------------------------------------------------------------------------- |
| 默认上限 | **5 / 项目空间**                                                          | dedicated 容器重，.28 共享机防滥；admin 可按 ps 调高                                   |
| 强制方式 | **hard-block**（超限 binding failed，不起容器）                           | spec §9「供给前拦」；防容器爆炸                                                        |
| 计数口径 | active dedicated `service_instance` JOIN binding JOIN app WHERE app.ps=$1 | dedicated 实例 `project_space_id=NULL`（P2b：靠 binding 关联 app），须经 app 归属 ps   |
| 接线     | `DedicatedQuotaChecker` 接口注入 `NewReconciler`（`quota.Service` 满足）  | 沿用 pgsupply `QuotaChecker` 注入模式；dedicated 总数跨 kind，不进 KindSpec.Quota 字段 |
| nil 语义 | `dedQuota=nil` → 不强制                                                   | 兼容未注入场景（开发/测试/旧装配），同 pgsupply QuotaChecker                           |

## 4. 改动详述

### 4.1 quota 第 5 维度（model / errors / store）

**`quota/model.go`**：

- const 块加 `DefaultMaxDedicatedInstances = 5`。
- `Quota` 结构加 `MaxDedicatedInstances int \`json:"max_dedicated_instances" db:"max_dedicated_instances"\``。
- `Usage` 结构加 `UsedDedicatedInstances int \`json:"used_dedicated_instances"\``。
- 维度常量加 `DimensionDedicated = "dedicated"`。

**`quota/errors.go`**：`QuotaExceededError.Error()` 的 dimLabel map 加 `DimensionDedicated: "专属实例数"`；调用方传 `Unit: "个"`。

**`quota/store.go`**（表 `project_quota`）：

- `quotaCols` 加 `max_dedicated_instances`（Get 的 SELECT 自动含）。
- `GetOrCreate` 的 INSERT 加列 + `DefaultMaxDedicatedInstances` 值。
- `Set` 签名加 `maxDedicatedInstances int` 参数 + UPDATE 加 `max_dedicated_instances=$N`。

### 4.2 迁移 000035

`internal/db/migrations/pg/000035_quota_dedicated.up.sql`：

```sql
-- 000035_quota_dedicated.up.sql
ALTER TABLE project_quota ADD COLUMN IF NOT EXISTS max_dedicated_instances INT NOT NULL DEFAULT 5;
```

`000035_quota_dedicated.down.sql`：

```sql
ALTER TABLE project_quota DROP COLUMN IF EXISTS max_dedicated_instances;
```

幂等（`IF NOT EXISTS`/`IF EXISTS`）；存量行 backfill 默认 5（`DEFAULT 5` + `NOT NULL`）。

### 4.3 quota.Service：Check + count + Set/Usage

**`quota/service.go`**：

```go
// CheckDedicatedInstances 专属实例数 check（redis/milvus/pg dedicated 容器合计，per 项目空间）。
// 超限返回 *QuotaExceededError（mwsupply.supplyDedicated 起容器前调）。
func (s *Service) CheckDedicatedInstances(ctx context.Context, psID string) error {
	q, err := s.store.GetOrCreate(ctx, psID)
	if err != nil { return err }
	used, err := s.countDedicatedInstances(ctx, psID)
	if err != nil { return err }
	if used >= q.MaxDedicatedInstances {
		return &QuotaExceededError{Dimension: DimensionDedicated, Used: used, Limit: q.MaxDedicatedInstances, Unit: "个"}
	}
	return nil
}

// countDedicatedInstances 该 ps 下 active dedicated 实例数（distinct；dedicated 实例 ps=NULL，经 binding→app 归属 ps）。
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

- `Set` 签名加 `maxDedicatedInstances int`（透传 store.Set）。
- `Usage` 加 `u.UsedDedicatedInstances, err = s.countDedicatedInstances(ctx, psID)`。

### 4.4 quota handler（admin 配置 + 看板）

`quota/handler.go`：Set 请求体加 `MaxDedicatedInstances *int \`json:"max_dedicated_instances" validate:"omitempty,min=0,max=1000"\``；调用 `svc.Set(..., maxDedicated)`透传。Usage 响应自动含`used_dedicated_instances`（via Usage struct）。swagger 文档同步（可选，非阻塞）。

### 4.5 mwsupply 强制点（接线 + enforce）

**`mwsupply/supply.go`**：

- 加接口（同 EnvWriter/PgDedicatedRunner 风格）：

```go
// DedicatedQuotaChecker 专属实例数配额检查（quota.Service 实现）。
// nil=不强制（开发/测试或未注入 quota）。
type DedicatedQuotaChecker interface {
	CheckDedicatedInstances(ctx context.Context, psID string) error
}
```

- `Reconciler` 加字段 `dedQuota DedicatedQuotaChecker`；`NewReconciler` 末参加 `dedQuota DedicatedQuotaChecker`（赋值 `r.dedQuota`）。
- `supplyDedicated`：**reuse 判定之后、起容器之前**（即默认 `PortRange` 路径与 pg `spec.SupplyDedicated` 路径之前）插入：

```go
if r.dedQuota != nil {
	if err := r.dedQuota.CheckDedicatedInstances(ctx, psID); err != nil {
		mkBind(StatusFailed, "", "", "专属中间件实例数已达上限: "+err.Error())
		return
	}
}
```

位置：`supplyDedicated` 里，紧跟现有 reuse 复用判定块（已 bound 同实例→早返回）之后，`if spec.SupplyDedicated != nil { ... }` 与默认 `lo, hi := spec.PortRange()` 之前。reuse 已 bound 的不重供、不耗新配额，故放在 reuse 之后。

**`cmd/server/main.go`**：`NewReconciler(...)` 调用加 `quotaSvc`（已构造的 `*quota.Service`，满足 `DedicatedQuotaChecker`）。若 main 尚未构造 quota.Service 给 mwsupply，则就近构造/取既有传入。

### 4.6 行为

- 声明 dedicated 且该 ps active dedicated 实例数已达上限 → 部署时 `supplyDedicated` 返回 binding `failed`（last_error 含「专属中间件实例数已达上限：N个 / 5个」），**不起容器**、不登记 service_instance。best-effort 总语义不变（不阻塞部署主流程，仅该 binding failed）。
- admin 调高 `max_dedicated_instances` → 下次部署通过。
- redis/milvus/pg dedicated 共享同一计数池（合计封顶）。

## 5. 测试策略（分层）

- **quota 单测**（`service_test.go`/`store_test.go`，真 PG anp_test）：
  - `TestService_CheckDedicatedInstances_WithinLimit`：建 N<5 个 dedicated（binding+app+ps+instance 行）→ 通过。
  - `TestService_CheckDedicatedInstances_Exceeded`：建 5 个 → 第 6 次 Check 返 `*QuotaExceededError`（Dimension=dedicated）。
  - count 口径：跨 kind 合计（建 redis+pg dedicated 都算）；非 active / 非 dedicated 不算。
  - store：Get/Set/GetOrCreate 含新列；迁移后默认 5。
- **mwsupply 单测**（`supply_*test.go`，fake）：
  - fake `DedicatedQuotaChecker` 返超限 → `supplyDedicated` mkBind failed、**不调** `LaunchDedicated`/`spec.SupplyDedicated`（fake docker/ded 计数验证 0 调用）。
  - `nil` dedQuota → 不拦（既有 dedicated 用例回归绿）。
  - reuse（已 bound dedicated）→ 不调 Check（不耗配额）。
- **回归**：`go test -p 1 ./internal/quota/... ./internal/mwsupply/... ./internal/pgsupply/... ./internal/appdeploy/...` 全绿；redis/milvus shared/bind_existing 零行为变化。
- **.28 e2e**：scp + 迁移 000035 + 重建 backend；声明 5 个 dedicated 通过、第 6 个 binding failed 不起容器；admin 调高→通过。

## 6. 影响面

| 文件                                                             | 改动                                                                                  |
| ---------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| `internal/quota/model.go`                                        | +MaxDedicatedInstances 字段/默认/维度常量；Usage +UsedDedicatedInstances              |
| `internal/quota/errors.go`                                       | dimLabel +dedicated                                                                   |
| `internal/quota/store.go`                                        | quotaCols/GetOrCreate/Set 加列+参数                                                   |
| `internal/quota/service.go`                                      | +CheckDedicatedInstances/+countDedicatedInstances；Set/Usage 加维                     |
| `internal/quota/handler.go`                                      | Set 体 +max_dedicated_instances                                                       |
| `internal/db/migrations/pg/000035_quota_dedicated.{up,down}.sql` | 新增列                                                                                |
| `internal/mwsupply/supply.go`                                    | +DedicatedQuotaChecker 接口；Reconciler 字段+NewReconciler 参；supplyDedicated 强制点 |
| `cmd/server/main.go`                                             | NewReconciler 传 quota.Service                                                        |
| 测试                                                             | quota + mwsupply 上述新增                                                             |

## 7. 风险

- **签名变更**：`store.Set` / `service.Set` 加参波及 handler 调用点；编译强校验，零遗漏。
- **NewReconciler 加参**：波及 main.go + 测试构造（`newReconcilerTest*` 须加 dedQuota，传 nil 保持旧行为）；编译强校验。
- **计数口径**：依赖 dedicated 实例经 binding→app 归属 ps（P2b 既定）；若未来 dedicated 实例改挂 ps 直存，count 查询同步改（spec 注明）。
- **迁移**：`ADD COLUMN ... DEFAULT 5 NOT NULL` 在 PG 11+ 是瞬时元数据操作（非重写表），存量行即时 backfill，无长锁。
- **best-effort**：配额检查失败（DB 错）不阻塞部署主流程——但为防"配额查不到就放行"，count/Check 的 DB 错**返回 err 让 binding failed**（不放行），同 CheckApps 语义。

# 依赖供给统一 P2b：pg bind_existing + dedicated Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use `- [ ]`.

**Goal:** 给 pg 补齐 bind_existing（注入现成 DSN，不建库）+ dedicated（独立 pgvector 容器 + 库/role）。至此 pg 三策略齐全。redis/milvus 零行为变化。

**Architecture:** KindSpec 加两个可选字段：`ConnValue func(inst)string`（bind_existing 连接值；默认 ConnStr，pg=inst.AuthRef 即 DSN）+ `SupplyDedicated func(ctx,appID,psID,host)(instID,token,err)`（dedicated 自管；pg 设它起容器+建库/role+写 DATABASE_URL+登记 service_instance，redis/milvus 不设走原 LaunchDedicated 三件套）。bind_existing 路径用 ConnValue；supplyDedicated 加 SupplyDedicated 分支；放开 P2a 的 pg guard。pg dedicated 复用 pgsupply 的 DockerRunner/PGAdmin（新增 pgsupply dedicated helper），service_instance 登记+清理走 mwsupply。

**Tech Stack:** Go 1.24（mwsupply + pgsupply）；PG（anp_test）；TDD + 回归。

**Spec:** `docs/superpowers/specs/2026-08-04-依赖供给统一三策略-design.md`（§5）

## Global Constraints

- go 命令前缀 `GOPATH=C:/Users/yxt/go`；禁 SQLite；PG 测试连 .28 anp_test。
- commit `feat(mwsupply): 中文` + `Co-Authored-By: Claude <noreply@anthropic.com>`。
- **零行为变化（redis/milvus）**：mwsupply+pgsupply 测试全绿 + `go build ./cmd/server` 通过。
- 自编译：每 task 末 build 通过。
- pg bind_existing 不建库/role（注入现成 DSN）；pg dedicated 建 per-app 容器+库/role，登记 service_instance（container_name 供清理）。

## File Structure

| 文件                                                      | 责任                                                                                         | 动作          |
| --------------------------------------------------------- | -------------------------------------------------------------------------------------------- | ------------- |
| `internal/mwsupply/kindspec.go`                           | 加 `ConnValue` + `SupplyDedicated` 可选字段                                                  | 改（T1）      |
| `internal/mwsupply/supply.go`                             | supplyOne bind_existing 用 ConnValue；supplyDedicated 加 SupplyDedicated 分支；放开 pg guard | 改（T1）      |
| `internal/mwsupply/supply_*_test.go`                      | ConnValue / SupplyDedicated 分支测（fake）                                                   | 新建/改（T1） |
| `internal/mwsupply/spec_pg.go`                            | pg 加 ConnValue=AuthRef + SupplyDedicated + CleanupDedicated                                 | 改（T2/T3）   |
| `internal/pgsupply/provisioner.go`（或新 dedicated.go）   | `ProvisionDedicated`（per-app 容器+库/role+DSN）+ `CleanupDedicated`（docker rm）            | 新增（T3）    |
| `internal/mwsupply/specs.go` / `supply.go`(NewReconciler) | pgSpec 注入 pgsupply dedicated 依赖                                                          | 改（T3）      |

---

### Task 1: ConnValue + SupplyDedicated 字段 + 分支 + 放开 guard

**Files:** `kindspec.go`（加字段）、`supply.go`（bind_existing 用 ConnValue、supplyDedicated 加分支、放开 guard）、测试。

**Interfaces:**

- Produces: `KindSpec.ConnValue func(inst *ServiceInstance) string`（nil→默认 ConnStr）；`KindSpec.SupplyDedicated func(ctx, appID, psID, host string) (instanceID, token string, err error)`（nil→默认 LaunchDedicated 路径）。

- [ ] **Step 1: 写失败测试** — fake spec：ConnValue 返回 inst.AuthRef（模拟 pg DSN），bind_existing 注入该值；fake SupplyDedicated 被调用并写 binding。两个测覆盖。

- [ ] **Step 2: 跑确认失败** → FAIL（字段未定义）。

- [ ] **Step 3: 加字段** — `kindspec.go`：

```go
	// ConnValue（可选）：bind_existing 的连接 env 值。nil→默认 ConnStr(inst)（host:port）。
	// pg 设为 inst.AuthRef（登记的完整 DSN）。
	ConnValue func(inst *ServiceInstance) string
	// SupplyDedicated（可选）：自管 dedicated 供给的 kind（pg：起 per-app 容器+建库/role+写 env+登记 service_instance）。
	// nil→走默认 LaunchDedicated/ReadyDedicated/CleanupDedicated 路径。
	SupplyDedicated func(ctx context.Context, appID, psID, host string) (instanceID, token string, err error)
```

- [ ] **Step 4: supplyOne bind_existing 用 ConnValue** — `supply.go` bind_existing 段，`connStr := ConnStr(inst)` 改为：

```go
	connVal := ConnStr(inst)
	if spec.ConnValue != nil {
		connVal = spec.ConnValue(inst)
	}
	// ... UpsertEnv(ctx, appID, spec.AddrEnv, connVal, isSecret, "platform") ...
```

- [ ] **Step 5: supplyDedicated 加 SupplyDedicated 分支** — `supply.go` supplyDedicated 函数体最前面（复用判定之后、默认 PortRange 路径之前）：

```go
	if spec.SupplyDedicated != nil {
		instID, token, err := spec.SupplyDedicated(ctx, appID, psID, r.host)
		if err != nil { mkBind(StatusFailed, "", "", "起 "+dep.Kind+" 容器: "+err.Error()); return }
		mkBind(StatusBound, instID, token, "")
		return
	}
```

（复用判定保留：binding 已 bound → 不再 SupplyDedicated。）

- [ ] **Step 6: 放开 pg guard** — `supply.go` supplyOne 的 P2a guard（`spec.SupplyShared != nil && strategy != ModeShared` → failed）改为允许 bind_existing/dedicated。即把 guard 条件去掉/改注释（pg 现在三策略都支持）。注意：bind_existing pg 走默认 bind_existing 路径（ConnValue=DSN）；dedicated pg 走 SupplyDedicated 分支。

- [ ] **Step 7: 跑确认通过 + 回归** — `go test -p 1 ./internal/mwsupply/...`（含新测 + 既有零回归）+ `go build ./cmd/server`。

- [ ] **Step 8: Commit** — `feat(mwsupply): KindSpec 加 ConnValue/SupplyDedicated 字段+分支,放开pg guard`

---

### Task 2: pg bind_existing（注入现成 DSN）

**Files:** `spec_pg.go`（pg 加 ConnValue=AuthRef）、测试。

**Interfaces:** pg KindSpec 设 `ConnValue: func(inst) string { return inst.AuthRef }`。

- [ ] **Step 1: 写失败测试** — 登记一个 service_instance(kind=pg, bind_existing, auth_ref="postgres://x:y@h:p/db")，声明 pg=bind_existing → supplyOne bind_existing → 注入 DATABASE_URL=该 DSN，binding bound、service_instance_id=该实例、token 空、不建库（无 appdeploy_database）。

- [ ] **Step 2: 跑确认失败** → FAIL（pg bind_existing 还走 ConnStr/host:port）。

- [ ] **Step 3: 实现** — `spec_pg.go` pgSpec 返回的 KindSpec 加：

```go
		ConnValue: func(inst *ServiceInstance) string { return inst.AuthRef }, // bind_existing 注入现成 DSN
```

- [ ] **Step 4: 跑确认通过 + 回归**。

- [ ] **Step 5: Commit** — `feat(mwsupply): pg bind_existing 注入现成 DSN(不建库)`

---

### Task 3: pg dedicated（独立 pgvector 容器 + 库/role）

**Files:** `pgsupply/`（新增 dedicated helper）、`spec_pg.go`（pg SupplyDedicated + CleanupDedicated）、`specs.go`/`supply.go`（装配注入）、测试。

**Interfaces:**

- pgsupply 新增：`ProvisionDedicated(ctx, appID, psID, host) (containerName, dbName, dsn, err)` — docker run pgvector(per-app, 端口 9500-9599, 复用 DockerRunner.RunPGContainer) + waitForReady + 建库/role/grant(复用 PGAdmin) + 返回 containerName/dbName/dsn（**不**写 env、**不**登记 service_instance —— 由 mwsupply 闭包做）。
- mwsupply pg SupplyDedicated：调 pgsupply.ProvisionDedicated → 写 DATABASE_URL env(dsn) → 登记service_instance(kind=pg, dedicated, container_name, host, port, auth_ref=adminURL) → 返回 (instID, dbName)。
- pg CleanupDedicated：docker rm container_name（复用 pgsupply DockerRunner.RmForce）。

- [ ] **Step 1: 写失败测试** — fake pgsupply dedicated runner，声明 pg=dedicated → SupplyDedicated 调它 → binding bound、service_instance 登记了 container_name、DATABASE_URL 写入；release → CleanupDedicated(docker rm) 被调。

- [ ] **Step 2: 跑确认失败** → FAIL（pg SupplyDedicated 未实现）。

- [ ] **Step 3: pgsupply dedicated helper** — 新增（provisioner.go 或 dedicated.go）：

```go
// ProvisionDedicated 起 per-app 独立 PG 容器 + 建库/role，返回 containerName/dbName/dsn。
// 不写 env、不登记实例表（由 mwsupply 闭包做，保持 mwsupply 管实例登记+清理）。
// 复用 InstanceManager 的 docker/admin/waitForReady，但建 per-app 容器（非 per-project）。
func (p *Provisioner) ProvisionDedicated(ctx context.Context, appID, psID, host string) (container, dbName, dsn string, err error) {
	// 复用 pgsupply 内部：InstanceManager 暴露 docker/admin（或新增方法）。
	// container = InstanceName(psID)+"-ded-"+genShortID(); pwd=genPassword(); port=allocPort(docker.UsedPorts, 9500,9599)
	// docker.RunPGContainer; adminURL=DSN(host,port,postgres,pwd,postgres); waitForReady
	// dbName=DBName(appID); role=RoleName(dbName); admin.CreateDatabase/Role/GrantAll
	// dsn=DSN(host,port,role,pwd,dbName); return
}

// CleanupDedicated docker rm per-app 容器（mwsupply CleanupDedicated 调）。
func (p *Provisioner) CleanupDedicated(ctx context.Context, container string) error { ... docker.RmForce ... }
```

> 实现者：InstanceManager 的 docker/admin 字段未导出。最小改法：给 InstanceManager 加 `Docker()`/`Admin()` 访问器，或 Provisioner 持有 docker/admin 引用（NewProvisioner 已收 instanceMgr/store/admin/env/quota —— admin 已在；docker 加个参数或经 instanceMgr 暴露）。读 provisioner.go/instance.go 决定最小侵入方式。

- [ ] **Step 4: pg SupplyDedicated + CleanupDedicated** — `spec_pg.go`：

```go
		SupplyDedicated: func(ctx, appID, psID, host) (string, string, error) {
			container, dbName, dsn, err := pgProv.ProvisionDedicated(ctx, appID, psID, host)
			if err != nil { return "", "", err }
			_ = envWriter.UpsertEnv(ctx, appID, "DATABASE_URL", dsn, true, "platform")
			// 登记 service_instance（mwsupply store）—— 需 store 引用。pgSpec 闭包捕获 store。
			instID := registerPgDedicatedInstance(store, container, host, port, adminURL) // helper
			return instID, dbName, nil
		},
		CleanupDedicated: func(ctx, name string) error { return pgProv.CleanupDedicated(ctx, name) },
```

> pgSpec 签名扩展：需 store（登 service_instance）+ envWriter（写 DATABASE_URL）+ host。`pgSpec(pgProv, store, env, host)`。BuildSpecs/NewReconciler 透传（已有 store/env/host，加 pgProv 已在）。

- [ ] **Step 5: 装配** — `specs.go` pgSpec 调用加 store/env/host；`NewReconciler` 已有这些 + pgProv，调 BuildSpecs 透传。

- [ ] **Step 6: 跑确认通过 + 回归** — `go test -p 1 ./internal/mwsupply/... ./internal/pgsupply/...` + `go build ./cmd/server`。

- [ ] **Step 7: Commit** — `feat(mwsupply): pg dedicated 独立 pgvector 容器+库/role`

---

### Task 4: 回归 + .28 暂缓

- [ ] **Step 1**: `go test -p 1 ./internal/mwsupply/... ./internal/pgsupply/... ./internal/appdeploy/...` 全绿。
- [ ] **Step 2**: .28 部署暂缓（与 P1/P2a 一致，等 P3 一起）。push 续攒 PR #3。

---

## Self-Review

**1. Spec 覆盖：** §5 pg bind_existing（注入现成 DSN）→T2；pg dedicated（独立容器+库/role）→T3；KindSpec 扩展（ConnValue/SupplyDedicated）→T1。redis/milvus 不设新字段→零变化。✓
**2. 占位：** T3 pgsupply dedicated helper 的 docker/admin 访问方式给了最小改法指引（访问器或 Provisioner 持引用），实现者按现有结构定。pgSpec 签名扩展（store/env/host）明确。✓
**3. 类型一致：** ConnValue/SupplyDedicated 签名 T1 定义，T2/T3 引用一致。pgSpec 签名演进（pgProv→pgProv+store+env+host）T3 明确。✓
**4. 自编译：** T1（字段+分支，redis/milvus 不受影响）独立；T2（pg ConnValue）独立；T3（pg dedicated+装配）依赖 T1 字段，同 task 完成。✓
**5. 风险：** (a) pgsupply docker/admin 未导出——T3 Step3 给应对；(b) pg dedicated 的 service_instance 登记需 store——pgSpec 闭包捕获，T3 注明；(c) cleanup 需 container_name——service_instance.container_name 存，mwsupply Cleanup 读它调 spec.CleanupDedicated。

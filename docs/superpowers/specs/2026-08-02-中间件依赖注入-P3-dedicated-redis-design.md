# 中间件依赖注入 P3 设计 —— dedicated 专属 Redis（每 app 一个容器）

- **类型**：方案 / 详细设计（P1 设计 §10 分期里的 P3 阶段）
- **状态**：待审核（按「方案先成文审核」，审核通过后再开 plan → 实现）
- **日期**：2026-08-02
- **作者**：miscode + Claude
- **关联模块**：`mwsupply`（扩展 dedicated 分支 + Cleanup）、`appdeploy`（Delete handler 接 Cleanup、`MWReconciler` 接口加方法）
- **关联文档**：
  - [中间件依赖供给与注入 设计（P1 总设计）](2026-08-01-中间件依赖供给与注入-design.md) —— 本文件是其 §10「P3：dedicated」的细化
  - [P2 shared redis 设计](2026-08-01-中间件依赖注入-P2-shared-redis-design.md) —— 同一 mwsupply 包的前一阶段（shared db 号隔离），本文件延续其范式
  - [多形态应用治理 PRD §4.4/§7/§8/§9](../../PRD/2026-08-01-多形态应用治理与开发运维统筹-PRD.md)
  - [应用库与 API 统一管理设计](2026-07-21-应用库与API统一管理-设计.md)（pgsupply 范式来源：InstanceManager/Provisioner/Cleanup）

---

## 1. 背景与目标

### 1.1 P1/P2 已闭环

- **P1（bind_existing）**：`.anp/deps.yaml` 声明依赖 → `buildAndDeploy` 在 `EnvPairs()` 前调 `mwsupply.Reconcile` → 查 `appdeploy_service_instance` 注册表（bind_existing）→ 经 `EnvWriter.UpsertEnv(source=platform)` 写 `REDIS_ADDR` 等 → 现有 `docker run -e` 注入。`.28` live 验证通过。
- **P2（shared）**：扩 shared 分支，共享 redis 实例 + db 号隔离（`db_range:[1,15]`，最小空闲号分配 + 重分配 flush + CASCADE 回收）。`.28` live 验证通过（commit 252ff07）。关键教训：`.28` backend（`deploy_default` 网）拨 redis host LAN IP 超时 → flush 降级 best-effort。

### 1.2 P3 要补的缺口

P2 的 `supply.go:70` 对一切非 `bind_existing`/`shared` 策略一刀切标 `failed`：

```go
if strategy != ModeBindExisting {
    mkBind(StatusFailed, "", "", "策略 "+strategy+" 暂未实现（仅 bind_existing/shared）")
    return
}
```

`model.go` 已声明 `ModeDedicated = "dedicated"` 常量，但无任何分支消费它。**本设计填 dedicated 分支**，且**仅做 redis**（理由见 §2）。

### 1.3 目标

应用在 `.anp/deps.yaml` 声明 `strategy: dedicated` 的 redis 依赖 → 平台为该 app **docker 起一个专属 redis 容器**（端口池分配端口 + `requirepass` 鉴权）→ 注入 `REDIS_ADDR`（AppDeployHost:port）+ `REDIS_PASSWORD` → 该 app 独占整实例；删 app 时 `docker rm` 容器；重部署复用容器、保数据。

**与 shared 的本质区别**：shared 是「共享实例 + db 号逻辑隔离」（轻、但有跨租户数据卫生问题，靠 flush 兜底）；dedicated 是「每 app 物理独立实例」（重、但天然强隔离、无 flush 需求）。dedicated 是 PRD §4.4 的默认供给策略。

---

## 2. 范围

### 2.1 本期做（in）

- redis dedicated 全链路：端口分配 + 起 redis 容器 + 就绪检测 + 实例行登记 + 双 env 注入 + 删 app `docker rm` Cleanup
- `MWReconciler` 接口加 `Cleanup` 方法 + Delete handler 接入（dedicated 容器是宿主资源，CASCADE 删 DB 行不删容器 → 必须显式回收，类比 pgsupply.Cleanup）

### 2.2 本期不做（out / YAGNI）

- **milvus dedicated**：milvus standalone 容器重（依赖 etcd/minio、启动慢、内存需求高），值得独立设计，留后续
- **项目级 dedicated 池 / dedicated 实例复用**：本期 dedicated 严格 1:1（一个 app 一个容器），不做「同项目多 app 共享一个 dedicated 实例」的池化
- **配额模块接入**：配额即端口池（池满 failed），不动 `internal/quota`（同 P2 思路）
- **vault/KMS 密钥管理**：`requirepass` 明文存 `auth_ref`（同 P1/P2、同 pgsupply I1 债），阶段 3 接 vault
- **strategy 热切换清理**：app 从 shared 切 dedicated 时残留 `REDIS_DB` env 行的清理（边缘场景，§9 记债）
- **监控/备份/迁移**：dedicated redis 的指标采集/备份不在本期

---

## 3. 关键决策

| 维度        | 选择                                                                                        | 理由                                                                                                                                                               |
| ----------- | ------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 粒度        | **每 app 一个专属 redis 容器（1:1）**                                                       | 物理强隔离；用户明确「每 app 专属」。比 PG 的「项目级实例 + 库-per-app」更细，但 redis 容器轻（~10MB，秒起），可承受                                               |
| 容器建模    | **A1：每 app 一条 `supply_mode='dedicated'` 实例行**（带 `container_name`），binding 指向它 | 完全对称 pgsupply（PGInstance 行 = 容器记录）；instance 行是容器单一事实源（host/port/auth_ref/container_name），Cleanup 经 binding→instance→container_name 回收   |
| docker 操作 | **B1：mwsupply 自包含 `MWDockerRunner`**（UsedPorts/RunRedisContainer/RmForce）             | 与 mwsupply 自包含哲学一致（connstr/redisflush 都不引依赖、不跨包）；不动已验证的 pgsupply；~40 行可接受重复                                                       |
| 网络        | **publish 到宿主 + `REDIS_ADDR=AppDeployHost:port`**                                        | 与 bind_existing/shared 同范式；app 容器（默认 bridge）走 host LAN IP:port 可达（P2 已验证）。**dedicated 无 flusher** → 天然避开 P2 的 .28 backend↔redis 不可达坑 |
| 鉴权        | **`requirepass=genPassword()` + 注入 `REDIS_PASSWORD`**                                     | 专属新容器由平台控制，设密码比 .28 bind_existing 无密码更卫生；对称 pgsupply 每 app 独立 role+密码                                                                 |
| 端口池      | **9600-9699**（PG 占 9500-9599，避开）                                                      | 100 槽；`allocPort` 复用 pgsupply 同款纯函数（最小未占用号）                                                                                                       |
| 配额        | **端口池即配额**（池满 failed），**不动 `internal/quota`**                                  | 同 P2 db_range 思路；dedicated 实例天然 app 作用域，塞项目级 quota 别扭                                                                                            |
| 回收        | **Delete handler 显式 `mwReconciler.Cleanup` → docker rm + 删 instance 行**                 | dedicated 容器是宿主资源，CASCADE 只删 DB 行不删容器（资源泄漏，类比 pgsupply I2）。与 P2 shared（靠 CASCADE 零改 Delete）**不同**                                 |
| 就绪检测    | **AUTH+PING 轮询**（复用 redisflush.go 裸 RESP dial）                                       | 不登记未就绪实例；仿 pgsupply `waitForReady`                                                                                                                       |
| 重启策略    | **`--restart unless-stopped`**                                                              | 宿主重启后 redis 容器自恢复（同 pgsupply PG 容器）                                                                                                                 |

---

## 4. 数据模型（迁移 000030 加 container_name 列）

### 4.1 现状

`appdeploy_service_instance`（000028 建）列：`id, project_space_id, kind, name, supply_mode, host, port, auth_ref, isolation, status, created_at, updated_at`。**无 `container_name`**（pgsupply.PGInstance 有，mwsupply.ServiceInstance 没有）。dedicated Cleanup 要 `docker rm` 必须知道容器名。

### 4.2 新增迁移 `000030_mwsupply_dedicated.up.sql` / `.down.sql`

```sql
-- 000030_mwsupply_dedicated.up.sql
-- P3：dedicated 专属 redis（每 app 一个容器）
-- 加 container_name 列：dedicated 实例的容器名（Cleanup 时 docker rm 用）。
-- nullable：bind_existing/shared 种子行及非 dedicated 实例恒 NULL，无影响。

ALTER TABLE appdeploy_service_instance ADD COLUMN IF NOT EXISTS container_name TEXT;
```

```sql
-- 000030_mwsupply_dedicated.down.sql
ALTER TABLE appdeploy_service_instance DROP COLUMN IF EXISTS container_name;
```

> **不加种子**：dedicated 实例由供给流程动态创建（每 app 一行），无平台级种子。
>
> **版本号 000030**：已确认 `migrations/pg/` 当前最大 000029（P2），000030 空闲。
>
> **不加唯一索引**：dedicated 实例 id 是 PK 已唯一；binding `(app_id, service_kind)` 已唯一兜底绑定行；不引入额外约束（同 app 并发部署的孤立容器风险见 §9）。

### 4.3 model.go 改动

`ServiceInstance` 加字段，`instCols` 加列（沿用 COALESCE 防 NULL）：

```go
type ServiceInstance struct {
    // ... 既有字段 ...
    ContainerName string `json:"container_name,omitempty" db:"container_name"` // dedicated 容器名（Cleanup docker rm 用）
    // ...
}

const instCols = `id, project_space_id, kind, name, supply_mode, host, port,
 COALESCE(auth_ref,'') AS auth_ref, COALESCE(isolation::text,'') AS isolation,
 COALESCE(container_name,'') AS container_name, status, created_at, updated_at`
```

---

## 5. 供给流程 supplyDedicated

### 5.1 supplyOne 加 dedicated 分支

`supply.go` 的 `supplyOne` 在 shared 分支后加：

```go
if strategy == ModeDedicated {
    r.supplyDedicated(ctx, appID, psID, dep, mkBind)
    return
}
```

（之后的 `if strategy != ModeBindExisting` 兜底文案改为「仅 bind_existing/shared/dedicated」。）

### 5.2 supplyDedicated 伪码

```
supplyDedicated(dep{kind:redis, strategy:dedicated}):
  // —— 复用判定（幂等：同 app 重部署不重启、不换端口、保数据）——
  if b = store.GetBinding(appID, kind); b != nil
     && b.status==bound && b.service_instance_id!="":
      if inst = store.GetInstance(b.service_instance_id); inst != nil && inst.status==active:
          writeDedicatedEnv(appID, inst)
          mkBind(bound, inst.ID, "", "")
          return

  // —— 新供给 ——
  // 1. 端口分配（最小未占用号）
  port = allocPort(docker.UsedPorts(ctx), mwPortMin, mwPortMax)   // 9600-9699
  if port == 0:
      mkBind(failed, "", "", fmt.Sprintf("redis 端口池 %d-%d 已满", mwPortMin, mwPortMax)); return

  // 2. 起容器
  short = genShortID()
  name  = "mwredis-" + short
  pwd   = genPassword()
  if err = docker.RunRedisContainer(ctx, name, pwd, port); err != nil:
      mkBind(failed, "", "", "起 redis 容器: "+err); return

  // 3. 就绪检测（AUTH+PING 轮询；失败清半成品容器）
  if err = waitForRedisReady(ctx, host, port, pwd); err != nil:
      docker.RmForce(ctx, name)
      mkBind(failed, "", "", "redis 未就绪: "+err); return

  // 4. 登记实例行（容器单一事实源）
  inst = &ServiceInstance{
      ID: "svinst-redis-ded-" + short, ProjectSpaceID: psID, Kind: kind,
      Name: name, SupplyMode: ModeDedicated, Host: host, Port: port,
      AuthRef: pwd, ContainerName: name, Status: "active",
  }
  if err = store.CreateInstance(ctx, inst); err != nil:
      docker.RmForce(ctx, name)               // 登记失败回收容器
      mkBind(failed, "", "", "登记实例: "+err); return

  // 5. 注入 env + binding
  writeDedicatedEnv(appID, inst)
  mkBind(bound, inst.ID, "", "")
```

### 5.3 状态机（binding.status）

```
（无 binding）──新供给──▶ bound        // 起容器+就绪+登记+env 全成后落 bound
bound ──重部署──▶ bound（复用容器，不重启/不换端口/保数据）   // 幂等
（任意）──端口池满/起容器失败/未就绪/登记失败──▶ failed（不建实例行、不写 env）
failed ──重部署──▶ 重新走「新供给」      // 失败 binding 无 service_instance_id，重试
```

无 `declared`/`allocating` 中间态（同 P2 Model B：成功直接 bound，失败直接 failed）。

### 5.4 writeDedicatedEnv

```go
func (r *Reconciler) writeDedicatedEnv(ctx, appID string, inst *ServiceInstance) {
    _ = r.env.UpsertEnv(ctx, appID, "REDIS_ADDR", ConnStr(inst), false, "platform")     // host:port
    _ = r.env.UpsertEnv(ctx, appID, "REDIS_PASSWORD", inst.AuthRef, true, "platform")   // secret
}
```

> **不注入 `REDIS_DB`**：dedicated 是整实例，应用用默认 db 0；`REDIS_DB` 是 shared 专属。`ConnStr` 仍返回 `host:port`（不变）。

---

## 6. 回收：Cleanup（与 P2 shared 的核心区别）

### 6.1 为什么 dedicated 必须显式 Cleanup

P2 shared 的 db 号是「逻辑号」，随 binding 行 `ON DELETE CASCADE` 即回收，Delete handler 零改。**dedicated 的 redis 容器是宿主资源**，CASCADE 只删 `appdeploy_service_instance`/`appdeploy_service_binding` 的 DB 行，**运行中的容器仍占端口/内存**（资源泄漏，类比 pgsupply I2）。故必须显式 `docker rm`。

### 6.2 MWReconciler 接口加 Cleanup

`appdeploy/handler.go`：

```go
type MWReconciler interface {
    Reconcile(ctx context.Context, appID, psID, repoDir string) error
    Cleanup(ctx context.Context, appID string) error   // P3：docker rm dedicated 容器
}
```

### 6.3 mwsupply.Reconciler.Cleanup 实现

```
Cleanup(ctx, appID):
  binds = store.ListBindingsByApp(ctx, appID)
  for b in binds:
      if b.strategy != ModeDedicated: continue        // bind_existing/shared 靠 CASCADE，不动
      if b.service_instance_id == "": continue
      inst = store.GetInstance(ctx, b.service_instance_id)
      if inst != nil && inst.container_name != "":
          _ = docker.RmForce(ctx, inst.container_name) // best-effort（失败不阻塞删 app）
          _ = store.DeleteInstance(ctx, inst.ID)       // 删实例行（binding/env CASCADE 兜底）
  return nil   // 总不返错（best-effort，不阻塞 Delete）
```

> **只动 dedicated**：bind_existing（指向平台/项目既有服务，不归平台删）/ shared（db 号靠 CASCADE）的 binding 跳过。
>
> **删 instance 行**：instance 无 app FK，不会被 CASCADE 自动删，必须显式 `DeleteInstance`，否则孤儿行。

### 6.4 Delete handler 接入

`appdeploy/handler.go` Delete（现状 1862 行 `h.provisioner.Cleanup(...)` 之后）插：

```go
if h.provisioner != nil {
    _ = h.provisioner.Cleanup(c.Request.Context(), a.ID)
}
// P3：回收 dedicated 中间件容器（best-effort，不阻塞）
if h.mwReconciler != nil {
    _ = h.mwReconciler.Cleanup(c.Request.Context(), a.ID)
}
```

**必须在 `h.store.Delete`（CASCADE 删 binding）之前**——插入点 1864 满足（store.Delete 在更后）。Cleanup 此时仍能读到 binding→instance→container_name。

---

## 7. 网络与可达性（关键决策详解）

### 7.1 容器启动命令

```
docker run -d --name mwredis-<short> \
  -p <port>:6379 \
  --restart unless-stopped \
  redis:7-alpine \
  redis-server --requirepass <pwd>
```

- `-p <port>:6379`：把容器 6379 publish 到宿主 `<port>`（9600-9699），绑 `0.0.0.0:<port>`
- `--restart unless-stopped`：宿主重启自恢复
- `redis-server --requirepass <pwd>`：设密码

### 7.2 REDIS_ADDR 注入与可达性

- `REDIS_ADDR = AppDeployHost:port`（如 `10.10.0.28:9631`），与 bind_existing/shared **同范式**
- app 容器（默认 bridge 网络）走 host LAN IP:port 可达 ✅ —— P2 e2e 已验证 app 容器内 `REDIS_ADDR=10.10.0.28:6381` 可用
- `AppDeployHost` 经 `NewReconciler` 注入（main.go 传 `cfg.AppDeployHost`，同 pgsupply InstanceManager）

### 7.3 为什么 dedicated 避开了 P2 的 .28 坑

P2 的坑：flusher 在 `deploy_backend_1`（`deploy_default` 网）内拨 redis host LAN IP **超时**（redis 在 `yxt-infra_default` 网，bridge 无 hairpin NAT）→ flush 降级 best-effort。

**dedicated 无 flush 步骤**：专属容器是全新空的，没有「清前任租户残留」需求。就绪检测的 AUTH+PING 也在供给时（backend 进程内）执行——若 backend 同样拨不到，就绪检测失败 → binding failed（不会泄漏半成品，§5.2 步骤 3 已 `RmForce` 清容器）。即：dedicated 把「不可达」从 P2 的「卫生降级」变成 P3 的「供给失败直接反馈」，语义更干净。

> .28 上 backend 能否拨到 dedicated 容器的 host:port：dedicated 容器由 backend 进程 `docker run -p` 起在宿主，publish 到 `0.0.0.0:port`，backend 拨 `AppDeployHost:port` 走宿主回环/LAN——与 app 容器拨同一地址。若 .28 拓扑下 backend 同样拨不到（同 P2 flusher 坑），则 dedicated 供给会 failed。**e2e 必须先验证 backend↔dedicated 容器可达**（§10 风险）。若不可达，备选：dedicated 容器加入 `deploy_default` 网用容器名直连（§9 备选，本期不做）。

---

## 8. 模块 / 文件改动

| 动作     | 文件                                                                | 说明                                                                                                                                                                      |
| -------- | ------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 改       | `internal/mwsupply/supply.go`                                       | `Reconciler` +`docker MWDockerRunner`/`host string` 字段；`NewReconciler` 加参；`supplyOne` 加 `case ModeDedicated`；新增 `supplyDedicated`/`writeDedicatedEnv`/`Cleanup` |
| 新增     | `internal/mwsupply/docker.go`                                       | `MWDockerRunner` 接口（`UsedPorts`/`RunRedisContainer`/`RmForce`）+ osDocker 实现 + `NewOSDocker()`                                                                       |
| 新增     | `internal/mwsupply/naming.go`                                       | `dedicatedContainerName` + `genShortID`/`genPassword`/`allocPort`（自包含副本，pgsupply 的不导出）+ `mwPortMin`/`mwPortMax`/`redisImage`/`redisInternalPort` 常量         |
| 改       | `internal/mwsupply/store.go`                                        | +`CreateInstance`/`GetInstance(id)`/`DeleteInstance`                                                                                                                      |
| 改       | `internal/mwsupply/model.go`                                        | `ServiceInstance`+`ContainerName` 字段；`instCols`+`container_name`；端口/镜像常量（或放 naming.go）                                                                      |
| 新增迁移 | `internal/db/migrations/pg/000030_mwsupply_dedicated.{up,down}.sql` | ADD COLUMN container_name                                                                                                                                                 |
| 改       | `cmd/server/main.go`（`NewReconciler` 调用处，约 `main.go:185`）    | 多传 `mwsupply.NewOSDocker()` + `cfg.AppDeployHost`                                                                                                                       |
| 改       | `internal/appdeploy/handler.go`                                     | `MWReconciler` 接口 +`Cleanup`；Delete 调 `h.mwReconciler.Cleanup`                                                                                                        |
| 改测试   | `internal/appdeploy/handler_http_test.go`                           | `MWReconciler` mock 加 `Cleanup` 方法（接口实现）                                                                                                                         |
| 新增测试 | `internal/mwsupply/{docker,supply,store}_test.go`                   | PG 单测（§11）                                                                                                                                                            |
| **不改** | `internal/mwsupply/connstr.go`                                      | `ConnStr` 仍 `host:port`                                                                                                                                                  |
| 小改     | `internal/mwsupply/redisflush.go`                                   | 抽共享 RESP helper（`dialRedis`/`writeCmd`/`readOK`），`flushConn` 与就绪检测的 `pingConn` 共用，**语义不变**（flush 行为零回归）。shared flush 仍走 `flushConn`          |
| **不改** | shared/bind_existing 分支、`Deployer`/`EnvPairs` 主流程、pgsupply   |                                                                                                                                                                           |

> main.go 改 1 行（构造多传两参），handler.go 改接口+1 处调用。

---

## 9. docker.go / naming.go 关键实现

### 9.1 MWDockerRunner 接口（仿 pgsupply.DockerRunner）

```go
// MWDockerRunner 经宿主 docker socket 管理 dedicated 中间件容器。抽接口便于单测用 fake。
type MWDockerRunner interface {
    UsedPorts(ctx context.Context) map[int]struct{}
    RunRedisContainer(ctx context.Context, name, password string, port int) error
    RmForce(ctx context.Context, name string) error
}
```

osDocker 实现调 `docker` CLI（同 pgsupply `runDocker` helper）：

- `UsedPorts`：`docker ps --format {{.Ports}}` + 正则提宿主端口（同 pgsupply）
- `RunRedisContainer`：`docker run -d --name <name> -p <port>:6379 --restart unless-stopped redis:7-alpine redis-server --requirepass <pwd>`
- `RmForce`：`docker rm -f <name>`

### 9.2 naming.go（自包含副本）

`genShortID`（12 hex）/`genPassword`（32 hex）/`allocPort(used,min,max)` 与 pgsupply 同款纯函数，在 mwsupply 包内副本（pgsupply 的不导出，跨包 import 不值）。`dedicatedContainerName(short) = "mwredis-" + short`。

### 9.3 就绪检测 waitForRedisReady

把 `redisflush.go` 现有 RESP 写读逻辑抽成共享 helper（`dialRedis(ctx, host, port, password) (conn, error)` 做 dial+AUTH、`writeCmd`/`readOK` 维持不变），`flushConn`（shared）与新增的 `pingConn`（就绪检测）共用。`pingConn` 发 `PING` 读 `+PONG`。`waitForRedisReady` 用 ticker 轮询至成功或 ctx 超时（仿 pgsupply `waitForReady`）。**抽 helper 不改 flush 语义**（shared 零回归）。失败由 `supplyDedicated` 步骤 3 捕获 → `RmForce` 清半成品。

---

## 10. 测试计划

### 10.1 PG 单测（`go test -p 1`，跑 `anp_test` 库）

> 遵循记忆 `sqlite-test-pg-type-trap` / `go-test-serial-p1`：用真 PG（不 sqlite），全量回归 `-p 1` 串行；`GOPATH=C:/Users/yxt/go` 前缀。

**store_test.go**：

1. 迁移 000030：`container_name` 列存在
2. `CreateInstance` 落 dedicated 实例行（含 container_name）；`GetInstance(id)` 取回；`DeleteInstance` 删
3. dedicated 实例行 `supply_mode='dedicated'`、`container_name` 非空

**supply_test.go**（fake `MWDockerRunner` 记调用）：

4. dedicated 新供给：`.anp/deps.yaml` `strategy:dedicated` → binding=bound + `REDIS_ADDR=host:port` + `REDIS_PASSWORD` 写入（secret）+ `RunRedisContainer` 被调一次（记 name/port）+ instance 行落库（container_name 匹配）
5. 复用幂等：同 app 二次 `Reconcile` → `RunRedisContainer` **不**再被调、port 不变、env 重写、binding 仍 bound
6. 端口池满：fake `UsedPorts` 占满 9600-9699 → binding=failed + `last_error` 含「端口池」+ 不写 env + 不起容器
7. 起容器失败：fake `RunRedisContainer` 返错 → binding=failed
8. 就绪失败：fake 返就绪错 → binding=failed（若已起容器则 `RmForce` 被调清半成品）
9. `Cleanup`：app 有 dedicated binding → `RmForce` 被调（按 container_name）+ instance 行删除；同 app 的 shared/bind_existing binding **不**被 `RmForce`

**handler_http_test.go**：

10. `MWReconciler` mock 加 `Cleanup` 方法（接口实现）；`TestHandler_SetMwReconciler` 仍编译通过

### 10.2 `.28` 端到端（`deploy-28-no-local-test`）

> 本机不跑功能测试，`.28` 是测试库。commit → push origin main → scp + `.28` 重建。

1. **先验镜像 + 可达性**（§7.3 风险）：.28 上 `redis:7-alpine` 是否在（`docker images`）；backend 拨 dedicated 容器 host:port 是否通
2. 造最小 python 应用（`.anp/deps.yaml` 预写 `services:[{kind:redis, strategy:dedicated}]`；仿 P2 e2e 用 python:3-alpine）→ CREATE（带 repo_dir，不触发 adapt）→ deploy test
3. 容器内验证：`REDIS_ADDR=10.10.0.28:<port>` + `REDIS_PASSWORD=<pwd>` 在；`docker ps` 见 `mwredis-<short>` 容器（端口映射对）
4. app 能 SET/GET 自己的 redis（隔离验证：写入后读回）
5. **隔离**：两个 dedicated app → 两个 `mwredis-` 容器、两个端口、各自独立数据
6. **回收**：删 app → `docker ps` 不再见其 `mwredis-` 容器（`docker rm` 成功）+ `appdeploy_service_instance`/`binding`/`env` 行清
7. **重部署复用**：同 app 重 deploy → 同一 `mwredis-` 容器（不新建）+ 先前写入的 redis 数据仍在
8. **平台保护**：手改 `REDIS_PASSWORD` 返 409（复用 source=platform 保护）

---

## 11. 风险与取舍

| 风险                                                            | 对策                                                                                                                                                                                                                    |
| --------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| .28 无 `redis:7-alpine` 镜像缓存（类 P2 golang 坑）             | e2e step 1 先验；缺则 `docker pull`（需 .28 联网）或预载镜像                                                                                                                                                            |
| .28 backend 拨不到 dedicated 容器 host:port（同 P2 flusher 坑） | **e2e 实测确认（§14）**：backend(deploy_default) 拨 host 发布端口超时，但 app(默认 bridge) 能到（PONG）。**对策：Ping 降级 best-effort**（失败记 Warn 继续 bound，容器保留、app 经 host LAN IP 用），同 P2 flush 范式。 |
| 每 app 一容器资源成本（内存/CPU）                               | 端口池即配额（100 槽）；shared 是降本选项；dedicated 是强隔离默认                                                                                                                                                       |
| 同 app 并发部署起两容器（罕见 race）                            | binding `(app_id,service_kind)` 唯一兜底绑定行；两 goroutine 都过「复用判定」查无则各起一容器，孤立容器为轻微泄漏（记风险，不做 partial unique）                                                                        |
| strategy 由 shared 切 dedicated 残留 `REDIS_DB` env 行          | 边缘场景（strategy 一般不变）；`writeDedicatedEnv` 可选清 `REDIS_DB`（YAGNI 记债）                                                                                                                                      |
| `auth_ref` 明文（I1 债）                                        | 沿用 P1/P2 模式；阶段 3 接 vault/KMS（同 pgsupply）                                                                                                                                                                     |
| `container_name` 列迁移对已有种子行影响                         | `ADD COLUMN ... TEXT` nullable，既有行 NULL，无影响                                                                                                                                                                     |

---

## 12. 未覆盖（YAGNI / 后续）

- **milvus dedicated**：standalone 容器重（etcd/minio 依赖、启动慢），独立设计
- **项目级 dedicated 池**：同项目多 app 共享一个 dedicated 实例（当前严格 1:1）
- **dedicated 容器加入 app 同网络**（容器名直连，免 host:port publish）
- **监控/备份/迁移**：dedicated redis 的指标采集与备份
- **vault/KMS 密钥管理**
- **端口池运行时配置**（UI 调范围/镜像）——本期常量固定

---

## 13. 验收标准

1. **迁移**：000030 apply 后 `appdeploy_service_instance` 有 `container_name` 列
2. **供给**：dedicated app 部署后容器内 `REDIS_ADDR`（AppDeployHost:port）+ `REDIS_PASSWORD` 在；`docker ps` 见 `mwredis-<short>` 容器（端口/密码对）；`appdeploy_service_instance` 一行 `supply_mode=dedicated, container_name` 非空；binding `strategy=dedicated, status=bound`
3. **隔离**：两个 dedicated app = 两个容器、两个端口、独立数据
4. **回收**：删 app → `mwredis-` 容器 `docker rm` 消失 + instance/binding/env 行清
5. **幂等**：同 app 重部署复用容器（不新建）、redis 数据留存
6. **配额**：端口池满（>100）新 app `binding=failed`，`last_error` 含「端口池」
7. **零回归**：P1 bind_existing / P2 shared 链路、`DATABASE_URL` 注入、部署主流程不受影响；Delete 仍正常（pgsupply.Cleanup + mwReconciler.Cleanup 都跑）
8. **平台保护**：手改 `REDIS_PASSWORD` 返 409（source=platform）

---

## 14. e2e 修订（2026-08-02）：Ping 降级 best-effort

**实测**（部署前只读预检，起临时 redis `-p 9699:6379`）：

- `deploy_backend_1`（`deploy_default` 网）→ `10.10.0.28:9699`：**拨不通**（wget error getting response）。印证 cross-network 教训（bridge 无 hairpin，容器→host LAN IP 不通）。
- 默认 bridge 容器（模拟 app）→ `10.10.0.28:9699`：**PONG** ✓（app 能用）。

**含义**：原设计「Ping 失败 → binding=failed」会让 dedicated 供给在 .28 上直接 failed——但 app 本能用到（PONG），是 P2 flush 同一个坑的形状（backend 拨不到、app 拨得到）。

**决策（用户拍板，同 P2 flush 范式）**：Ping **降级 best-effort**。

- `supplyDedicated` 就绪检测失败 → 记 Warn（经 `SetLogger` 注入的 zap）后**继续 claim→bound**，**不 RmForce**（容器保留，app 经 host LAN IP:port 使用）。
- 短超时 `readyPingTimeout=5s`（不可达时快速放行，避免干等；可达时秒级成功）。
- 依据：dedicated 是全新空容器、redis 秒级起；app 部署+连上的时序里 redis 早已就绪。Ping 失去的是「redis 启动失败」边缘检测（罕见，且 `RunRedisContainer` 已兜底 `docker run` 失败），换来 .28 可用性。

**单测**：`TestReconcile_dedicated_readyFail_bestEffort`——ping 返错 → binding 仍 `bound`、不 RmForce、`REDIS_ADDR` 已写。

**备选（不做）**：dedicated 容器 `--network deploy_default` + publish，backend 用容器名 ping——但 Ping 地址(容器名)与 `REDIS_ADDR`(host IP) 模型不一致，改动更大，留后续。

---

_本设计把 mwsupply 范式从 bind_existing/shared 推进到 dedicated（每 app 专属 redis 容器）：迁移加 container_name 列 + supplyDedicated（端口分配→起容器→就绪(best-effort)→登记→双 env）+ MWReconciler.Cleanup（docker rm + 先删 binding 解 FK 再删 instance）+ Delete handler 接入。完全对称 pgsupply，且 Ping best-effort（§14，e2e 驱动）让 dedicated 在 .28 真正可用。milvus dedicated 留后续。_

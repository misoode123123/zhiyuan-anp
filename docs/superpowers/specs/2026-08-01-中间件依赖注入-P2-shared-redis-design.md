# 中间件依赖注入 P2 设计 —— shared 共享 Redis（db 号隔离）

- **类型**：方案 / 详细设计（P1 设计 §10 分期里的 P2 阶段）
- **状态**：待审核（按「方案先成文审核」，审核通过后再开 plan → 实现）
- **日期**：2026-08-01
- **作者**：miscode + Claude
- **关联模块**：`mwsupply`（扩展）、`appdeploy`（不改主流程）
- **关联文档**：
  - [中间件依赖供给与注入 设计（P1 总设计）](2026-08-01-中间件依赖供给与注入-design.md) —— 本文件是其 §10「P2：shared 共享实例」的细化
  - [多形态应用治理 PRD §4.4/§7/§8/§9](../../PRD/2026-08-01-多形态应用治理与开发运维统筹-PRD.md)
  - [应用库与 API 统一管理设计](2026-07-21-应用库与API统一管理-设计.md)（pgsupply 范式来源）

---

## 1. 背景与目标

### 1.1 P1 已闭环

P1（bind_existing）已落地并 `.28` live 验证（2026-08-01，10 commit 已 push）：

- `internal/mwsupply/`（`model/store/supply/manifest/connstr/norows`）扩 pgsupply 范式
- 仓库根 `.anp/deps.yaml`（opencode 适配回写）声明依赖 → `buildAndDeploy` 在 `EnvPairs()` 读表前调 `mwsupply.Reconcile` → 按 `bind_existing` 查 `appdeploy_service_instance` 注册表 → 经 `EnvWriter.UpsertEnv(source=platform)` 写 `appdeploy_env` → 现有 `docker run -e` 注入
- `.28` e2e：容器内 `REDIS_ADDR=10.10.0.28:6381` / `MILVUS_ADDR=10.10.0.28:19530` / `DATABASE_URL` 全在，`appdeploy_env` 三行 `source=platform`，平台保护 409 生效

### 1.2 P2 要补的缺口

P1 的 `supply.go:52` 对一切非 `bind_existing` 策略一刀切标 `failed`：

```go
if strategy != ModeBindExisting {
    mkBind(StatusFailed, "", "策略 "+strategy+" 暂未实现（P1 仅 bind_existing）")
    return
}
```

P1 迁移 000028 **已建好**但**从未被写**的 shared 相关字段：

- `appdeploy_service_instance.supply_mode='shared'`、`appdeploy_service_instance.isolation JSONB`（存 `{"db_range":[1,15]}`）
- `appdeploy_service_binding.isolation_token`（存分配到的 redis db 号）

即：**数据模型就位、缺的是分配/注入/回收逻辑**。本设计填这块，且**仅做 redis**（理由见 §2）。

### 1.3 目标

应用在 `.anp/deps.yaml` 声明 `strategy: shared` 的 redis 依赖 → 平台从共享 redis 实例的 `db_range` 里给它分配一个**独占 db 号** → 注入 `REDIS_ADDR` + `REDIS_DB=N` → 多个 app 共用一台 redis 但互不可见；删 app 自动回收 db 号；重分配时清空残留数据。

---

## 2. 范围

### 2.1 本期做（in）

- redis shared 全链路：实例注册（种子）+ db 号分配 + 双 env 注入 + 重分配 flush + 删 app 回收
- 把 `LookupShared / AllocateToken / flush` 骨架做**通用**（不写死 redis 专属到不可复用），milvus shared 作为小步后续可接

### 2.2 本期不做（out / YAGNI）

- **milvus shared**：milvus 隔离要么 collection 前缀（要求应用配合加前缀）要么 2.3+ 原生 database/RBAC（需验证 `.28` milvus 版本 + 管 user 生命周期），是更大的设计，留后续
- dedicated（P3）：每 app 专属容器 + `ProvisionDedicated` + 删 app `Cleanup`
- 项目级 shared 池（本期 shared 实例=平台级 `project_space_id IS NULL`；未来要按项目空间分池再扩 `LookupShared` 的 scope）
- vault/KMS 密钥管理（沿用 P1 明文 `auth_ref` 模式，阶段 3）
- stale `allocating` 清扫器（见 §9 风险，本期 Model B 无此状态）

---

## 3. 关键决策

| 维度       | 选择                                                                                 | 理由                                                                                                                                           |
| ---------- | ------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| 隔离机制   | **redis db 号**（共享实例 + 每 app 独占一个 db）                                     | 离散、干净、redis 原生 16 库；应用 redis 客户端连时 `SELECT N` 即隔离，零代码配合                                                              |
| db 号池    | **`db_range:[1,15]`**，保留 db 0                                                     | redis 默认 0-15 共 16 库；db 0 留给 bind_existing/系统，shared 用 1-15（15 槽）                                                                |
| 配额       | **`db_range` 本身即配额**，**不动 `internal/quota` 模块**                            | shared 实例是平台级、token 天然实例作用域；池满=配额超限，自然拒绝。塞进项目级 `project_quota` 反而别扭，且免循环依赖                          |
| 分配算法   | **最小空闲号**（`db_range` 内未被任何 `isolation_token IS NOT NULL` 占用的最小号）   | 紧凑占用、回收后立即可复用最小号                                                                                                               |
| 并发控制   | **乐观选号 + 部分唯一索引兜底 + 有界重试**（非 `SELECT FOR UPDATE`）                 | flush 幂等（`FLUSHDB`），并发撞号由 `UNIQUE(service_instance_id,isolation_token)` 检测、重试即可；免长事务、免 `allocating` 中间态、免崩溃泄漏 |
| 数据卫生   | **（重）分配时 `FLUSHDB`**（方案 A）                                                 | CASCADE 只回收「号」不清「残留数据」；重分配时 flush 保证新租户拿到干净 db，覆盖崩溃/手删所有场景                                              |
| flush 实现 | **`redisflush.go`：`net.Dial` + 裸 RESP**（不引 go-redis/redigo）                    | 后端 go.mod **无任何 redis 客户端依赖**；`FLUSHDB` 是 3 条命令的裸 RESP，~40 行搞定，不值得为此引重依赖。经 `DBFlusher` 接口注入便于测试       |
| 回收       | **删 app 靠 `ON DELETE CASCADE` 自动回收**，**Delete handler 零改**                  | token 占用集合=存活 binding 行；删 app → binding 行 CASCADE 删 → db 号自动回池；env 行同样 CASCADE 清。无需像 pgsupply.Cleanup 加显式钩子      |
| env 注入   | **`REDIS_ADDR` + `REDIS_DB`（+ `REDIS_PASSWORD` 若鉴权）**，三行均 `source=platform` | 与 bind_existing 同 `REDIS_ADDR` key（应用读法不变），`REDIS_DB` 是同范式小扩展；匹配 AGENTS.md env-over-config                                |

---

## 4. 数据模型（复用 000028 已有列 + 000029 补种子/约束）

### 4.1 复用，不改表结构

000028 已建：

```sql
appdeploy_service_instance.isolation JSONB        -- shared 实例存 {"db_range":[1,15]}
appdeploy_service_instance.supply_mode TEXT       -- 'shared'
appdeploy_service_binding.isolation_token TEXT    -- 分配到的 db 号（"7"）
```

P2 只是**第一次写**这些列，不改 DDL。

### 4.2 新增迁移 `000029_mwsupply_shared.up.sql` / `.down.sql`

```sql
-- 000029_mwsupply_shared.up.sql
-- P2：shared 共享 redis（db 号隔离）
-- ① 种子一条平台级 shared redis 实例（复用 .28 同一台 yxt-redis，db 1-15 隔离，db 0 留给 bind_existing/系统）
-- ② 部分唯一索引：防并发分配撞 db 号（兜底，主路径靠乐观选号 + 重试）

INSERT INTO appdeploy_service_instance
  (id, project_space_id, kind, name, supply_mode, host, port, auth_ref, isolation, status)
VALUES
  ('svinst-redis-shared-28', NULL, 'redis', 'yxt-redis-shared', 'shared',
   '10.10.0.28', 6381, NULL, '{"db_range":[1,15]}'::jsonb, 'active')
ON CONFLICT (id) DO NOTHING;

-- 仅对「已分配 token」的 binding 建（NULL 不入索引，多 NULL 不冲突；bind_existing binding token 恒 NULL 不受影响）
CREATE UNIQUE INDEX IF NOT EXISTS uq_svbind_inst_token
  ON appdeploy_service_binding (service_instance_id, isolation_token)
  WHERE isolation_token IS NOT NULL;
```

```sql
-- 000029_mwsupply_shared.down.sql
DROP INDEX IF EXISTS uq_svbind_inst_token;
DELETE FROM appdeploy_service_instance WHERE id = 'svinst-redis-shared-28';
```

> **`auth_ref=NULL`**：000028 种子 `svinst-redis-28` 的 `auth_ref` 即 NULL，证实 `.28` yxt-redis 无密码；shared 种子同理。flusher 跳过 `AUTH`。若后续 redis 加密码，更新种子 `auth_ref` + flusher 自动走 `AUTH`。

> **版本号 000029**：已确认 `migrations/pg/` 当前最大 000028，000029 空闲。

---

## 5. Token 分配算法（核心）

采用**乐观模型（Model B）**：先读占用集合选号 → flush → 原子 claim；唯一索引防撞号，有界重试解冲突。**无 `allocating` 中间态、无长事务、崩溃不泄漏**（claim 只在 flush 成功后写，直接落 `bound`）。

### 5.1 supplyOne 的 shared 分支伪代码

```
supplyOne(dep{kind:redis, strategy:shared}):
  inst = store.LookupShared(kind)                         // 平台级 supply_mode='shared' 实例
  if inst == nil:
      mkBind(failed, "", "无 shared redis 实例"); return

  // —— 复用判定（幂等：同 app 重部署不换号、不 flush、保数据）——
  existing = store.GetBinding(appID, kind)
  if existing != nil && existing.status==bound
     && existing.isolation_token != "" && existing.service_instance_id==inst.ID:
      token = existing.isolation_token                    // 复用，跳过 flush/claim
  else:
      // —— 新分配 ——
      lo, hi = ParseDBRange(inst.isolation)               // [1,15]
      allocated = store.AllocatedTokens(inst.ID)          // 该实例所有 token IS NOT NULL 的集合
      token, ok = pickLowestFree(lo, hi, allocated)
      if !ok:
          mkBind(failed, inst.ID, "shared redis db 号耗尽（池 %d-%d）", lo, hi); return
      // 数据卫生：分配时清空（仅新分配时；复用不 flush，保数据）
      if err = flusher.FlushDB(inst.host, inst.port, inst.auth_ref, atoi(token)); err != nil:
          mkBind(failed, inst.ID, "flush db "+token+" 失败: "+err); return   // token 未 claim，仍空闲
      // 原子 claim（唯一索引兜底撞号）
      err = store.ClaimSharedToken(appID, psID, inst.ID, token, "REDIS_ADDR")
      if isUniqueViolation(err):
          // 罕见并发撞号：刷新占用集，挑下一个空闲号，重试（有界 ≤ 池大小）
          ... retry loop ...
          if 仍撞: mkBind(failed, inst.ID, "并发分配冲突，重试用尽"); return
      else if err:
          mkBind(failed, inst.ID, err); return

  // —— 注入 env（复用/新分配都重写，幂等）——
  env.UpsertEnv(appID, "REDIS_ADDR", host:port, false, "platform")
  env.UpsertEnv(appID, "REDIS_DB",   token,     false, "platform")
  if inst.auth_ref != "":
      env.UpsertEnv(appID, "REDIS_PASSWORD", inst.auth_ref, true, "platform")
  mkBind(bound, inst.ID, "")     // claim 已落 bound；此调用刷新 updated_at/normalize（ON CONFLICT 幂等）
```

### 5.2 状态机（binding.status）

```
（无 binding）──新分配──▶ bound          // claim 直接落 bound（flush 成功后才 claim）
bound ──重部署──▶ bound（复用号，不 flush）  // 幂等
（任意）──flush 失败/池满/撞号重试用尽──▶ failed（token 不 claim / 留空）
failed ──重部署──▶ 重新走「新分配」        // 失败 binding 的 token 恒为空，重试可复用同号
```

无 `declared`/`allocating` 中间态消费（P1 已把 declare 钩子简化掉，清单 deploy 时直读）。

### 5.3 `pickLowestFree`

```
pickLowestFree(lo, hi, allocatedSet):
  for n in lo..hi:
      if str(n) not in allocatedSet: return str(n), true
  return "", false   // 池满
```

---

## 6. Env 注入

| env key          | 值                    | source   | is_secret | 说明                                     |
| ---------------- | --------------------- | -------- | --------- | ---------------------------------------- |
| `REDIS_ADDR`     | `10.10.0.28:6381`     | platform | false     | 与 bind_existing 同 key，应用读法不变    |
| `REDIS_DB`       | `7`（分配到的 db 号） | platform | false     | 应用 redis 客户端 `SELECT` 该 db         |
| `REDIS_PASSWORD` | `auth_ref`            | platform | true      | 仅 `auth_ref` 非空时注入（`.28` 当前无） |

应用侧（已由 opencode 适配成 env-over-config）读 `REDIS_ADDR` + `REDIS_DB` 连 redis 即得隔离。`EnvPairs()`（`store.go:181`）读全表拼 `KEY=VALUE` 不变。

---

## 7. 数据卫生：flush（`redisflush.go`）

### 7.1 `DBFlusher` 接口（注入，便于测试）

```go
// DBFlusher 清空指定 redis db（shared 重分配时保证干净隔离位）。
// 由 supply.go 经 NewReconciler 注入；测试传 fake。
type DBFlusher interface {
    FlushDB(ctx context.Context, host string, port int, password string, db int) error
}
```

### 7.2 裸 RESP 实现（~40 行，不引依赖）

`redisflush.go` 用 `net.Dial` + 手写 RESP：

1. `Dial("tcp", host:port)`（带 `context` 超时）
2. 若 `password != ""`：发 `AUTH <password>` → 读 `+OK`
3. 发 `SELECT <db>` → 读 `+OK`
4. 发 `FLUSHDB` → 读 `+OK`
5. 关连接

RESP 写：`*N\r\n$len\r\nCMD\r\n...`；读：按首字节判 `+`（simple string OK）/`-`（error）/`$`（bulk，本文不涉及）。仅需处理 simple-string/error 两种回包。

> `FLUSHDB` **幂等**：并发场景下两个分配器同时对同号 flush 无害（结果都是空 db）。这是乐观模型敢「先 flush 后 claim」的依据。

---

## 8. 回收：CASCADE 自动回收，Delete handler 零改

**关键洞察**：token 的「占用集合」完全由 `appdeploy_service_binding` 的存活行派生（`AllocatedTokens` 查的就是非空 `isolation_token` 的行）。删 app 时：

- `appdeploy_service_binding` 行靠 `app_id REFERENCES appdeploy_application(id) ON DELETE CASCADE`（000028 `up.sql:24`）**自动删** → db 号自动回池
- `appdeploy_env` 的 `REDIS_ADDR`/`REDIS_DB`/`REDIS_PASSWORD` 行同样 CASCADE 自动清

**故 `handler.go` Delete（`handler.go:1853`）零改动**——P1 缺口「删 app 回收钩子」被 CASCADE 自然消解，不需要像 pgsupply.Cleanup 那样加显式钩子。

下一个 app 新分配时，`pickLowestFree` 会复用刚回收的最小号，且 §7 的 flush 保证它拿到干净 db。

> 与 pgsupply 对照：pgsupply 必须 `Cleanup`（要 `DROP DATABASE`/`DROP ROLE`，DB 对象不随行 CASCADE）；redis db 号是「逻辑号」，随 binding 行 CASCADE 即回收，flush 在分配侧补数据卫生即可。

---

## 9. 并发与失败处理

| 场景                        | 处理                                                                                                                                                                        |
| --------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 两 app 并发分配同号         | 都 `pickLowestFree` 得同号 T → 都 flush(T)（幂等无害）→ claim 时唯一索引 `uq_svbind_inst_token` 让其中一个 `23505` → 失败方刷新占用集、挑下一空闲号重试（有界 ≤ 池大小 15） |
| 池满（15 个 app 占满 1-15） | `pickLowestFree` 返回 `false` → `mkBind(failed, "db 号耗尽")`，不写 env                                                                                                     |
| flush 失败（redis 不可达）  | 不 claim → `mkBind(failed, "flush 失败")`，token 留空仍空闲；下次部署重试                                                                                                   |
| claim 失败（非唯一冲突）    | `mkBind(failed, err)`                                                                                                                                                       |
| 同 app 重部署               | `existing.status==bound && token!=""` → 复用号、**不 flush**（保数据）、重写 env（幂等）                                                                                    |
| 应用删 → 号回收             | CASCADE 自动；下次新分配复用 + flush                                                                                                                                        |

---

## 10. 模块 / 文件改动

| 动作     | 文件                                                             | 说明                                                                                                                                                                                                                                      |
| -------- | ---------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 扩展     | `internal/mwsupply/supply.go`                                    | `supplyOne` 加 `case ModeShared` 分支（§5.1）；`Reconciler` 加 `flusher DBFlusher` 字段                                                                                                                                                   |
| 扩展     | `internal/mwsupply/supply.go` `NewReconciler`                    | 签名加 `flusher DBFlusher` 参数                                                                                                                                                                                                           |
| 新增     | `internal/mwsupply/store.go`                                     | `LookupShared(ctx, kind)`、`GetBinding(ctx, appID, kind)`、`AllocatedTokens(ctx, instID)`、`ClaimSharedToken(ctx, appID, psID, instID, token, envKey)`（`INSERT ... ON CONFLICT(app_id,service_kind) DO UPDATE`，唯一索引冲突返 `23505`） |
| 新增     | `internal/mwsupply/redisflush.go`                                | `DBFlusher` 接口 + `redisFlusher{}` 裸 RESP 实现 + `NewRedisFlusher()`                                                                                                                                                                    |
| 新增     | `internal/mwsupply/isolation.go`（或并入 store.go）              | `ParseDBRange(isolationJSON string) (lo, hi int, ok bool)`                                                                                                                                                                                |
| 新增迁移 | `migrations/pg/000029_mwsupply_shared.{up,down}.sql`             | shared 实例种子 + 部分唯一索引（§4.2）                                                                                                                                                                                                    |
| 扩展     | `cmd/server/main.go`（`NewReconciler` 调用处，约 `main.go:184`） | 传 `mwsupply.NewRedisFlusher()` 作第三参数                                                                                                                                                                                                |
| 新增测试 | `internal/mwsupply/{store,supply,redisflush}_test.go`            | PG 单测（§11）                                                                                                                                                                                                                            |
| **不改** | `internal/mwsupply/connstr.go`                                   | `ConnStr` 仍返回 `host:port`（REDIS_ADDR 值）；REDIS_DB 是独立 env 行，不走 ConnStr                                                                                                                                                       |
| **不改** | `internal/mwsupply/model.go`                                     | 无新状态常量（Model B 直接 declared→bound/failed）                                                                                                                                                                                        |
| **不改** | `internal/appdeploy/handler.go`                                  | `MWReconciler` 接口签名不变；`buildAndDeploy` 调用点不变；Delete 不变（CASCADE 回收）                                                                                                                                                     |
| **不改** | `Deployer` / `EnvPairs` 主流程                                   | 表驱动                                                                                                                                                                                                                                    |

> main.go 改动仅 1 行（构造多传一个 flusher），`handler.go` 真正零改。

---

## 11. 测试计划

### 11.1 PG 单测（`go test -p 1`，跑 `anp_test` 库）

> 遵循记忆 `sqlite-test-pg-type-trap` / `go-test-serial-p1`：用真 PG（不 sqlite），全量回归 `-p 1` 串行。

**store_test.go（PG）**：

1. `LookupShared` 命中种子 `svinst-redis-shared-28`（isolation=`{"db_range":[1,15]}`）
2. `AllocatedTokens` 空池返空；占 2 个后返 `{"1","2"}`
3. `ClaimSharedToken` 新增 binding 落 `bound` + `token`；同 app 同 kind 再 claim 走 ON CONFLICT 更新（幂等）
4. **分配序列**：15 个 app 依次分配 → token `1..15`；第 16 个 `pickLowestFree` 返 false
5. **回收复用**：删 app（删其 binding）→ 占用集少一号 → 下次分配复用最小空闲号
6. **并发**：N goroutine 并发 `ClaimSharedToken` 各自 token → 唯一索引保证无重号（或冲突方重试后无重号）

**supply_test.go（fake flusher + 真 EnvWriter 或 mock）**：

7. shared 分支：`.anp/deps.yaml` 含 `strategy:shared` → binding=bound + `REDIS_ADDR`/`REDIS_DB` 两行 env（fake flusher 被调一次）
8. 复用：同 app 二次 `Reconcile` → token 不变、flusher **不**被调、env 重写
9. flush 失败：fake flusher 返错 → binding=failed、token 未 claim、env 未写
10. 池满：占满后新增 → binding=failed + `last_error` 含「耗尽」
11. 无 shared 实例（LookupShared nil）→ binding=failed

**redisflush_test.go**：

12. RESP 帧：用 `bytes.Buffer`/内存 fake conn 校验发出的字节序列（`AUTH`/`SELECT`/`FLUSHDB` 正确编码）；解析 `+OK`/`-ERR` 回包

### 11.2 `.28` 端到端（`deploy-28-no-local-test`）

> 本机不跑功能测试，`.28` 是测试库。commit → push origin main → scp + `.28` 重建。

1. 造两个最小 Go 应用，`.anp/deps.yaml` 预写 `services:[{kind:redis, strategy:shared}]`（golang:1.25-alpine 本地镜像，仿 P1 e2e）
2. 各自 CREATE（带 repo_dir，不触发 adapt）→ deploy test
3. 容器内验证：app1 `REDIS_DB=1`、app2 `REDIS_DB=2`（**隔离号不同**），`REDIS_ADDR` 同
4. `appdeploy_env` 各有 `REDIS_ADDR`+`REDIS_DB` 两行 `source=platform`；`appdeploy_service_binding` 各一行 `isolation_token=1/2, status=bound`
5. **回收**：删 app1 → 其 binding/env CASCADE 删 → 新建 app3 deploy → `REDIS_DB=1`（复用最小号）且 db 1 数据已 flush（app3 写 key 读不到 app1 残留）
6. 平台保护：手改 `REDIS_DB` 返 409（复用 P1 的 source=platform 保护）

---

## 12. 风险与取舍

| 风险                                      | 对策                                                                         |
| ----------------------------------------- | ---------------------------------------------------------------------------- |
| shared 多租户隔离（重分配残留数据）       | 分配时 `FLUSHDB`（方案 A）——本期已纳入                                       |
| 并发撞号                                  | 乐观选号 + 部分唯一索引 `uq_svbind_inst_token` + 有界重试                    |
| db 号池小（15）                           | 池满即 `failed`（配额自然表达）；dedicated/扩 `db_range` 是后续降本/扩容手段 |
| `auth_ref` 明文（I1 债）                  | 沿用 P1 模式；阶段 3 接 vault/KMS（同 pgsupply）                             |
| flush 不可达（redis 故障）                | `failed` 不阻塞部署（best-effort，同 P1）；下次部署重试                      |
| `.28` redis 无密码裸跑                    | 本期按现状（`auth_ref=NULL`，flusher 跳 AUTH）；生产强化时补                 |
| 乐观模型「先 flush 后 claim」的额外 flush | `FLUSHDB` 幂等，并发多 flush 一次无害                                        |

---

## 13. 未覆盖（YAGNI / 后续）

- **milvus shared**：collection 前缀 or 原生 database/RBAC，需单独设计（含应用配合规范）
- **dedicated（P3）**：每 app 专属 redis/milvus 容器 + 端口分配 + 删 app `Cleanup`
- 项目级 shared 池（按 project_space 分池）
- shared 实例的监控/备份/迁移
- vault/KMS 密钥管理
- `db_range` 的运行时配置（UI 调池大小/范围）——本期种子固定 `[1,15]`

---

## 14. 验收标准

1. **种子**：迁移后 `appdeploy_service_instance` 含 `svinst-redis-shared-28`（`supply_mode=shared`, `isolation={"db_range":[1,15]}`）；唯一索引 `uq_svbind_inst_token` 存在
2. **分配隔离**：两个 shared app 部署后容器内 `REDIS_DB` 不同（1/2），互不可见
3. **env 注入**：`appdeploy_env` 每app 有 `REDIS_ADDR`+`REDIS_DB` 两行 `source=platform`
4. **回收复用**：删 app 后其 db 号可被新 app 复用，且复用时数据已 flush
5. **幂等**：同 app 重部署 token 不变、不 flush、保数据
6. **配额**：池满（>15）新 app `binding=failed`，`last_error` 含「耗尽」
7. **零回归**：P1 的 bind_existing 链路、`DATABASE_URL` 注入、部署主流程不受影响；Delete 仍正常（CASCADE 回收）
8. **平台保护**：手改 `REDIS_DB` 返 409

---

_本设计把 P1 的 mwsupply 范式从 bind_existing 推进到 shared（redis db 号隔离）：复用 000028 已有列，新增「最小空闲号分配 + 重分配 flush + CASCADE 自动回收」三件套，配额即 db_range、不动 quota 模块，Delete handler 零改。milvus shared / dedicated 留 P3。审核通过后开 plan → TDD 实现 → `.28` e2e。_

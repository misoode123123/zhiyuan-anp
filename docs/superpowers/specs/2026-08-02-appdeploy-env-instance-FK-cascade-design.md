# appdeploy_env / appdeploy_instance 缺 FK CASCADE 修复 — 设计

- 日期：2026-08-02
- 类型：横切数据正确性修复（既有缺口，非新功能）
- 关联：[[appdeploy-env-no-cascade]] 记忆；P3 dedicated e2e（2026-08-02）发现；PRD `2026-08-01-多形态应用治理与开发运维统筹`
- 状态：✅ 已实现（commit `44ebbd7`，.28 e2e 通过 2026-08-02）

## 1. 背景与问题

删 app 时，`Store.Delete` 只执行 `DELETE FROM appdeploy_application WHERE id=? AND project_space_id=?`，**完全依赖子表的 `ON DELETE CASCADE` 兜底**清理子记录。

平台 app 维度的子表中，**有 FK CASCADE 的**（删 app 自动清）：

- `appdeploy_database`（000003）
- `appdeploy_route`（000006）
- `appdeploy_artifact`（000022）
- `appdeploy_service_binding`（000028）

**漏加 FK 的（删 app 后行变孤儿）**：

- `appdeploy_env`（000001:408）— `app_id TEXT NOT NULL` + `UNIQUE(app_id,key)` + `idx_appdeploy_env_app`，**无 FK**
- `appdeploy_instance`（000001:390）— `app_id TEXT NOT NULL` + `UNIQUE(app_id,env)` + `idx_appdeploy_instance_app`，**无 FK**

> `appdeploy_build_config`（按 `app_kind`）与 `appdeploy_service_instance`（按 `project_space_id`）是全局维度表，非 app 维度，无需 FK，不在范围内。

### 1.1 证据

`handler.Delete`（`platform/backend/internal/appdeploy/handler.go:1854`）全链路：

1. `provisioner.Cleanup` — pgsupply DropDatabase/Role
2. `mwReconciler.Cleanup` — 回收 dedicated 中间件容器（mwsupply P3）
3. `routeWriter.DeleteRouteByApp` — appgw 路由
4. `ListInstancesByApp` → `deployer.Remove(containerName)` — **删 docker 容器实体，但不删 `appdeploy_instance` DB 行**
5. `deployer.RemoveImages` — 删镜像
6. `artifactStore.DeleteByApp`（+ 先删产物文件实体）— 产物
7. `store.Delete` → `DELETE FROM appdeploy_application`，靠 CASCADE 清子表

全局 `grep "DELETE FROM appdeploy_(instance|env)"` → **0 匹配**。即：env 行从不显式删；instance 行只删其 docker 容器、DB 行从不删。两张表都在每个被删过的 app 上残留。

### 1.2 影响

- **数据泄漏 / 卫生**：`appdeploy_env` 残留含 `DATABASE_URL`、`REDIS_ADDR`、`REDIS_PASSWORD` 等 secret；`appdeploy_instance` 残留 `build_log`（可能含敏感构建输出）、`container_name`/`url`。
- **影响面**：所有「曾删过 app」的库；pgsupply / mwsupply（P1/P2/P3）的 spec 都假定「env 靠删 app CASCADE 兜底」，实际一直泄漏。
- **非 P3 引入**：是 init schema（000001）既有遗漏，P3 e2e 只是首次量化暴露（删 dedicated app 后 env 残留 3 行）。

## 2. 目标 / 非目标

**目标**

- 给 `appdeploy_env`、`appdeploy_instance` 补 `app_id → appdeploy_application(id) ON DELETE CASCADE` FK，与既有 4 张子表一致。
- 清掉历史孤儿行（加 FK 前置条件，否则 `ALTER ADD CONSTRAINT` 会因现存孤儿失败）。
- delete 流程零代码改动（已按 CASCADE 模式设计）。

**非目标（YAGNI）**

- 不动 `appdeploy_application` 自身、不碰已有 FK 的子表。
- 不在 handler 加代码层显式 `DELETE FROM appdeploy_env/instance`（DB CASCADE 已是权威兜底；env/instance 的 DB 行无外部实体需代码清，不像产物文件）。
- 不做「孤儿巡检定时任务」。
- 不回填/恢复历史数据（孤儿直接删，secret 本就该随 app 删除而消失）。

## 3. 方案选型

| 方案        | 内容                                                                                                | 结论                                                                                 |
| ----------- | --------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------ |
| **A（采）** | 一个迁移文件：先 `DELETE` 两表孤儿，再 `ALTER ADD CONSTRAINT ... ON DELETE CASCADE`；down 反向 drop | ✅ 与既有 4 表同模式；孤儿 DELETE 必先于 ALTER，同事务保序；migrate 工具全环境可复现 |
| B           | 迁移纯 DDL（ADD CONSTRAINT）+ 单独 ops 脚本清孤儿                                                   | ❌ 引入「先手动清孤儿→再跑迁移」部署顺序坑；可观测性差                               |
| C           | handler 里代码层显式删 env/instance                                                                 | ❌ 冗余、与其余 4 表不一致；YAGNI                                                    |

采 **方案 A**。

## 4. 详细设计

### 4.1 迁移文件

新增 `platform/backend/internal/db/migrations/pg/000031_appdeploy_fk_cascade.up.sql`：

```sql
-- 000031_appdeploy_fk_cascade.up.sql
-- 收口 appdeploy_env / appdeploy_instance 缺 FK：删 app 后这两张表行变孤儿（000001 init schema 遗漏）。
-- 必须先清孤儿，否则 ADD CONSTRAINT 因现存违规行失败。
-- 不写显式 BEGIN/COMMIT：migrate 的 postgres 驱动默认把整个文件包一个事务（与既有迁移一致），
-- 文件内 DELETE→ALTER 顺序即执行顺序，孤儿清除与加约束原子生效。

DELETE FROM appdeploy_env
WHERE app_id NOT IN (SELECT id FROM appdeploy_application);

DELETE FROM appdeploy_instance
WHERE app_id NOT IN (SELECT id FROM appdeploy_application);

ALTER TABLE appdeploy_env
ADD CONSTRAINT appdeploy_env_app_fk
FOREIGN KEY (app_id) REFERENCES appdeploy_application(id) ON DELETE CASCADE;

ALTER TABLE appdeploy_instance
ADD CONSTRAINT appdeploy_instance_app_fk
FOREIGN KEY (app_id) REFERENCES appdeploy_application(id) ON DELETE CASCADE;
```

新增 `000031_appdeploy_fk_cascade.down.sql`：

```sql
-- 只回滚 DDL。历史孤儿 DELETE 不可逆（且本就该随 app 删除消失），不回填。
ALTER TABLE appdeploy_instance DROP CONSTRAINT IF EXISTS appdeploy_instance_app_fk;
ALTER TABLE appdeploy_env      DROP CONSTRAINT IF EXISTS appdeploy_env_app_fk;
```

**约束命名**：`appdeploy_<表>_app_fk`，与既有 FK（如 `appdeploy_service_binding.app_id` 内联无名 FK）风格尽量一致，便于日后定位。

**索引**：`idx_appdeploy_env_app` / `idx_appdeploy_instance_app` 已存在，cascade-delete 扫子表走该索引，无需新增。

**迁移编号**：上一文件 `000030_mwsupply_dedicated`，本迁移 `000031`。

### 4.2 代码改动

**零。** `Store.Delete`、`Handler.Delete` 不动。

### 4.3 测试

- **单测**：`platform/backend/internal/appdeploy/store_test.go` 加 `TestStore_Delete_cascadesEnvAndInstance`：建 app → 插 env 行 → 插 instance 行 → `store.Delete` → 断言 `appdeploy_env` / `appdeploy_instance` 对该 app_id 行数为 0。
  - ⚠️ **sqlite-test-pg-type-trap 风险**：若 `store_test` 的 schema 来源是手写 sqlite DDL（不含这个 FK），则单测验不出 CASCADE，只能验「调用不崩」。实施时**先确认 store_test 的 schema 来源**（`grep -rn "appdeploy_env" platform/backend/internal/appdeploy/*_test.go` 看建表语句出处）。若 sqlite 不带 FK：单测保留作行为冒烟，**真验证以 §5 端到端为准**（与既有 sqlite→PG 真实验证策略一致）。
- **无需前端改动**（后端 schema 层）。

## 5. 端到端验证（.28，权威）

按 [[deploy-28-no-local-test]]：commit → push origin main → scp + .28 docker-compose 重建（自动跑 migrate）。

e2e 步骤：

1. 建一个 app（project_space 内）。
2. 触发依赖注入让 `appdeploy_env` 出现行：pgsupply 注入 `DATABASE_URL`；mwsupply 注入 `REDIS_ADDR` + `REDIS_PASSWORD`（dedicated 或 shared 均可）。
3. 部署生成 `appdeploy_instance` 行（至少 prod 一行）。
4. 记下 `app_id`。
5. **删 app**（走 `DELETE /project-spaces/:id/apps/:aid`）。
6. 断言：
   - `SELECT count(*) FROM appdeploy_env WHERE app_id=$app_id` → **0**
   - `SELECT count(*) FROM appdeploy_instance WHERE app_id=$app_id` → **0**
   - 全库巡检 `SELECT app_id, count(*) FROM appdeploy_env WHERE app_id NOT IN (SELECT id FROM appdeploy_application) GROUP BY app_id` → **空**
   - 同样对 `appdeploy_instance` 巡检 → **空**
7. 顺带确认 `appdeploy_service_binding` / `appdeploy_database` 等仍 CASCADE 正常（回归未坏）。

### 5.1 e2e 结论（2026-08-02 .28 实测，commit `44ebbd7`）

- **迁移落地**：prod `deploy_postgres_1/anp` 库 `schema_migrations` 最新 = `000031_appdeploy_fk_cascade`；`pg_constraint` 含 `appdeploy_env_app_fk` + `appdeploy_instance_app_fk`。
- **历史孤儿清理**：部署前 `env_orphans=21` / `instance_orphans=26`（实锤既有泄漏）→ 迁移 DELETE 后双 **0**。
- **新删 CASCADE 触发**（prod DB 事务内直验）：建 app+2 env+1 instance → `DELETE FROM appdeploy_application` → `env_after_delete=0` / `instance_after_delete=0`（FK CASCADE 真触发，非仅清旧行），ROLLBACK 自清。
- **单测**：`TestStore_Delete_cascadesEnvAndInstance` 在 .28 anp_test 真 PG 通过（Store 全链路：Create→UpsertEnv→GetOrCreateInstance→Delete→两表归零）。
- **回归**：`go test ./internal/appdeploy/ -p 1` 全 PASS；backend deep healthz healthy，无崩溃。

## 6. 影响面 / 风险 / 回滚

- **影响面**：所有「曾删过 app」的库——首次跑迁移会删掉历史孤儿（含 secret 行，符合预期）。之后每个删 app 都干净。
- **风险**：低。`ALTER ADD CONSTRAINT` 前已 `DELETE` 孤儿，不会因违规行失败；被引用列 `appdeploy_application.id` 是 PK，索引齐全。
- **回滚**：跑 `down.sql` drop 两个 FK。注意：历史孤儿已删不恢复；回滚后新删的 app 又会泄漏，故一般不回滚。

## 7. 实现顺序（落到 plan）

1. 写 `000031` up/down 迁移。
2. 确认 store_test schema 来源；加单测（冒烟）。
3. 本机 `go test ./internal/appdeploy/... -p 1 -run Store_Delete`（串行，避免 anp_test 库污染，见 [[go-test-serial-p1]]）。
4. commit → push origin main → scp + .28 重建。
5. .28 e2e 验证（§5）。
6. 写闭环记录 + 给下个 session 开场白（见 [[handoff-prompt-for-next-session]]）。

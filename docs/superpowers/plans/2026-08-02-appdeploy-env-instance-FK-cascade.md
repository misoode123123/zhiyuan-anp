# appdeploy env+instance FK CASCADE 修复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给 `appdeploy_env` 与 `appdeploy_instance` 补 `app_id → appdeploy_application(id) ON DELETE CASCADE` 外键，并清掉历史孤儿行，使删 app 后这两张表的行不再泄漏。

**Architecture:** 纯 schema 层修复——新增 `000031` 迁移文件（先 `DELETE` 两表孤儿，再 `ALTER ADD CONSTRAINT ... ON DELETE CASCADE`），delete 业务代码零改动（已按 CASCADE 模式设计）。迁移由 `//go:embed migrations/pg/*.sql` 自动打包，加文件即生效，无需改 manifest。单测连真 PG（.28 anp_test，`testutil.TestDB` 跑全部迁移）能验出 CASCADE。

**Tech Stack:** PostgreSQL 15、golang-migrate 风格的 embed 迁移（`internal/db/migrate.go`）、Go test（pgx 驱动）。

## Global Constraints

（每个任务的隐含前置；逐字来自 spec 与项目记忆）

- **禁 SQLite，只用 PG**：开发/生产不用 SQLite；单测连 `.28 anp_test`（`testutil.DefaultTestDBURL = postgres://anp:anp_dev_pwd@10.10.0.28:5432/anp_test?sslmode=disable`，无需设环境变量）。
- **迁移规则**：append-only，新需求加新文件 `migrations/pg/NNNNNN_name.up.sql` + `.down.sql`（6 位版本前缀），**不回头改已应用的老迁移**；每个 SQL 必须能在事务内跑（禁 `CREATE DATABASE`/`VACUUM`）；up/down 配对；本迁移上一版本 `000030_mwsupply_dedicated`，本迁移 `000031`。
- **go 命令前缀**：本机 GOPATH 被污染，所有 `go` 命令前加 `GOPATH=C:/Users/yxt/go`。
- **全量回归串行**：`go test ./...` 用 `-p 1`（并发污染 anp_test 库）；单测带 `-run` 过滤可不加。
- **部署到 .28**：commit → `git push origin main` → scp 源码到 .28 `/opt/anp` → docker-compose 重建（启动自动跑 migrate）。
- **代码风格**：迁移文件不写显式 `BEGIN/COMMIT`（runner 事务包裹，与既有 30 个迁移一致）。

## File Structure

| 文件                                                                              | 责任                                                                      | 动作                   |
| --------------------------------------------------------------------------------- | ------------------------------------------------------------------------- | ---------------------- |
| `platform/backend/internal/db/migrations/pg/000031_appdeploy_fk_cascade.up.sql`   | 清两表孤儿 + 加 FK CASCADE                                                | 新建                   |
| `platform/backend/internal/db/migrations/pg/000031_appdeploy_fk_cascade.down.sql` | 回滚两个 FK 约束                                                          | 新建                   |
| `platform/backend/internal/appdeploy/store_test.go`                               | `TestStore_Delete_cascadesEnvAndInstance` 验删 app 后 env/instance 行归零 | 追加一个测试函数       |
| `docs/superpowers/specs/2026-08-02-appdeploy-env-instance-FK-cascade-design.md`   | 设计文档（已 commit `089d3ca`）                                           | 收尾改状态为「已实现」 |

**不动**：`store.go`、`handler.go`、`db/migrate.go`、前端任何文件。

---

## Task 1: 迁移文件 + CASCADE 单测（TDD）

**Files:**

- Create: `platform/backend/internal/db/migrations/pg/000031_appdeploy_fk_cascade.up.sql`
- Create: `platform/backend/internal/db/migrations/pg/000031_appdeploy_fk_cascade.down.sql`
- Modify: `platform/backend/internal/appdeploy/store_test.go`（文件末尾追加测试函数）
- Test: `platform/backend/internal/appdeploy/store_test.go::TestStore_Delete_cascadesEnvAndInstance`

**Interfaces:**

- Consumes: 既有 `Store` 方法——`Create(ctx,*Application)`、`UpsertEnv(ctx,appID,key,value,isSecret,source)`、`GetOrCreateInstance(ctx,appID,env)`、`Delete(ctx,psID,id)`、`ListEnv(ctx,appID)→[]EnvVar`、`ListInstancesByApp(ctx,appID)→[]AppInstance`；测试辅助 `newTestStore(t)`、`mkApp(ps,name)`。
- Produces: 无新公开符号（只加迁移文件 + 一个测试函数）。

- [ ] **Step 1: 写失败的单测（红）**

在 `platform/backend/internal/appdeploy/store_test.go` 末尾（`TestStore_Delete` 函数之后）追加：

```go
// TestStore_Delete_cascadesEnvAndInstance 删 app 后 appdeploy_env / appdeploy_instance 行
// 应被 FK ON DELETE CASCADE 清掉（修 000001 init schema 漏加 FK 的缺口）。
// 验证路径：testutil.TestDB 跑全部迁移 → 000031 加的 FK 生效 → Delete 触发级联清空。
func TestStore_Delete_cascadesEnvAndInstance(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "snake")
	if err := s.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	// 注入 env 行（模拟 pgsupply 的 DATABASE_URL + mwsupply 的 REDIS_ADDR）
	if err := s.UpsertEnv(ctx, a.ID, "DATABASE_URL", "postgres://u:p@h/db", true, "platform"); err != nil {
		t.Fatalf("upsert env DATABASE_URL: %v", err)
	}
	if err := s.UpsertEnv(ctx, a.ID, "REDIS_ADDR", "redis://h:6379", false, "platform"); err != nil {
		t.Fatalf("upsert env REDIS_ADDR: %v", err)
	}
	// 建 instance 行（模拟部署生成 prod 实例记录）
	if _, err := s.GetOrCreateInstance(ctx, a.ID, "prod"); err != nil {
		t.Fatalf("getorcreate instance: %v", err)
	}

	// 删 app → 期待 FK CASCADE 清 env + instance
	if err := s.Delete(ctx, "ps_1", a.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	envs, err := s.ListEnv(ctx, a.ID)
	if err != nil {
		t.Fatalf("list env after delete: %v", err)
	}
	if len(envs) != 0 {
		t.Fatalf("env 应被 CASCADE 清空，仍剩 %d 行: %+v", len(envs), envs)
	}
	inss, err := s.ListInstancesByApp(ctx, a.ID)
	if err != nil {
		t.Fatalf("list instance after delete: %v", err)
	}
	if len(inss) != 0 {
		t.Fatalf("instance 应被 CASCADE 清空，仍剩 %d 行: %+v", len(inss), inss)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败（红）**

Run（仓库根 `platform/backend/` 下；前缀 GOPATH 修污染）:

```bash
cd "/d/Projects/智源-ANP平台/platform/backend" && GOPATH=C:/Users/yxt/go go test ./internal/appdeploy/ -run TestStore_Delete_cascadesEnvAndInstance -v
```

Expected: **FAIL**，类似 `env 应被 CASCADE 清空，仍剩 2 行`（因为还没加 000031 迁移，无 FK，删 app 后 env/instance 行残留）。

> 若失败信息是「连 anp_test 失败」→ 先确认能 ping .28 / `.28` 可达；不要当成测试逻辑错。

- [ ] **Step 3: 写迁移文件（实现，转绿）**

Create `platform/backend/internal/db/migrations/pg/000031_appdeploy_fk_cascade.up.sql`:

```sql
-- 000031_appdeploy_fk_cascade.up.sql
-- 收口 appdeploy_env / appdeploy_instance 缺 FK：删 app 后这两张表行变孤儿（000001 init schema 遗漏）。
-- 必须先清孤儿，否则 ADD CONSTRAINT 因现存违规行失败。
-- 不写显式 BEGIN/COMMIT：migrate runner 把每个迁移包一个事务（与既有迁移一致），
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

Create `platform/backend/internal/db/migrations/pg/000031_appdeploy_fk_cascade.down.sql`:

```sql
-- 000031_appdeploy_fk_cascade.down.sql
-- 只回滚 DDL。历史孤儿 DELETE 不可逆（且本就该随 app 删除消失），不回填。
ALTER TABLE appdeploy_instance DROP CONSTRAINT IF EXISTS appdeploy_instance_app_fk;
ALTER TABLE appdeploy_env      DROP CONSTRAINT IF EXISTS appdeploy_env_app_fk;
```

- [ ] **Step 4: 跑测试确认通过（绿）**

Run:

```bash
cd "/d/Projects/智源-ANP平台/platform/backend" && GOPATH=C:/Users/yxt/go go test ./internal/appdeploy/ -run TestStore_Delete_cascadesEnvAndInstance -v
```

Expected: **PASS**（`ok ... — 000031 迁移已让 FK 生效，删 app 后两表行被 CASCADE 清空`）。

> `//go:embed migrations/pg/*.sql` 是 glob，新文件 `go test` 重新编译即自动打包，无需改 `migrate.go`。

- [ ] **Step 5: Commit**

```bash
cd "/d/Projects/智源-ANP平台" && git add platform/backend/internal/db/migrations/pg/000031_appdeploy_fk_cascade.up.sql platform/backend/internal/db/migrations/pg/000031_appdeploy_fk_cascade.down.sql platform/backend/internal/appdeploy/store_test.go && git commit -m "$(cat <<'EOF'
fix(appdeploy): env+instance 加 FK CASCADE,清删 app 后孤儿行

000001 init schema 漏给 appdeploy_env/appdeploy_instance 加 app_id→application FK,
删 app 后这两张表行变孤儿(env 含 DATABASE_URL/REDIS_PASSWORD 等 secret;instance 含 build_log)。

新增 000031 迁移:先清两表历史孤儿,再加 FK ON DELETE CASCADE。
delete 业务代码零改动(已按 CASCADE 模式设计)。单测连 .28 anp_test 真 PG 验级联清空。

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: 全量回归 + .28 部署 + 端到端验证 + 收尾

**Files:**

- Verify: 全 `appdeploy` 包测试无回归
- Deploy: `.28 /opt/anp`（docker-compose 重建自动跑 migrate）
- Modify: `docs/superpowers/specs/2026-08-02-appdeploy-env-instance-FK-cascade-design.md`（状态改「已实现」+ 补 e2e 结论）

**Interfaces:**

- Consumes: Task 1 的迁移与单测；`.28` 部署通道（keyless SSH、scp、docker-compose）。
- Produces: `.28` 生产库跑过 000031、e2e 通过的证据、闭环记录。

- [ ] **Step 1: 全量 appdeploy 包回归（串行，防 anp_test 污染）**

Run:

```bash
cd "/d/Projects/智源-ANP平台/platform/backend" && GOPATH=C:/Users/yxt/go go test ./internal/appdeploy/ -p 1 -v
```

Expected: **全 PASS**。重点关注新增的 `TestStore_Delete_cascadesEnvAndInstance` 与既有 `TestStore_Delete`、`TestStore_GetOrCreateInstance`、`TestStore_SetInstanceStatus` 不回归。

- [ ] **Step 2: 推到远端 main**

```bash
cd "/d/Projects/智源-ANP平台" && git push origin main
```

Expected: 推送成功（Task 1 的迁移 commit 上远端）。

- [ ] **Step 3: 部署到 .28（docker-compose 重建自动跑 migrate）**

按 `.28` 部署惯例（keyless SSH 到 `10.10.0.28`、源码在 `/opt/anp`、入口 8088）：

```bash
# 1) 同步源码到 .28（项目惯用 scp；路径以实际为准）
scp -r "/d/Projects/智源-ANP平台/platform/backend/." anp@10.10.0.28:/opt/anp/platform/backend/
# 2) 重建后端容器（启动时 Migrate 自动应用 000031）
ssh anp@10.10.0.28 'cd /opt/anp && docker compose up -d --build backend'
# 3) 确认 migrate 跑过 000031
ssh anp@10.10.0.28 'cd /opt/anp && docker compose logs backend | grep -iE "migrat|000031" | tail -20'
```

Expected: backend 容器起来；日志含迁移应用到 `000031`（或无报错、`schema_migrations` 已记录 31）。

> 若 scp 目标路径/用户与上述不同，按 `deploy-prod-10.10.0.28` 记忆里的实际通道调整，目标一致即可。

- [ ] **Step 4: 验证 FK 已落地（库层确认）**

连 `.28` 生产库（`anp_dev` 或实际生产库名）执行：

```bash
ssh anp@10.10.0.28 'docker exec -i <pg容器名> psql -U anp -d anp_dev -c "SELECT conname, conrelid::regclass AS tbl FROM pg_constraint WHERE conname IN ('"'"'appdeploy_env_app_fk'"'"','"'"'appdeploy_instance_app_fk'"'"');"'
```

Expected: 两行——`appdeploy_env_app_fk | appdeploy_env` 与 `appdeploy_instance_app_fk | appdeploy_instance`。

> `<pg容器名>` 用 `docker ps --format '{{.Names}}' | grep -i postgres` 找；库名以实际为准（生产 PG 库）。

- [ ] **Step 5: 端到端验证（删 app → 两表归零 + 全库无孤儿）**

通过平台（前端或 API）走完整流程，或在 `.28` 库直查：

1. 建一个 app（某 project_space 内），记 `app_id`。
2. 触发依赖注入让 `appdeploy_env` 出现行：pgsupply 注入 `DATABASE_URL`；mwsupply 注入 `REDIS_ADDR`+`REDIS_PASSWORD`（dedicated/shared 均可）。
3. 部署生成 `appdeploy_instance` 行（至少 prod 一行）。
4. 删 app（前端删 app，或 `DELETE /project-spaces/:id/apps/:aid`）。
5. 直查确认（连 `.28` 生产库）：

```sql
-- 该 app 两表应归零
SELECT count(*) FROM appdeploy_env      WHERE app_id = '<app_id>';   -- 期望 0
SELECT count(*) FROM appdeploy_instance WHERE app_id = '<app_id>';   -- 期望 0
-- 全库无孤儿（迁移的历史清理 + 后续 CASCADE 双保障）
SELECT 'env'      AS t, count(*) FROM appdeploy_env      WHERE app_id NOT IN (SELECT id FROM appdeploy_application)
UNION ALL
SELECT 'instance' AS t, count(*) FROM appdeploy_instance WHERE app_id NOT IN (SELECT id FROM appdeploy_application);
-- 期望两行 count 均为 0
```

6. 顺带回归：删 app 后 `appdeploy_service_binding` / `appdeploy_database` 仍 CASCADE 正常（既有 4 表未坏）。

Expected: 该 `app_id` 两表 count=0；全库孤儿巡检两行均 0。

- [ ] **Step 6: 收尾——spec 状态 + 闭环记录 + 给下个 session 开场白**

1. 编辑 `docs/superpowers/specs/2026-08-02-appdeploy-env-instance-FK-cascade-design.md`：把开头 `- 状态：待实现` 改为 `- 状态：✅ 已实现（.28 e2e 通过）`，并在 §5 末尾补一行 e2e 结论（删 app 后 env/instance count=0、全库无孤儿）。
2. commit + push：

```bash
cd "/d/Projects/智源-ANP平台" && git add docs/superpowers/specs/2026-08-02-appdeploy-env-instance-FK-cascade-design.md && git commit -m "$(cat <<'EOF'
docs(appdeploy): env+instance FK CASCADE e2e 结论(.28 通过)

删 app 后 appdeploy_env/appdeploy_instance count=0,全库无孤儿。
000031 迁移已在 .28 生产库落地。

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
)" && git push origin main
```

3. 更新记忆 `appdeploy-env-no-cascade`：缺口已修（000031 加 FK CASCADE + 清孤儿），从「待修」改为「已闭环」。关联本 spec。
4. 给下个 session 可复制的开场白（用「复制开始/结束」标记包裹），含：本闭环结论 + 可选下一项（headless 运行 / milvus dedicated / host 网络门禁）。

---

## Self-Review 结论

- **Spec 覆盖**：§1 背景证据 → Task 1 测试复现；§4.1 迁移 → Task 1 Step 3 逐字落地；§4.3 测试 → Task 1 Step 1（sqlite-trap 顾虑已被「testutil 跑迁移连真 PG」化解，单测可真验 CASCADE）；§5 e2e → Task 2 Step 5；§7 实现顺序 → Task 1+2 顺序对齐。无遗漏。
- **占位符**：无 TBD/TODO；所有命令、SQL、测试代码均完整。
- **类型一致**：`UpsertEnv`/`GetOrCreateInstance`/`ListEnv`/`ListInstancesByApp`/`Delete` 签名均与 `store.go` 核对一致；约束名 `appdeploy_env_app_fk` / `appdeploy_instance_app_fk` 全文统一。

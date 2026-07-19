# 切换 PostgreSQL + pgvector 方案（设计稿）

> 目标：将后端持久层从 SQLite 切到 PostgreSQL（+pgvector），解决 SQLite 写锁/单点/备份短板，并为 RAG/AI 能力市场准备向量检索。同时把延后的 #11 迁移工具化一并完成（golang-migrate 版本化 SQL）。
> 配套：[`2026-07-18-8人团队工程化完善方案.md`](2026-07-18-8人团队工程化完善方案.md) P1-3 + [`开发标准与规范.md`](详细设计/开发标准与规范.md) §2.4。
> 状态：执行中。

---

## 0. 决策记录（已与用户确认）

| 决策点      | 选择                                                                  |
| ----------- | --------------------------------------------------------------------- |
| 单元测试 DB | **PG testcontainer**（与生产方言完全一致，测试绿=生产行为）           |
| .28 生产 PG | **新起 `deploy_` 前缀 postgres 容器**（遵循共享服务器规则，数据隔离） |
| pgvector    | **一起上**（`pgvector/pgvector:pg16` 镜像，为 RAG/AI 能力市场预备）   |
| 现有数据    | **dev 重建（seed）；.28 生产 anp.db 导出导入 PG**                     |

---

## 1. 现状与改造面

- SQLite 方言 **100 处 / 17 文件**，集中 `migrate.go`（59 处）。
- 驱动层 `db.go:parseDSN` 仅认 `sqlite://`（已留 TODO）。
- **利好**：主键全 `TEXT`+uuid（非自增）；业务 SQL 用 `?` 占位符 → sqlx+pgx 自动转 `$n`，**业务 store SQL 基本不动**。
- **必改方言**（集中 migrate.go）：
  - `PRAGMA table_info` → golang-migrate 管版本，删除该幂等补列逻辑
  - `DATETIME` → `TIMESTAMP`
  - `INSERT OR IGNORE` → `INSERT ... ON CONFLICT DO NOTHING`
  - `CURRENT_TIMESTAMP` / `TEXT` 主键 / `CREATE TABLE IF NOT EXISTS` → PG 原生兼容

---

## 2. 技术选型

| 组件     | 选型                                                  | 理由                                     |
| -------- | ----------------------------------------------------- | ---------------------------------------- |
| PG 驱动  | `github.com/jackc/pgx/v5` (+stdlib 注册 database/sql) | Go PG 事实标准，sqlx 兼容，`?` 自动 bind |
| 迁移工具 | `github.com/golang-migrate/migrate/v4`                | 版本化 SQL，up/down，CI 友好             |
| 测试     | `github.com/testcontainers/testcontainers-go`         | 真 PG 容器，方言一致                     |
| 镜像     | `pgvector/pgvector:pg16`                              | PG16 + pgvector 扩展一步到位             |

---

## 3. 改造步骤（依赖序）

### PG-1 依赖 + db.go 双驱动

- go.mod 加 pgx/v5、golang-migrate、testcontainers。
- `db.go`：`parseDSN` 支持 `postgres://`，按驱动选 sqlx driver；保留 sqlite 支持（测试可切回/回退）。`Open` 仍返回 `*sqlx.DB`。

### PG-2 migrate 重写为 golang-migrate

- 把 `migrate.go` 的 `sqliteSchema` + 增量列按演进顺序拆为 `infra/migrations/pg/`：
  - `000001_init.up.sql` / `.down.sql`（所有初始表，PG 语法）
  - `000002_xxx.up.sql`（后续增量列）
- `Migrate()` 改为调 golang-migrate（embed SQL 文件）。
- 删除 `addColumnIfMissing` / PRAGMA 逻辑（版本化迁移不需幂等补列）。
- seed 函数保留（Go 可重入），`migrateInstances`（数据迁移）保留为迁移内步骤。

### PG-3 审查 17 文件 SQL 方言

- `INSERT OR IGNORE` → `ON CONFLICT DO NOTHING`（各 store insert）。
- `DATETIME` 字面量（建表已在迁移 SQL，运行时查询若无不用改）。
- `?` 占位符不动（sqlx+pgx 自动转）。

### PG-4 testcontainer 测试改造

- 新建 `internal/testutil`：testcontainers 起 `pgvector/pgvector:pg16`，跑迁移，返回 `*sqlx.DB`。
- 各 `*_test.go` 的 `newTestStore` 从 sqlite `:memory:` 改用 testutil（真 PG）。

### PG-5 docker-compose 加 pgvector

- `docker-compose.yml`（dev）+ `docker-compose.prod.yml`（.28）加 postgres 服务 + 数据卷。
- backend env：`DATABASE_URL=postgres://anp:***@postgres:5432/anp?sslmode=disable`。

### PG-6 本地端到端验证

- 起 pg → migrate → seed → 后端启动 → 全量 `go test` → 前端登录主线。

### PG-7 .28 部署 + 数据迁移

- .28 新起 `deploy_postgres` 容器。
- `sqlite3 anp.db .dump` → 转 PG INSERT → 导入。
- backend 重建切 `DATABASE_URL`。

---

## 4. 风险与回滚

| 风险                              | 缓解                                                                       |
| --------------------------------- | -------------------------------------------------------------------------- |
| 迁移 SQL 拆分遗漏表/列 → 启动失败 | 拆分后用空 PG 库 `migrate up && seed` 全量建表，与原 sqliteSchema 逐表比对 |
| 数据迁移丢数据                    | .28 先 dump 备份 anp.db；导入后行数比对                                    |
| 生产切库停服                      | .28 部署窗口内操作；保留 sqlite 代码路径可快速回退 DATABASE_URL            |
| 测试变慢（testcontainer）         | 仅 store 测试用 PG；纯函数测试（如 devstep）不经 DB                        |

---

## 5. 验收

- 空 PG 库迁移后表结构与原 SQLite 一致（逐表核对）。
- 全量 `go test ./...` 在 PG testcontainer 下绿。
- 本地三服务（pg + backend + frontend）跑通登录主线。
- .28 切 PG 后入口 8088 可用、数据完整。

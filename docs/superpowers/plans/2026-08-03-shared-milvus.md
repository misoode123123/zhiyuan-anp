# milvus shared（collection 前缀隔离）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 mwsupply 的 shared 供给从 redis 扩到 milvus——`supplyShared` 按 kind 分派，milvus 用随机 collection 前缀隔离，注入 `MILVUS_ADDR` + `MILVUS_COLLECTION_PREFIX`，补齐 milvus 三策略最后一档。

**Architecture:** 镜像 P4 对 `supplyDedicated` 的 kind 分派重构，但作用在 `supplyShared` 上。redis 路径逐字保留（搬入 `allocRedisDB` + `writeSharedEnv` 默认分支），milvus 新增 `allocMilvusPrefix`（随机 `app<12hex>_` 前缀 + 单次 claim + 有界重试）与 `writeSharedEnv` milvus 分支。不起任何容器（复用 .28 共享 milvus），故无 docker/就绪检测/main.go 改动。仅 1 迁移（种子）。

**Tech Stack:** Go 1.25（`platform/backend`，module `zhiyuan-anp/platform/backend`）、PostgreSQL（pgvector，测试库 `anp_test`）、`database/sql` + `jmoiron/sqlx`、go:embed 迁移、表驱动测试。

**Spec:** `docs/superpowers/specs/2026-08-03-中间件依赖注入-P5-shared-milvus-design.md`

---

## Global Constraints

- **禁 SQLite，只用 PG**（记忆 `no-sqlite-pg-only`）：所有测试连真 PG 库 `anp_test`（经 `testutil.TestDB`），不回退 sqlite。
- **全量回归串行**（记忆 `go-test-serial-p1`）：跑 `go test ./...` 必须加 `-p 1`（防并发污染 `anp_test`）；单包测试可不加（mwsupply 测试无 `t.Parallel`）。
- **GOPATH 前缀**（记忆 `gopath-pollution-windows`）：本机 `go` 命令前缀 `GOPATH=C:/Users/yxt/go`，否则 GOPATH 被污染成 go.exe 路径。
- **redis 零回归**：重构后 redis shared/dedicated 行为逐字不变，既有 redis 单测全绿是硬性闸门。
- **迁移自动跑**：迁移文件放 `platform/backend/internal/db/migrations/pg/`，经 go:embed 由 `testutil.TestDB` 在测试库自动应用（无需手动 migrate）；生产由 backend 启动迁移。
- **提交规范**：conventional commits（`feat(mwsupply): ...`），末尾 `Co-Authored-By: Claude <noreply@anthropic.com>`；直接提交 main（项目约定，见 git log）。
- **本机不跑功能测试**（记忆 `deploy-28-no-local-test`）：本计划只做单测；e2e 在 .28（commit→push→scp+重建），不在本计划范围内（spec §10.2）。

---

## File Structure

| 文件                                                                                | 责任                                                                       | 本期动作                                                                                                                       |
| ----------------------------------------------------------------------------------- | -------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| `platform/backend/internal/mwsupply/naming.go`                                      | 命名/ID 生成纯函数（`genShortID`/`genPassword`/`allocPort`/容器名/端口池） | 新增 `genMilvusPrefix()`                                                                                                       |
| `platform/backend/internal/mwsupply/supply.go`                                      | 供给编排（`Reconcile`/`supplyShared`/`supplyDedicated`/`Cleanup`）         | `supplyShared` kind 分派重构 + 新增 `allocAndClaimShared`/`allocRedisDB`/`allocMilvusPrefix` + `writeSharedEnv` 加 milvus 分支 |
| `platform/backend/internal/db/migrations/pg/000033_mwsupply_shared_milvus.up.sql`   | shared milvus 实例种子                                                     | 新建                                                                                                                           |
| `platform/backend/internal/db/migrations/pg/000033_mwsupply_shared_milvus.down.sql` | 回滚种子                                                                   | 新建                                                                                                                           |
| `platform/backend/internal/mwsupply/naming_test.go`                                 | naming 纯函数单测                                                          | 新增 `TestGenMilvusPrefix`                                                                                                     |
| `platform/backend/internal/mwsupply/store_test.go`                                  | store + 迁移单测                                                           | 新增 `TestMigration_000033_sharedMilvusSeed`；改 `TestStore_LookupShared_seed`（milvus nil→命中）                              |
| `platform/backend/internal/mwsupply/supply_test.go`                                 | Reconciler 单测 + `ensureSeed` 测试种子                                    | `ensureSeed` 加 milvus shared 行；新增 milvus shared 用例（新分配/隔离/幂等/无实例）                                           |

**不改**：`connstr.go`（`EnvKeyFor`/`ConnStr` 已通用）、`model.go`（常量/字段已齐）、`store.go`（`LookupShared`/`AllocatedTokens`/`ClaimSharedToken` 已通用）、`docker.go`（shared 不起容器）、`isolation.go`（`ParseDBRange`/`pickLowestFree` 仍只服务 redis）、`redisflush.go`（flush 仍只服务 redis）、`main.go`（无新依赖）、`handler.go`（shared 靠 CASCADE，Delete 不变）。

---

## Task 1: `genMilvusPrefix` 纯函数

生成 milvus shared collection 前缀 `app<12hex>_`，复用 `genShortID`。纯函数，无依赖，先立。

**Files:**

- Modify: `platform/backend/internal/mwsupply/naming.go`
- Test: `platform/backend/internal/mwsupply/naming_test.go`

**Interfaces:**

- Consumes: `genShortID() string`（naming.go 已有，返回 12 位 hex）
- Produces: `genMilvusPrefix() string` —— 返回 `app` + 12hex + `_`（如 `app1a2b3c4d5e6f_`），Task 3 `allocMilvusPrefix` 调用

- [ ] **Step 1: 写失败测试（naming_test.go）**

在 `platform/backend/internal/mwsupply/naming_test.go` 顶部 import 块加 `"regexp"`（当前只有 `testing`/`time`），并在文件末尾追加：

```go
func TestGenMilvusPrefix(t *testing.T) {
	re := regexp.MustCompile(`^app[0-9a-f]{12}_$`)
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		p := genMilvusPrefix()
		if !re.MatchString(p) {
			t.Fatalf("前缀应匹配 ^app[0-9a-f]{12}_$，得 %q", p)
		}
		if p[0] != 'a' { // 首字符须字母（milvus collection 名规则）
			t.Fatalf("首字符应字母，得 %q", p)
		}
		if seen[p] {
			t.Fatalf("1000 次内不应碰撞：%q", p)
		}
		seen[p] = true
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run:

```bash
cd platform/backend && GOPATH=C:/Users/yxt/go go test ./internal/mwsupply/ -run TestGenMilvusPrefix -v
```

Expected: FAIL / 编译错 `undefined: genMilvusPrefix`

- [ ] **Step 3: 实现 `genMilvusPrefix`（naming.go）**

在 `platform/backend/internal/mwsupply/naming.go` 的 `genPassword` 函数之后追加：

```go
// genMilvusPrefix 生成 milvus shared collection 前缀：app<12hex>_。
// 复用 genShortID 的 crypto/rand 12-hex；'app' 首字符为字母、仅 [a-zA-Z0-9_]，合 milvus collection 名规则
// （首字符字母、仅字母数字下划线、长度 1-255）。app 给 collection 加前缀后仍合法（如 app1a2b..._foo）。
func genMilvusPrefix() string {
	return "app" + genShortID() + "_"
}
```

- [ ] **Step 4: 跑测试确认通过**

Run:

```bash
cd platform/backend && GOPATH=C:/Users/yxt/go go test ./internal/mwsupply/ -run TestGenMilvusPrefix -v
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
cd platform/backend && git add internal/mwsupply/naming.go internal/mwsupply/naming_test.go
git commit -m "$(cat <<'EOF'
feat(mwsupply): genMilvusPrefix 随机 collection 前缀 app<12hex>_ (P5 shared milvus)

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: 迁移 000033 + shared milvus 种子

建 shared milvus 实例种子（复用 .28:19530 yxt-milvus），更新测试种子 helper 与 store 测试断言。

**Files:**

- Create: `platform/backend/internal/db/migrations/pg/000033_mwsupply_shared_milvus.up.sql`
- Create: `platform/backend/internal/db/migrations/pg/000033_mwsupply_shared_milvus.down.sql`
- Modify: `platform/backend/internal/mwsupply/supply_test.go`（`ensureSeed` 加行）
- Test: `platform/backend/internal/mwsupply/store_test.go`（新增迁移测试 + 改 LookupShared 断言）

**Interfaces:**

- Consumes: 无（迁移是 DDL/种子；store 测试用既有 `LookupShared`）
- Produces: `appdeploy_service_instance` 行 `svinst-milvus-shared-28`（`kind=milvus, supply_mode=shared, isolation={"mode":"prefix"}, host=10.10.0.28, port=19530`）—— Task 3 `supplyShared` 经 `LookupShared("milvus")` 取此行

- [ ] **Step 1: 写迁移 up 文件**

创建 `platform/backend/internal/db/migrations/pg/000033_mwsupply_shared_milvus.up.sql`：

```sql
-- 000033_mwsupply_shared_milvus.up.sql
-- P5：shared 共享 milvus（collection 前缀隔离）
-- ① 种子一条平台级 shared milvus 实例（复用 .28 同一台 yxt-milvus，每 app 独占一个 collection 前缀）
-- ② 唯一索引 uq_svbind_inst_token（000029 已建）直接复用——token=前缀串，两 app 前缀不同不冲突，无需新索引

INSERT INTO appdeploy_service_instance
  (id, project_space_id, kind, name, supply_mode, host, port, auth_ref, isolation, status)
VALUES
  ('svinst-milvus-shared-28', NULL, 'milvus', 'yxt-milvus-shared', 'shared',
   '10.10.0.28', 19530, NULL, '{"mode":"prefix"}'::jsonb, 'active')
ON CONFLICT (id) DO NOTHING;
```

- [ ] **Step 2: 写迁移 down 文件**

创建 `platform/backend/internal/db/migrations/pg/000033_mwsupply_shared_milvus.down.sql`：

```sql
-- 000033_mwsupply_shared_milvus.down.sql
DELETE FROM appdeploy_service_instance WHERE id = 'svinst-milvus-shared-28';
```

- [ ] **Step 3: 写失败测试（store_test.go）**

在 `platform/backend/internal/mwsupply/store_test.go` 末尾（`contains` 函数之前）追加迁移测试：

```go
// TestMigration_000033_sharedMilvusSeed 迁移后：shared milvus 种子在（isolation mode=prefix）。
func TestMigration_000033_sharedMilvusSeed(t *testing.T) {
	_, db := newTestStore(t)
	var mode, supplyMode string
	var port int
	err := db.QueryRow(`SELECT isolation->>'mode', supply_mode, port
		FROM appdeploy_service_instance
		WHERE id='svinst-milvus-shared-28' AND kind='milvus' AND project_space_id IS NULL`).
		Scan(&mode, &supplyMode, &port)
	if err != nil {
		t.Fatalf("shared milvus 种子缺失: %v", err)
	}
	if mode != "prefix" || supplyMode != "shared" || port != 19530 {
		t.Fatalf("shared milvus 种子不符: mode=%s supply_mode=%s port=%d", mode, supplyMode, port)
	}
}
```

- [ ] **Step 4: 改既有 `TestStore_LookupShared_seed` 断言（milvus nil → 命中）**

`platform/backend/internal/mwsupply/store_test.go` 的 `TestStore_LookupShared_seed`（约 110-114 行）当前断言「无 shared milvus → nil」。改为「命中 shared milvus 种子」。

把这段：

```go
	// 无 shared milvus → nil,nil
	gotM, err := s.LookupShared(context.Background(), "milvus")
	if err != nil || gotM != nil {
		t.Fatalf("shared milvus 应 nil,nil，得 %+v err=%v", gotM, err)
	}
```

改为：

```go
	// shared milvus 种子也在（P5）
	gotM, err := s.LookupShared(context.Background(), "milvus")
	if err != nil || gotM == nil {
		t.Fatalf("应命中 shared milvus 种子，err=%v got=%+v", err, gotM)
	}
	if gotM.ID != "svinst-milvus-shared-28" || gotM.SupplyMode != "shared" || gotM.Port != 19530 {
		t.Fatalf("shared milvus 种子不符: %+v", gotM)
	}
	// 未注册 kind 仍 nil,nil
	gotX, err := s.LookupShared(context.Background(), "mongodb")
	if err != nil || gotX != nil {
		t.Fatalf("未注册 kind 应 nil,nil，得 %+v err=%v", gotX, err)
	}
```

- [ ] **Step 5: 跑 store 测试确认迁移测试失败、既有断言失败**

Run:

```bash
cd platform/backend && GOPATH=C:/Users/yxt/go go test ./internal/mwsupply/ -run 'TestMigration_000033_sharedMilvusSeed|TestStore_LookupShared_seed' -v
```

Expected:

- `TestMigration_000033_sharedMilvusSeed`：FAIL（`shared milvus 种子缺失`——迁移 000033 还没被 testutil 应用？应已应用。若 PASS 说明迁移已就位，正常）

> 注：`testutil.TestDB` 在测试库自动跑全部迁移（含新建的 000033）。若 step 5 迁移测试就 PASS，说明种子已就位，直接进 step 6。`TestStore_LookupShared_seed` 此时也应 PASS（断言已改为命中）。

- [ ] **Step 6: 更新 `ensureSeed`（supply_test.go）加 milvus shared 行**

`platform/backend/internal/mwsupply/supply_test.go` 的 `ensureSeed`（约 94-104 行）当前插 3 行种子。改成 4 行（加 milvus shared）。

把 `ensureSeed` 里的 `db.Exec(...)` SQL 改为（在 `svinst-redis-shared-28` 行后加一行 `svinst-milvus-shared-28`）：

```go
	_, err := db.Exec(`INSERT INTO appdeploy_service_instance (id, project_space_id, kind, name, supply_mode, host, port, isolation, status) VALUES
	  ('svinst-redis-28',NULL,'redis','yxt-redis','bind_existing','10.10.0.28',6381,'{"default_db":0}'::jsonb,'active'),
	  ('svinst-milvus-28',NULL,'milvus','yxt-milvus','bind_existing','10.10.0.28',19530,NULL,'active'),
	  ('svinst-redis-shared-28',NULL,'redis','yxt-redis-shared','shared','10.10.0.28',6381,'{"db_range":[1,15]}'::jsonb,'active'),
	  ('svinst-milvus-shared-28',NULL,'milvus','yxt-milvus-shared','shared','10.10.0.28',19530,'{"mode":"prefix"}'::jsonb,'active')
	  ON CONFLICT (id) DO NOTHING`)
```

- [ ] **Step 7: 跑 store 测试确认通过**

Run:

```bash
cd platform/backend && GOPATH=C:/Users/yxt/go go test ./internal/mwsupply/ -run 'TestMigration_000033_sharedMilvusSeed|TestStore_LookupShared_seed' -v
```

Expected: PASS（两个测试均绿）

- [ ] **Step 8: 提交**

```bash
cd platform/backend && git add internal/db/migrations/pg/000033_mwsupply_shared_milvus.up.sql internal/db/migrations/pg/000033_mwsupply_shared_milvus.down.sql internal/mwsupply/store_test.go internal/mwsupply/supply_test.go
git commit -m "$(cat <<'EOF'
feat(mwsupply): 000033 shared milvus 种子 + 测试种子 (P5)

svinst-milvus-shared-28 复用 .28:19530 yxt-milvus; isolation mode=prefix;
ensureSeed 加行; LookupShared("milvus") 断言 nil→命中。

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: `supplyShared` 按 kind 分派（milvus shared 供给）

核心改动：`supplyShared` 新分配段抽 `allocAndClaimShared` 分派（redis 逐字保留 / milvus 随机前缀），`writeSharedEnv` 加 milvus 分支。配 milvus shared 单测 + redis 零回归。

**Files:**

- Modify: `platform/backend/internal/mwsupply/supply.go`（`supplyShared` 重构 + 新增 3 函数 + `writeSharedEnv` 分派）
- Test: `platform/backend/internal/mwsupply/supply_test.go`（新增 milvus shared 用例 + `regexp` import）

**Interfaces:**

- Consumes: `genMilvusPrefix()`（Task 1）、`LookupShared`/`AllocatedTokens`/`ClaimSharedToken`（store.go，已通用）、`isUniqueViolation`（包内已有，`claimWithRetry` 在用）、`EnvKeyFor`/`ConnStr`（connstr.go，milvus 已支持）、`svinst-milvus-shared-28` 种子（Task 2）
- Produces: 重构后的 `supplyShared`——对 `kind: milvus, strategy: shared` 产出 `MILVUS_ADDR` + `MILVUS_COLLECTION_PREFIX` env 与 `strategy=shared, status=bound, isolation_token=app<12hex>_` binding

- [ ] **Step 1: 写失败测试（supply_test.go）—— milvus shared 新分配 + 隔离**

在 `platform/backend/internal/mwsupply/supply_test.go` 顶部 import 块加 `"regexp"`（在 `"path/filepath"` 之后）。当前 import：

```go
import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"zhiyuan-anp/platform/backend/internal/appdeploy"
	"zhiyuan-anp/platform/backend/internal/testutil"
)
```

改为（加 `"regexp"`）：

```go
import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"zhiyuan-anp/platform/backend/internal/appdeploy"
	"zhiyuan-anp/platform/backend/internal/testutil"
)
```

在文件末尾追加 milvus shared 用例：

```go
// —— milvus shared 用例 ——

// TestReconcile_sharedMilvus 两个 shared milvus app 分到不同前缀；MILVUS_ADDR + MILVUS_COLLECTION_PREFIX；flusher 不被调；无 password/db。
func TestReconcile_sharedMilvus(t *testing.T) {
	r, appStore, db, fl, _ := newReconcilerTest(t)
	ctx := context.Background()
	dir := writeManifest(t, "services:\n  - kind: milvus\n    strategy: shared\n")

	mk := func(name string) string {
		a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: name, RepoDir: "/x", InternalPort: 8080}
		_ = appStore.Create(ctx, a)
		_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
		return a.ID
	}
	a1 := mk("msh1")
	a2 := mk("msh2")

	pf1, _ := appStore.GetEnvValue(ctx, a1, "MILVUS_COLLECTION_PREFIX")
	pf2, _ := appStore.GetEnvValue(ctx, a2, "MILVUS_COLLECTION_PREFIX")
	if pf1 == "" || pf2 == "" || pf1 == pf2 {
		t.Fatalf("两 app 前缀应不同且非空，得 %q / %q", pf1, pf2)
	}
	re := regexp.MustCompile(`^app[0-9a-f]{12}_$`)
	if !re.MatchString(pf1) || !re.MatchString(pf2) {
		t.Fatalf("前缀应匹配 ^app[0-9a-f]{12}_$，得 %q / %q", pf1, pf2)
	}
	for _, aid := range []string{a1, a2} {
		ma, _ := appStore.GetEnvValue(ctx, aid, "MILVUS_ADDR")
		if ma != "10.10.0.28:19530" {
			t.Fatalf("MILVUS_ADDR 应 10.10.0.28:19530，得 %q", ma)
		}
		src, _ := appStore.GetEnvSource(ctx, aid, "MILVUS_COLLECTION_PREFIX")
		if src != "platform" {
			t.Fatalf("MILVUS_COLLECTION_PREFIX source 应 platform，得 %q", src)
		}
		if mp, _ := appStore.GetEnvValue(ctx, aid, "MILVUS_PASSWORD"); mp != "" {
			t.Fatalf("milvus v1 无 auth，不应写 MILVUS_PASSWORD，得 %q", mp)
		}
		if mdb, _ := appStore.GetEnvValue(ctx, aid, "MILVUS_DB"); mdb != "" {
			t.Fatalf("milvus shared 不应注入 MILVUS_DB，得 %q", mdb)
		}
	}
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a1)
	if len(binds) != 1 || binds[0].Status != StatusBound || binds[0].IsolationToken != pf1 ||
		binds[0].Strategy != ModeShared || binds[0].ServiceInstanceID != "svinst-milvus-shared-28" {
		t.Fatalf("a1 binding 应 shared/bound token=%s inst=svinst-milvus-shared-28，得 %+v", pf1, binds)
	}
	if fl.calls != 0 {
		t.Fatalf("milvus shared 不应调 flusher，得 %d", fl.calls)
	}
}

// TestReconcile_sharedMilvus_idempotent 同 app 重部署：前缀不变、不重生、flusher 不被调、仍 bound。
func TestReconcile_sharedMilvus_idempotent(t *testing.T) {
	r, appStore, db, fl, _ := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "mshidem", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: milvus\n    strategy: shared\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	pf1, _ := appStore.GetEnvValue(ctx, a.ID, "MILVUS_COLLECTION_PREFIX")
	fl.calls = 0
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir) // 重部署
	pf2, _ := appStore.GetEnvValue(ctx, a.ID, "MILVUS_COLLECTION_PREFIX")
	if pf1 != pf2 {
		t.Fatalf("重部署前缀应不变，%q → %q", pf1, pf2)
	}
	if fl.calls != 0 {
		t.Fatalf("重部署复用不应调 flusher，得 %d 次", fl.calls)
	}
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 1 || binds[0].Status != StatusBound {
		t.Fatalf("重部署应仍 bound，得 %+v", binds)
	}
}

// TestReconcile_sharedMilvus_noInstance 无 shared milvus 实例 → failed。
func TestReconcile_sharedMilvus_noInstance(t *testing.T) {
	r, appStore, db, _, _ := newReconcilerTest(t)
	// 删掉 shared milvus 种子（ensureSeed 插的）；后续测试的 ensureSeed 会幂等补回
	if _, err := db.Exec(`DELETE FROM appdeploy_service_instance WHERE id='svinst-milvus-shared-28'`); err != nil {
		t.Fatalf("删种子: %v", err)
	}
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "mshnone", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: milvus\n    strategy: shared\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 1 || binds[0].Status != StatusFailed || binds[0].ServiceInstanceID != "" {
		t.Fatalf("无 shared milvus 实例应 failed + 无实例，得 %+v", binds)
	}
	if ma, _ := appStore.GetEnvValue(ctx, a.ID, "MILVUS_ADDR"); ma != "" {
		t.Fatalf("failed 不应写 MILVUS_ADDR，得 %q", ma)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run:

```bash
cd platform/backend && GOPATH=C:/Users/yxt/go go test ./internal/mwsupply/ -run 'TestReconcile_sharedMilvus$|TestReconcile_sharedMilvus_idempotent|TestReconcile_sharedMilvus_noInstance' -v
```

Expected: `TestReconcile_sharedMilvus` FAIL（当前 `supplyShared` 走 redis 的 `ParseDBRange`，解析 milvus 的 `{"mode":"prefix"}` 失败 → binding=failed，前缀为空）；`noInstance` 可能 PASS（恰好像无实例）；`idempotent` FAIL。

- [ ] **Step 3: 重构 `supplyShared` + 新增分派函数（supply.go）**

在 `platform/backend/internal/mwsupply/supply.go` 中：

**3a.** 把 `supplyShared` 函数（注释 + 函数体，约 100-134 行）整体替换为下面这版（新分配段改为调 `allocAndClaimShared`）：

```go
// supplyShared shared 供给（按 kind 分派）：复用判定（幂等不换 token 不 flush）/ 新分配（按 kind 选 token → claim → env）。
// redis：db 号池（ParseDBRange + pickLowestFree + claimWithRetry 的 flush+重试）。
// milvus：随机 collection 前缀（无 flush、无池；极罕见撞号重生）。
func (r *Reconciler) supplyShared(ctx context.Context, appID, psID string, dep DepService,
	mkBind func(status, instID, token, lastErr string)) {
	inst, err := r.store.LookupShared(ctx, dep.Kind)
	if err != nil || inst == nil {
		mkBind(StatusFailed, "", "", "无 shared "+dep.Kind+" 实例")
		return
	}
	// 复用：同 app 已 bound 同实例同 token → 不换 token、不 flush（保数据）、重写 env。
	if existing, e := r.store.GetBinding(ctx, appID, dep.Kind); e == nil && existing != nil &&
		existing.Status == StatusBound && existing.IsolationToken != "" && existing.ServiceInstanceID == inst.ID {
		r.writeSharedEnv(ctx, appID, inst, existing.IsolationToken)
		mkBind(StatusBound, inst.ID, existing.IsolationToken, "")
		return
	}
	// 新分配 + claim（按 kind 分派）
	token, err := r.allocAndClaimShared(ctx, appID, psID, dep.Kind, inst)
	if err != nil {
		mkBind(StatusFailed, inst.ID, "", err.Error())
		return
	}
	r.writeSharedEnv(ctx, appID, inst, token)
	mkBind(StatusBound, inst.ID, token, "")
}

// allocAndClaimShared 按 kind 分派 shared token 分配 + claim。
// redis：db 号池；milvus：随机 collection 前缀。
func (r *Reconciler) allocAndClaimShared(ctx context.Context, appID, psID, kind string, inst *ServiceInstance) (string, error) {
	if kind == "milvus" {
		return r.allocMilvusPrefix(ctx, appID, psID, kind, inst)
	}
	return r.allocRedisDB(ctx, appID, psID, kind, inst)
}

// allocRedisDB redis shared db 号分配：ParseDBRange + pickLowestFree + claimWithRetry（flush + 有界重试）。
// 逐字=重构前 supplyShared 的新分配段（零回归）。
func (r *Reconciler) allocRedisDB(ctx context.Context, appID, psID, kind string, inst *ServiceInstance) (string, error) {
	lo, hi, ok := ParseDBRange(inst.Isolation)
	if !ok {
		return "", fmt.Errorf("shared 实例 isolation 缺 db_range")
	}
	allocated, _ := r.store.AllocatedTokens(ctx, inst.ID)
	first, found := pickLowestFree(lo, hi, allocated)
	if !found {
		return "", fmt.Errorf("shared redis db 号耗尽（池 %d-%d）", lo, hi)
	}
	return r.claimWithRetry(ctx, appID, psID, kind, inst, lo, hi, first, allocated)
}

// allocMilvusPrefix milvus shared collection 前缀分配：生成随机唯一前缀 + 单次 claim。
// 无 flush（无前缀级原语）、无有限池（前缀随机生成）；极罕见并发撞号（uq_svbind_inst_token 抛 23505）换号重生，有界 ≤4。
func (r *Reconciler) allocMilvusPrefix(ctx context.Context, appID, psID, kind string, inst *ServiceInstance) (string, error) {
	allocated, _ := r.store.AllocatedTokens(ctx, inst.ID)
	taken := make(map[string]bool, len(allocated))
	for _, t := range allocated {
		taken[t] = true
	}
	for attempts := 0; attempts < 4; attempts++ {
		token := genMilvusPrefix()
		if taken[token] {
			continue
		}
		err := r.store.ClaimSharedToken(ctx, appID, psID, kind, inst.ID, token, EnvKeyFor(kind))
		if err == nil {
			return token, nil
		}
		if !isUniqueViolation(err) {
			return "", err
		}
		taken[token] = true
	}
	return "", fmt.Errorf("milvus 前缀分配重试用尽（并发撞号）")
}
```

> `claimWithRetry`（约 138-169 行）**不动**——仍只被 `allocRedisDB` 调（redis 专属 flush + 有界重试）。

**3b.** 把 `writeSharedEnv` 函数（注释 + 函数体，约 171-179 行）整体替换为下面这版（加 milvus 分支）：

```go
// writeSharedEnv 写 shared env（按 kind 分派），均 source=platform。
// redis：REDIS_ADDR + REDIS_DB（+ REDIS_PASSWORD 若鉴权）。
// milvus：MILVUS_ADDR + MILVUS_COLLECTION_PREFIX（无 password，v1 无 auth；无 db token）。
func (r *Reconciler) writeSharedEnv(ctx context.Context, appID string, inst *ServiceInstance, token string) {
	if inst.Kind == "milvus" {
		_ = r.env.UpsertEnv(ctx, appID, "MILVUS_ADDR", ConnStr(inst), false, "platform")
		_ = r.env.UpsertEnv(ctx, appID, "MILVUS_COLLECTION_PREFIX", token, false, "platform")
		return
	}
	kindUp := strings.ToUpper(inst.Kind) // redis→REDIS
	_ = r.env.UpsertEnv(ctx, appID, kindUp+"_ADDR", ConnStr(inst), false, "platform")
	_ = r.env.UpsertEnv(ctx, appID, kindUp+"_DB", token, false, "platform")
	if inst.AuthRef != "" {
		_ = r.env.UpsertEnv(ctx, appID, kindUp+"_PASSWORD", inst.AuthRef, true, "platform")
	}
}
```

- [ ] **Step 4: 跑 milvus shared 测试确认通过**

Run:

```bash
cd platform/backend && GOPATH=C:/Users/yxt/go go test ./internal/mwsupply/ -run 'TestReconcile_sharedMilvus$|TestReconcile_sharedMilvus_idempotent|TestReconcile_sharedMilvus_noInstance' -v
```

Expected: 三个均 PASS

- [ ] **Step 5: 跑 redis shared 零回归（硬性闸门）**

Run:

```bash
cd platform/backend && GOPATH=C:/Users/yxt/go go test ./internal/mwsupply/ -run 'TestReconcile_sharedRedis$|TestReconcile_shared_idempotent|TestReconcile_shared_flushFailBestEffort|TestReconcile_shared_poolExhaust' -v
```

Expected: 四个均 PASS（redis 路径逐字保留：db 号分配/复用不 flush/flush best-effort/池满 全不变）

- [ ] **Step 6: 跑整个 mwsupply 包回归**

Run:

```bash
cd platform/backend && GOPATH=C:/Users/yxt/go go test ./internal/mwsupply/ -v
```

Expected: 全绿（bind_existing / shared redis / shared milvus / dedicated redis / dedicated milvus / Cleanup / store / naming 全过）

- [ ] **Step 7: 全量串行回归（确认无跨包污染）**

Run:

```bash
cd platform/backend && GOPATH=C:/Users/yxt/go go test -p 1 ./...
```

Expected: 全绿（记忆 `go-test-serial-p1`：`-p 1` 防并发污染 anp_test）

- [ ] **Step 8: 提交**

```bash
cd platform/backend && git add internal/mwsupply/supply.go internal/mwsupply/supply_test.go
git commit -m "$(cat <<'EOF'
feat(mwsupply): supplyShared kind 分派 + milvus shared 前缀隔离 (P5)

supplyShared 新分配段抽 allocAndClaimShared(redis allocRedisDB 逐字保留
/ milvus allocMilvusPrefix 随机 app<12hex>_ + 单次 claim + 有界重试);
writeSharedEnv 加 milvus 分支(MILVUS_ADDR+MILVUS_COLLECTION_PREFIX)。
redis shared 零回归。

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
)"
```

---

## Self-Review

**1. Spec coverage**（逐条对 spec）：

- §3 隔离机制/collection 前缀 → Task 1 `genMilvusPrefix` + Task 3 `allocMilvusPrefix` ✓
- §3 token 不复用 / 随机生成 / 有界重试 ≤4 → Task 3 `allocMilvusPrefix` ✓
- §3 共享实例复用 .28:19530 → Task 2 迁移种子 ✓
- §3 env 注入 MILVUS_ADDR + MILVUS_COLLECTION_PREFIX → Task 3 `writeSharedEnv` milvus 分支 ✓
- §3 回收 CASCADE / Delete 零改 → 无需任务（schema 已有，既有 redis shared 测试覆盖 CASCADE 形状；milvus 等价）✓
- §3 泛化 kind 分派 / redis 逐字保留 → Task 3 `allocRedisDB` + `writeSharedEnv` 默认分支 + Step 5 零回归闸门 ✓
- §4 迁移 000033 → Task 2 ✓
- §5 供给流程三处分派 → Task 3 ✓
- §6 app 配合契约 → 文档性（spec 已记），无代码任务 ✓
- §8 naming.go `genMilvusPrefix` → Task 1 ✓
- §10.1 单测：naming 前缀格式/唯一（Task 1）、迁移种子 + LookupShared（Task 2）、milvus shared 新分配/隔离/幂等/无实例 + redis 零回归（Task 3）✓
  - spec §10.1#4「claim 失败/重试用尽」**不单测**：测试 harness 用真 store（`newReconcilerTest` 无法 mock `ClaimSharedToken`），unique-violation 是并发态、无法确定性单测；retry 逻辑由代码审查 + `noInstance` failed 路径覆盖。已在 Task 3 体现（无占位测试）。
- §13 验收标准 1-8：种子/隔离/env/回收/幂等/可达/零回归/平台保护——除「可达」（e2e）与「平台保护 409」（既有 source=platform 机制，非本期改）外，单测全覆盖；可达 + 409 属 e2e（spec §10.2，本计划范围外）✓

**2. Placeholder scan**：无 TBD/TODO；每个 step 含真实代码/命令/预期 ✓

**3. Type consistency**：

- `genMilvusPrefix() string`（Task 1 定义）↔ Task 3 `allocMilvusPrefix` 调用 ✓
- `allocAndClaimShared` / `allocRedisDB` / `allocMilvusPrefix` 签名一致（`(ctx, appID, psID, kind, inst) (string, error)`）✓
- `writeSharedEnv(ctx, appID, inst, token)` 签名重构前后一致（supplyShared 复用分支与新分配分支都调）✓
- 种子 id `svinst-milvus-shared-28` 在迁移（Task 2）、ensureSeed（Task 2）、测试断言（Task 3）一致 ✓
- env key `MILVUS_COLLECTION_PREFIX` 在 `writeSharedEnv`（Task 3）与测试断言（Task 3）一致 ✓

无问题。

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-08-03-shared-milvus.md`. Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?

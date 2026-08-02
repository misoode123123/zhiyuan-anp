# 中间件依赖注入 P3 —— dedicated 专属 Redis（每 app 一个容器）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让声明 `strategy: dedicated` 的 redis 依赖为该 app 起一个专属 redis 容器（端口池分配 + requirepass），注入 `REDIS_ADDR`+`REDIS_PASSWORD`，删 app 时 docker rm 容器。

**Architecture:** 复用 P1/P2 的 mwsupply 范式 + 对称 pgsupply。`supplyOne` 加 dedicated 分支 → `supplyDedicated`：复用判定（同 app 已有 dedicated 实例则不重启/不换端口/保数据）或新供给（`allocPort` 9600-9699 → `RunRedisContainer` 起 `redis:7-alpine`+requirepass → `ReadyChecker.Ping` 轮询至通 → `CreateInstance` 登记实例行带 container_name → 写 `REDIS_ADDR`+`REDIS_PASSWORD`）。删 app 靠 `MWReconciler.Cleanup`（接口新增）→ 查 dedicated binding → `docker rm -f` 容器 + 删 instance 行；Delete handler 在 pgsupply.Cleanup 后调用。dedicated 无 flusher（全新空容器）→ 天然避开 P2 的 .28 backend↔redis 不可达坑。

**Tech Stack:** Go 1.25、pgx/v5、sqlx、PG（anp_test 库）、TDD。

**关联 spec：** [`docs/superpowers/specs/2026-08-02-中间件依赖注入-P3-dedicated-redis-design.md`](../specs/2026-08-02-中间件依赖注入-P3-dedicated-redis-design.md)

---

## Global Constraints

> 本节为 spec 的项目级硬约束，**每个任务的 requirements 隐式包含本节**。

- **禁 SQLite，只用 PG**：所有测试连 `.28 anp_test` 库（`testutil.TestDB`），不回退 sqlite（memory `no-sqlite-pg-only`）。
- **go test 串行**：`go test` 必带 `-p 1`（并发污染 `anp_test` 库；memory `go-test-serial-p1`）。
- **GOPATH 前缀**：所有 `go` 命令前缀 `GOPATH=C:/Users/yxt/go`（本机 GOPATH 被污染成 go.exe 路径；memory `gopath-pollution-windows`）。
- **工作目录**：所有 `go` 命令在 `platform/backend` 下执行。
- **PG 驱动**：pgx/v5；唯一约束冲突检测用 `errors.As(err, &pgErr)` + `pgErr.Code == "23505"`（`*pgconn.PgError`，import `github.com/jackc/pgx/v5/pgconn`）。
- **端口池**：redis dedicated `[9600,9699]`（PG 占 9500-9599，避开）；池满即 `failed`（配额即端口池，不动 `internal/quota`）。
- **best-effort**：`Reconcile` 总不返回错、不阻塞部署（沿用 P1/P2）；`Cleanup` 总返回 nil、不阻塞 Delete。
- **env 保护**：所有平台注入 env 行 `source=platform`（409 保护已有，复用）。
- **dedicated 不注入 `REDIS_DB`**（整实例用默认 db 0；REDIS_DB 是 shared 专属）。
- **不改**：shared/bind_existing 分支、`Deployer`/`EnvPairs` 主流程、pgsupply；`handler.go` 仅加接口方法 + 1 处 Delete 调用；`main.go` 仅 `NewReconciler` 多传参。
- **TDD**：每任务先写失败测试 → 跑红 → 最小实现 → 跑绿 → commit。frequent commits。

---

## File Structure

| 文件                                                           | 责任                                                                                                                                                 | 动作       |
| -------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- | ---------- |
| `internal/db/migrations/pg/000030_mwsupply_dedicated.up.sql`   | ALTER 加 container_name 列                                                                                                                           | 新建       |
| `internal/db/migrations/pg/000030_mwsupply_dedicated.down.sql` | 回滚                                                                                                                                                 | 新建       |
| `internal/mwsupply/model.go`                                   | `ServiceInstance`+`ContainerName`；`instCols`+列                                                                                                     | 改         |
| `internal/mwsupply/naming.go`                                  | `genShortID`/`genPassword`/`allocPort`/`dedicatedContainerName` + 端口/镜像/就绪超时常量                                                             | 新建       |
| `internal/mwsupply/naming_test.go`                             | 上述纯逻辑单测                                                                                                                                       | 新建       |
| `internal/mwsupply/redisflush.go`                              | 抽 `dialRedis`+`pingConn` 共享 helper；`redisFlusher` 实现 `DBFlusher`+`ReadyChecker`（+`Ping`）；flush 零回归                                       | 改         |
| `internal/mwsupply/redisflush_test.go`                         | flush 回归 + pingConn 单测                                                                                                                           | 改         |
| `internal/mwsupply/docker.go`                                  | `MWDockerRunner` 接口 + osDocker（UsedPorts/RunRedisContainer/RmForce）+ `redisRunArgs` 纯函数 + `NewOSDocker()`                                     | 新建       |
| `internal/mwsupply/docker_test.go`                             | `redisRunArgs` 纯函数单测                                                                                                                            | 新建       |
| `internal/mwsupply/store.go`                                   | +`CreateInstance`/`GetInstance(id)`/`DeleteInstance`                                                                                                 | 改         |
| `internal/mwsupply/store_test.go`                              | +dedicated store 单测 + 迁移 000030 校验                                                                                                             | 改         |
| `internal/mwsupply/supply.go`                                  | `Reconciler`+`ready`/`docker`/`host` 字段；`NewReconciler` 新签名；`supplyOne`+`case ModeDedicated`；`supplyDedicated`/`writeDedicatedEnv`/`Cleanup` | 改         |
| `internal/mwsupply/supply_test.go`                             | `newReconcilerTest` 加 fakeDocker+host + 全部调用点改 + dedicated/cleanup 单测                                                                       | 改         |
| `cmd/server/main.go`                                           | `NewReconciler` 多传 `NewOSDocker()`+`cfg.AppDeployHost`（probe 双用）                                                                               | 改（1 行） |
| `internal/appdeploy/handler.go`                                | `MWReconciler`+`Cleanup`；Delete 调 `h.mwReconciler.Cleanup`                                                                                         | 改         |
| `internal/appdeploy/handler_http_test.go`                      | `fakeMWReconciler`+`Cleanup`                                                                                                                         | 改         |

---

## Task 1: 迁移 000030（container_name 列）+ model.go（ContainerName 字段）

**Files:**

- Create: `platform/backend/internal/db/migrations/pg/000030_mwsupply_dedicated.up.sql`
- Create: `platform/backend/internal/db/migrations/pg/000030_mwsupply_dedicated.down.sql`
- Modify: `platform/backend/internal/mwsupply/model.go`（`ServiceInstance` 加字段 + `instCols` 加列）
- Test: `platform/backend/internal/mwsupply/store_test.go`（加迁移校验 + ContainerName 回归）

**Interfaces:**

- Produces: `appdeploy_service_instance.container_name TEXT` 列；`ServiceInstance.ContainerName` 字段 + `instCols` 含 `container_name`（后续 task 的 SELECT/INSERT 依赖）

- [ ] **Step 1: 写失败测试**

追加到 `internal/mwsupply/store_test.go` 末尾（`contains` 函数之前）：

```go
// TestMigration_000030_containerNameColumn 迁移后：service_instance 有 container_name 列；
// 既有 bind_existing/shared 种子行该列为 NULL → LookupBindExisting 取回 ContainerName==""（instCols 回归）。
func TestMigration_000030_containerNameColumn(t *testing.T) {
	s, db := newTestStore(t)
	// 列存在
	var hasCol bool
	if err := db.Get(&hasCol, `SELECT EXISTS(SELECT 1 FROM information_schema.columns
		WHERE table_name='appdeploy_service_instance' AND column_name='container_name')`); err != nil {
		t.Fatalf("查列: %v", err)
	}
	if !hasCol {
		t.Fatal("container_name 列应存在（迁移 000030）")
	}
	// instCols 含 container_name：LookupBindExisting 取回的种子行 ContainerName 为空（NULL→COALESCE '')
	got, err := s.LookupBindExisting(context.Background(), "ps_1", "redis")
	if err != nil || got == nil {
		t.Fatalf("应命中 redis 种子，err=%v got=%+v", err, got)
	}
	if got.ContainerName != "" {
		t.Fatalf("bind_existing 种子 ContainerName 应空，得 %q", got.ContainerName)
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run（from `platform/backend`）:

```
GOPATH=C:/Users/yxt/go go test -p 1 -run TestMigration_000030_containerNameColumn ./internal/mwsupply/
```

Expected: FAIL（列不存在 / `got.ContainerName` undefined）。

- [ ] **Step 3: 写迁移 up**

`internal/db/migrations/pg/000030_mwsupply_dedicated.up.sql`：

```sql
-- 000030_mwsupply_dedicated.up.sql
-- P3：dedicated 专属 redis（每 app 一个容器）
-- 加 container_name 列：dedicated 实例的容器名（Cleanup 时 docker rm 用）。
-- nullable：bind_existing/shared 种子行及非 dedicated 实例恒 NULL，无影响。

ALTER TABLE appdeploy_service_instance ADD COLUMN IF NOT EXISTS container_name TEXT;
```

- [ ] **Step 4: 写迁移 down**

`internal/db/migrations/pg/000030_mwsupply_dedicated.down.sql`：

```sql
-- 000030_mwsupply_dedicated.down.sql
ALTER TABLE appdeploy_service_instance DROP COLUMN IF EXISTS container_name;
```

- [ ] **Step 5: 改 model.go（ServiceInstance + instCols）**

在 `internal/mwsupply/model.go` 的 `ServiceInstance` 结构体的 `Isolation` 字段后加 `ContainerName` 字段：

```go
	Isolation      string   `json:"isolation,omitempty" db:"isolation"`     // raw jsonb text（shared 用）
	ContainerName  string   `json:"container_name,omitempty" db:"container_name"` // dedicated 容器名（Cleanup docker rm 用）
	Status         string   `json:"status" db:"status"`
```

并改 `store.go` 顶部的 `instCols` 常量（加 `container_name`，沿用 COALESCE 防 NULL）：

```go
const instCols = `id, project_space_id, kind, name, supply_mode, host, port,
 COALESCE(auth_ref,'') AS auth_ref, COALESCE(isolation::text,'') AS isolation,
 COALESCE(container_name,'') AS container_name, status, created_at, updated_at`
```

> 注：`instCols` 定义在 `store.go`（不在 model.go）；此处改 store.go 的常量。

- [ ] **Step 6: 跑测试验证通过**

Run:

```
GOPATH=C:/Users/yxt/go go test -p 1 -run TestMigration_000030_containerNameColumn ./internal/mwsupply/
```

Expected: PASS（`testutil.TestDB` 自动 apply 000030；既有 store 测试不回归——instCols 加列对已有 SELECT 透明）。

- [ ] **Step 7: 顺跑既有 store 测试确认零回归**

```
GOPATH=C:/Users/yxt/go go test -p 1 -run 'TestStore_|TestMigration_' ./internal/mwsupply/
```

Expected: PASS（含 000029/000030 迁移 + LookupBindExisting/Shared/UpsertBinding 全绿）。

- [ ] **Step 8: Commit**

```bash
git add platform/backend/internal/db/migrations/pg/000030_mwsupply_dedicated.up.sql \
        platform/backend/internal/db/migrations/pg/000030_mwsupply_dedicated.down.sql \
        platform/backend/internal/mwsupply/model.go \
        platform/backend/internal/mwsupply/store.go \
        platform/backend/internal/mwsupply/store_test.go
git commit -m "feat(mwsupply): 迁移000030 container_name列+ServiceInstance字段(P3 dedicated)"
```

---

## Task 2: naming.go（纯逻辑 + 常量）

**Files:**

- Create: `platform/backend/internal/mwsupply/naming.go`
- Test: `platform/backend/internal/mwsupply/naming_test.go`

**Interfaces:**

- Produces:
  - `func genShortID() string` —— 12 位 hex（crypto/rand）
  - `func genPassword() string` —— 32 位 hex（crypto/rand）
  - `func allocPort(used map[int]struct{}, min, max int) int` —— [min,max] 内首个未占用；无则 0
  - `func dedicatedContainerName(short string) string` —— `"mwredis-" + short`
  - 常量 `mwPortMin=9600` / `mwPortMax=9699` / `redisImage="redis:7-alpine"` / `redisInternalPort=6379` / `readyTimeout`（15s）

> 与 pgsupply 的 `genShortID`/`genPassword`/`allocPort` 同款纯函数副本（pgsupply 的不导出，跨包 import 不值；mwsupply 自包含）。

- [ ] **Step 1: 写失败测试**

`internal/mwsupply/naming_test.go`：

```go
package mwsupply

import (
	"testing"
	"time"
)

func TestGenShortID(t *testing.T) {
	a, b := genShortID(), genShortID()
	if len(a) != 12 || len(b) != 12 {
		t.Fatalf("genShortID 应 12 字符，得 %d/%d", len(a), len(b))
	}
	if a == b {
		t.Fatal("两次 genShortID 不应相同")
	}
}

func TestGenPassword(t *testing.T) {
	p := genPassword()
	if len(p) != 32 {
		t.Fatalf("genPassword 应 32 字符，得 %d", len(p))
	}
	for _, c := range p {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("genPassword 应 hex，含 %q", c)
		}
	}
}

func TestAllocPort(t *testing.T) {
	// 空池 → min
	if p := allocPort(map[int]struct{}{}, mwPortMin, mwPortMax); p != mwPortMin {
		t.Fatalf("空池应得 %d，得 %d", mwPortMin, p)
	}
	// 占了 9600 → 9601
	used := map[int]struct{}{9600: {}}
	if p := allocPort(used, mwPortMin, mwPortMax); p != 9601 {
		t.Fatalf("占 9600 应得 9601，得 %d", p)
	}
	// 全占 → 0
	full := map[int]struct{}{}
	for p := mwPortMin; p <= mwPortMax; p++ {
		full[p] = struct{}{}
	}
	if p := allocPort(full, mwPortMin, mwPortMax); p != 0 {
		t.Fatalf("全占应 0，得 %d", p)
	}
}

func TestDedicatedContainerName(t *testing.T) {
	if n := dedicatedContainerName("abc123"); n != "mwredis-abc123" {
		t.Fatalf("容器名应 mwredis-abc123，得 %q", n)
	}
}

func TestConstants(t *testing.T) {
	if mwPortMin != 9600 || mwPortMax != 9699 {
		t.Fatalf("端口池应 9600-9699，得 %d-%d", mwPortMin, mwPortMax)
	}
	if redisImage != "redis:7-alpine" || redisInternalPort != 6379 {
		t.Fatalf("镜像/端口不符: %s/%d", redisImage, redisInternalPort)
	}
	if readyTimeout != 15*time.Second {
		t.Fatalf("readyTimeout 应 15s，得 %v", readyTimeout)
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run:

```
GOPATH=C:/Users/yxt/go go test -p 1 -run 'TestGenShortID|TestGenPassword|TestAllocPort|TestDedicatedContainerName|TestConstants' ./internal/mwsupply/
```

Expected: FAIL（符号 undefined）。

- [ ] **Step 3: 写实现**

`internal/mwsupply/naming.go`：

```go
package mwsupply

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// dedicated redis 供给常量。
const (
	mwPortMin        = 9600 // redis dedicated 端口池下界（PG 占 9500-9599，避开）
	mwPortMax        = 9699 // 上界（100 槽；池满即配额超限 failed）
	redisImage       = "redis:7-alpine"
	redisInternalPort = 6379
	readyTimeout     = 15 * time.Second // 就绪检测轮询上限
)

// genShortID 生成 12 位 hex 短 ID（crypto/rand）。
func genShortID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// genPassword 随机 32 位 hex 密码（dedicated redis requirepass）。
func genPassword() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// allocPort 在 [min,max] 选首个未占用端口；无可用返回 0。纯函数，可单测。
func allocPort(used map[int]struct{}, min, max int) int {
	for p := min; p <= max; p++ {
		if _, ok := used[p]; !ok {
			return p
		}
	}
	return 0
}

// dedicatedContainerName 拼 dedicated redis 容器名：mwredis-<short>。
func dedicatedContainerName(short string) string { return "mwredis-" + short }
```

- [ ] **Step 4: 跑测试验证通过**

Run:

```
GOPATH=C:/Users/yxt/go go test -p 1 -run 'TestGenShortID|TestGenPassword|TestAllocPort|TestDedicatedContainerName|TestConstants' ./internal/mwsupply/
```

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add platform/backend/internal/mwsupply/naming.go platform/backend/internal/mwsupply/naming_test.go
git commit -m "feat(mwsupply): naming 纯逻辑 genShortID/genPassword/allocPort/dedicatedContainerName+常量"
```

---

## Task 3: redisflush.go 重构（抽 dialRedis/pingConn 共享 helper，redisFlusher 加 Ping）

**Files:**

- Modify: `platform/backend/internal/mwsupply/redisflush.go`
- Test: `platform/backend/internal/mwsupply/redisflush_test.go`（flush 回归 + pingConn/Ping 单测）

**Interfaces:**

- Consumes: 既有 `writeCmd`/`readOK`（保留）
- Produces:
  - `type ReadyChecker interface { Ping(ctx context.Context, host string, port int, password string) error }`
  - `func dialRedis(ctx context.Context, host string, port int, password string) (conn net.Conn, err error)` —— dial + 可选 AUTH
  - `func pingConn(rw io.ReadWriter) error` —— 发 PING 读 +PONG
  - `redisFlusher` 实现 `Ping`（轮询 dialRedis+pingConn 至通或超时）；`FlushDB` 改用 dialRedis+flushConn（**行为不变**）

> 抽 helper 不改 flush 语义（shared 零回归）。`flushConn` 签名改为 `(rw, db)`（AUTH 移入 dialRedis）。

- [ ] **Step 1: 改测试（先确保既有 flush 用例仍绿 + 加 ping 用例）**

在 `internal/mwsupply/redisflush_test.go` 末尾追加（既有 3 个 flush 用例**不动**，验证零回归）：

```go
// TestRedisPing_ok 拨假 redis 发 PING 读 +PONG。
func TestRedisPing_ok(t *testing.T) {
	addr, _, closer := startFakeRedis(t) // 假 redis 每条命令回 +OK（含 PING）
	defer closer()
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	if err := NewRedisFlusher().Ping(context.Background(), host, port, ""); err != nil {
		t.Fatalf("Ping 应成功（假 redis 回 +OK），得 %v", err)
	}
}

// TestRedisPing_withAuth 有密码时先 AUTH 再 PING。
func TestRedisPing_withAuth(t *testing.T) {
	addr, got, closer := startFakeRedis(t)
	defer closer()
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	if err := NewRedisFlusher().Ping(context.Background(), host, port, "secret"); err != nil {
		t.Fatalf("Ping(有密码) 应成功，得 %v", err)
	}
	// 应只见 AUTH（PING 也发但 +OK 即成功）；至少 AUTH 在
	found := false
	for _, c := range *got {
		if c == "AUTH secret" {
			found = true
		}
	}
	if !found {
		t.Fatalf("应有 AUTH secret，得 %v", *got)
	}
}

// TestRedisPing_unreachable 不可达 → Ping 返错（轮询超时）。
func TestRedisPing_unreachable(t *testing.T) {
	// 拨一个保留端口（无人监听）→ dial 失败 → 轮询至 ctx 超时
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := NewRedisFlusher().Ping(ctx, "127.0.0.1", 1, "") // port 1 不可达
	if err == nil {
		t.Fatal("不可达应收错")
	}
}
```

> `TestRedisPing_unreachable` 用 500ms ctx 控制轮询时长，避免等满 15s。

- [ ] **Step 2: 跑测试验证失败**

Run:

```
GOPATH=C:/Users/yxt/go go test -p 1 -run 'TestRedisPing' ./internal/mwsupply/
```

Expected: FAIL（`Ping` undefined / `ReadyChecker` undefined）。

- [ ] **Step 3: 重写 redisflush.go（抽 helper + Ping，flush 零回归）**

整体替换 `internal/mwsupply/redisflush.go` 为：

```go
package mwsupply

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// DBFlusher 清空指定 redis db（shared 重分配时保证干净隔离位）。
type DBFlusher interface {
	FlushDB(ctx context.Context, host string, port int, password string, db int) error
}

// ReadyChecker 探测 redis 是否就绪（dedicated 起容器后轮询 AUTH+PING 至通）。
type ReadyChecker interface {
	Ping(ctx context.Context, host string, port int, password string) error
}

// NewRedisFlusher 构造裸 RESP 实现（net.Dial，不引 go-redis/redigo）；同时满足 DBFlusher + ReadyChecker。
func NewRedisFlusher() *redisFlusher { return &redisFlusher{} }

type redisFlusher struct{}

// FlushDB 连 redis（dialRedis 含可选 AUTH），发 SELECT <db>、FLUSHDB。
func (f *redisFlusher) FlushDB(ctx context.Context, host string, port int, password string, db int) error {
	conn, err := dialRedis(ctx, host, port, password)
	if err != nil {
		return err
	}
	defer conn.Close()
	return flushConn(conn, db)
}

// Ping 轮询 dialRedis+pingConn 至成功或 ctx 超时（dedicated 就绪检测）。
// 不可达（如 .28 backend 拨不到 dedicated 容器）→ 返 ctx 超时错，调用方据此判 failed。
func (f *redisFlusher) Ping(ctx context.Context, host string, port int, password string) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	// 首次立即试
	if err := pingOnce(ctx, host, port, password); err == nil {
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("redis 就绪检测超时: %w", ctx.Err())
		case <-ticker.C:
			if err := pingOnce(ctx, host, port, password); err == nil {
				return nil
			}
		}
	}
}

// pingOnce 拨一次 + AUTH(可选) + PING。
func pingOnce(ctx context.Context, host string, port int, password string) error {
	conn, err := dialRedis(ctx, host, port, password)
	if err != nil {
		return err
	}
	defer conn.Close()
	return pingConn(conn)
}

// dialRedis 建立 TCP 连接（带 ctx 超时）+ 可选 AUTH。返回已认证的连接。
func dialRedis(ctx context.Context, host string, port int, password string) (net.Conn, error) {
	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("dial redis: %w", err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if password != "" {
		bw := bufio.NewWriter(conn)
		br := bufio.NewReader(conn)
		if err := writeCmd(bw, "AUTH", password); err != nil {
			conn.Close()
			return nil, err
		}
		if err := readOK(br); err != nil {
			conn.Close()
			return nil, fmt.Errorf("AUTH: %w", err)
		}
	}
	return conn, nil
}

// flushConn 在已建连（已 AUTH）上发 SELECT <db>、FLUSHDB。
func flushConn(rw io.ReadWriter, db int) error {
	bw := bufio.NewWriter(rw)
	br := bufio.NewReader(rw)
	if err := writeCmd(bw, "SELECT", strconv.Itoa(db)); err != nil {
		return err
	}
	if err := readOK(br); err != nil {
		return fmt.Errorf("SELECT %d: %w", db, err)
	}
	if err := writeCmd(bw, "FLUSHDB"); err != nil {
		return err
	}
	if err := readOK(br); err != nil {
		return fmt.Errorf("FLUSHDB: %w", err)
	}
	return nil
}

// pingConn 在已建连（已 AUTH）上发 PING，读 +PONG（+OK 也算成功，假 redis 用）。
func pingConn(rw io.ReadWriter) error {
	bw := bufio.NewWriter(rw)
	br := bufio.NewReader(rw)
	if err := writeCmd(bw, "PING"); err != nil {
		return err
	}
	line, err := br.ReadString('\n')
	if err != nil {
		return fmt.Errorf("读 PING 回包: %w", err)
	}
	if len(line) == 0 || (line[0] != '+' && line[0] != '-') {
		return fmt.Errorf("非预期 PING 回包: %q", line)
	}
	if line[0] == '-' {
		return fmt.Errorf("redis PING: %s", strings.TrimSpace(line[1:]))
	}
	return nil // +PONG / +OK 均视作就绪
}

// writeCmd 写一条 RESP 命令：*N\r\n + N×($len\r\n arg \r\n)。
func writeCmd(bw *bufio.Writer, args ...string) error {
	if _, err := fmt.Fprintf(bw, "*%d\r\n", len(args)); err != nil {
		return err
	}
	for _, a := range args {
		if _, err := fmt.Fprintf(bw, "$%d\r\n%s\r\n", len(a), a); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// readOK 读一个 RESP 回包，要求 simple-string +OK；-ERR / 其他 → 错。
func readOK(br *bufio.Reader) error {
	line, err := br.ReadString('\n')
	if err != nil {
		return fmt.Errorf("读回包: %w", err)
	}
	if len(line) == 0 {
		return fmt.Errorf("空回包")
	}
	switch line[0] {
	case '+':
		return nil
	case '-':
		return fmt.Errorf("redis: %s", strings.TrimSpace(line[1:]))
	default:
		return fmt.Errorf("非预期回包首字节 %q: %q", string(line[0]), line)
	}
}
```

> 关键：`FlushDB` 行为与重构前一致（dial + AUTH + SELECT + FLUSHDB）；既有 `TestRedisFlush_NoAuth/WithAuth/ServerError` 验证零回归。`flushConn` 签名由 `(rw, password, db)` 改为 `(rw, db)`（AUTH 移入 dialRedis）——仅包内调用，无外部影响。

- [ ] **Step 4: 跑测试验证通过（flush 回归 + ping）**

Run:

```
GOPATH=C:/Users/yxt/go go test -p 1 -run 'TestRedisFlush|TestRedisPing' ./internal/mwsupply/
```

Expected: PASS（既有 3 个 flush 用例 + 3 个 ping 用例全绿）。

- [ ] **Step 5: Commit**

```bash
git add platform/backend/internal/mwsupply/redisflush.go platform/backend/internal/mwsupply/redisflush_test.go
git commit -m "feat(mwsupply): redisflush 抽 dialRedis/pingConn+ReadyChecker.Ping(flush零回归)"
```

---

## Task 4: docker.go（MWDockerRunner 接口 + osDocker + redisRunArgs 纯函数）

**Files:**

- Create: `platform/backend/internal/mwsupply/docker.go`
- Test: `platform/backend/internal/mwsupply/docker_test.go`

**Interfaces:**

- Consumes: Task 2 常量（`redisImage`/`redisInternalPort`）
- Produces:
  - `type MWDockerRunner interface { UsedPorts(ctx) map[int]struct{}; RunRedisContainer(ctx, name, password string, port int) error; RmForce(ctx, name string) error }`
  - `func NewOSDocker() MWDockerRunner`
  - `func redisRunArgs(name, password string, port int) []string` —— 纯函数（可单测 docker 参数）
  - `func runDockerCmd(ctx, args ...string) (string, error)` —— 调 docker CLI

> `RunRedisContainer` 只 `docker run -d`（**不做就绪检测**——就绪在 supplyDedicated 经 `ReadyChecker.Ping` 做，持有 AppDeployHost；仿 pgsupply：DockerRunner 只起容器，InstanceManager/Reconciler 做就绪）。

- [ ] **Step 1: 写失败测试（redisRunArgs 纯函数 + UsedPorts 解析）**

`internal/mwsupply/docker_test.go`：

```go
package mwsupply

import (
	"testing"
)

func TestRedisRunArgs(t *testing.T) {
	got := redisRunArgs("mwredis-abc", "s3cr3t", 9631)
	want := []string{
		"run", "-d", "--name", "mwredis-abc",
		"-e", "REDIS_PASSWORD=s3cr3t",
		"-p", "9631:6379",
		"--restart", "unless-stopped",
		"redis:7-alpine",
		"redis-server", "--requirepass", "s3cr3t",
	}
	if len(got) != len(want) {
		t.Fatalf("参数数应 %d，得 %d: %v", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("args[%d] 想 %q 得 %q", i, w, got[i])
		}
	}
}

// TestDockerUsedPortsParse 校验 docker ps 端口输出解析（提取宿主 publish 端口）。
func TestDockerUsedPortsParse(t *testing.T) {
	used := parsePortsOutput(`0.0.0.0:9631->6379/tcp
0.0.0.0:9500->5432/tcp, :::9500->5432/tcp
`)
	if _, ok := used[9631]; !ok {
		t.Errorf("应含 9631，得 %v", used)
	}
	if _, ok := used[9500]; !ok {
		t.Errorf("应含 9500，得 %v", used)
	}
	if len(used) != 2 {
		t.Errorf("应 2 端口，得 %d: %v", len(used), used)
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run:

```
GOPATH=C:/Users/yxt/go go test -p 1 -run 'TestRedisRunArgs|TestDockerUsedPortsParse' ./internal/mwsupply/
```

Expected: FAIL（`redisRunArgs`/`parsePortsOutput` undefined）。

- [ ] **Step 3: 写实现**

`internal/mwsupply/docker.go`：

```go
package mwsupply

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
)

// MWDockerRunner 经宿主 docker socket 管理 dedicated 中间件容器。抽接口便于单测用 fake。
type MWDockerRunner interface {
	UsedPorts(ctx context.Context) map[int]struct{}
	RunRedisContainer(ctx context.Context, name, password string, port int) error
	RmForce(ctx context.Context, name string) error
}

// osDocker 默认实现：调 docker CLI。
type osDocker struct{}

// NewOSDocker 构造。
func NewOSDocker() MWDockerRunner { return osDocker{} }

// runDockerCmd 执行 docker 子命令，返回合并输出。
func runDockerCmd(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

var hostPortRe = regexp.MustCompile(`(?::[\d.]+)?:(\d+)->`)

// parsePortsOutput 解析 docker ps --format {{.Ports}} 的输出，提取宿主 publish 端口。
func parsePortsOutput(out string) map[int]struct{} {
	used := map[int]struct{}{}
	for _, line := range regexp.MustCompile(`\r?\n`).Split(out, -1) {
		for _, m := range hostPortRe.FindAllStringSubmatch(line, -1) {
			if len(m) > 1 {
				if p, err := strconv.Atoi(m[1]); err == nil {
					used[p] = struct{}{}
				}
			}
		}
	}
	return used
}

// UsedPorts 查运行中容器占用的宿主端口。
func (osDocker) UsedPorts(ctx context.Context) map[int]struct{} {
	out, _ := runDockerCmd(ctx, "ps", "--format", "{{.Ports}}")
	return parsePortsOutput(out)
}

// redisRunArgs 构造 docker run 参数（纯函数，可单测）。
//   redis:7-alpine + redis-server --requirepass；-p host:6379 publish；--restart unless-stopped 自恢复。
func redisRunArgs(name, password string, port int) []string {
	return []string{
		"run", "-d", "--name", name,
		"-e", "REDIS_PASSWORD=" + password,
		"-p", fmt.Sprintf("%d:%d", port, redisInternalPort),
		"--restart", "unless-stopped",
		redisImage,
		"redis-server", "--requirepass", password,
	}
}

// RunRedisContainer docker run -d 起 dedicated redis 容器（不做就绪检测，由 supplyDedicated 的 ReadyChecker 负责）。
func (osDocker) RunRedisContainer(ctx context.Context, name, password string, port int) error {
	out, err := runDockerCmd(ctx, redisRunArgs(name, password, port)...)
	if err != nil {
		return fmt.Errorf("docker run redis: %w: %s", err, out)
	}
	return nil
}

// RmForce 强删容器（清理失败的供给 / 删 app 回收）。
func (osDocker) RmForce(ctx context.Context, name string) error {
	_, err := runDockerCmd(ctx, "rm", "-f", name)
	return err
}
```

> 注：`-e REDIS_PASSWORD=<pwd>` 仅为容器内可见（可选，应用不读它；requirepass 已在 redis-server 启动参数生效）。保留以备容器内调试。

- [ ] **Step 4: 跑测试验证通过**

Run:

```
GOPATH=C:/Users/yxt/go go test -p 1 -run 'TestRedisRunArgs|TestDockerUsedPortsParse' ./internal/mwsupply/
```

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add platform/backend/internal/mwsupply/docker.go platform/backend/internal/mwsupply/docker_test.go
git commit -m "feat(mwsupply): docker MWDockerRunner接口+osDocker+redisRunArgs纯函数"
```

---

## Task 5: store.go 的 dedicated 方法（CreateInstance/GetInstance/DeleteInstance）

**Files:**

- Modify: `platform/backend/internal/mwsupply/store.go`（追加 3 方法）
- Test: `platform/backend/internal/mwsupply/store_test.go`（追加测试）

**Interfaces:**

- Consumes: Task 1 的 `container_name` 列 + `ServiceInstance.ContainerName` 字段；`instCols`
- Produces:
  - `func (s *Store) CreateInstance(ctx, inst *ServiceInstance) error` —— INSERT（id PK 冲突 DO NOTHING）
  - `func (s *Store) GetInstance(ctx, id string) (*ServiceInstance, error)` —— 无 → nil,nil
  - `func (s *Store) DeleteInstance(ctx, id string) error` —— DELETE（binding/env CASCADE 兜底）

- [ ] **Step 1: 写失败测试**

追加到 `internal/mwsupply/store_test.go` 末尾（`contains` 函数之前）：

```go
// TestStore_CreateGetInstance dedicated 实例行落库 + 取回（含 container_name）。
func TestStore_CreateGetInstance(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	inst := &ServiceInstance{
		ID: "svinst-redis-ded-test1", Kind: "redis", Name: "mwredis-test1",
		SupplyMode: ModeDedicated, Host: "10.10.0.28", Port: 9600,
		AuthRef: "pwd123", ContainerName: "mwredis-test1", Status: "active",
	}
	if err := s.CreateInstance(ctx, inst); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	got, err := s.GetInstance(ctx, inst.ID)
	if err != nil || got == nil {
		t.Fatalf("GetInstance 应取回，err=%v got=%+v", err, got)
	}
	if got.ContainerName != "mwredis-test1" || got.Port != 9600 || got.AuthRef != "pwd123" || got.SupplyMode != ModeDedicated {
		t.Fatalf("dedicated 实例行不符: %+v", got)
	}
	// 无 → nil,nil
	gotNil, err := s.GetInstance(ctx, "nope")
	if err != nil || gotNil != nil {
		t.Fatalf("无实例应 nil,nil，得 %+v err=%v", gotNil, err)
	}
}

// TestStore_DeleteInstance 删实例行。
func TestStore_DeleteInstance(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	inst := &ServiceInstance{
		ID: "svinst-redis-ded-test2", Kind: "redis", Name: "mwredis-test2",
		SupplyMode: ModeDedicated, Host: "h", Port: 9601, ContainerName: "mwredis-test2", Status: "active",
	}
	_ = s.CreateInstance(ctx, inst)
	if err := s.DeleteInstance(ctx, inst.ID); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}
	got, _ := s.GetInstance(ctx, inst.ID)
	if got != nil {
		t.Fatalf("删后应取不到，得 %+v", got)
	}
}

// TestStore_CreateInstance_idempotent 同 id 再 Create 不报错（DO NOTHING）。
func TestStore_CreateInstance_idempotent(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	inst := &ServiceInstance{ID: "svinst-ded-idem", Kind: "redis", Name: "n", SupplyMode: ModeDedicated, Host: "h", Port: 9602, ContainerName: "n", Status: "active"}
	if err := s.CreateInstance(ctx, inst); err != nil {
		t.Fatalf("首次 Create: %v", err)
	}
	if err := s.CreateInstance(ctx, inst); err != nil {
		t.Fatalf("二次 Create 应幂等不报错: %v", err)
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run:

```
GOPATH=C:/Users/yxt/go go test -p 1 -run 'TestStore_CreateGetInstance|TestStore_DeleteInstance|TestStore_CreateInstance_idempotent' ./internal/mwsupply/
```

Expected: FAIL（方法 undefined）。

- [ ] **Step 3: 追加 3 方法到 store.go**

在 `internal/mwsupply/store.go` 的 `ClaimSharedToken` 方法后追加：

```go
// CreateInstance 登记 dedicated 实例行（supply_mode=dedicated，含 container_name）。
// id PK 冲突 → DO NOTHING（幂等；并发兜底见 §9 风险）。
func (s *Store) CreateInstance(ctx context.Context, inst *ServiceInstance) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO appdeploy_service_instance
		   (id, project_space_id, kind, name, supply_mode, host, port, auth_ref, isolation, container_name, status)
		 VALUES ($1,NULLIF($2,''),$3,$4,$5,$6,$7,NULLIF($8,''),NULLIF($9,'')::jsonb,NULLIF($10,''),$11)
		 ON CONFLICT (id) DO NOTHING`,
		inst.ID, inst.ProjectSpaceID, inst.Kind, inst.Name, inst.SupplyMode,
		inst.Host, inst.Port, inst.AuthRef, inst.Isolation, inst.ContainerName, inst.Status)
	return err
}

// GetInstance 按 id 取实例。无则 nil,nil。
func (s *Store) GetInstance(ctx context.Context, id string) (*ServiceInstance, error) {
	var inst ServiceInstance
	err := s.db.GetContext(ctx, &inst, `SELECT `+instCols+` FROM appdeploy_service_instance WHERE id=$1`, id)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return &inst, nil
}

// DeleteInstance 按 id 删实例行（binding/env 由 FK CASCADE 兜底；dedicated 容器由调用方先 docker rm）。
func (s *Store) DeleteInstance(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM appdeploy_service_instance WHERE id=$1`, id)
	return err
}
```

> `ProjectSpaceID` 是 `*string`（model.go），但 dedicated 供给时传 psID 字符串 → `inst.ProjectSpaceID` 字段类型需匹配。**注意**：`ServiceInstance.ProjectSpaceID` 是 `*string`。supplyDedicated 构造 inst 时赋值见 Task 6（传 psID 的指针或留空）。此处 `NULLIF($2,'')` 接收字符串——`inst.ProjectSpaceID` 为 `*string`，sqlx 会把 nil→NULL、非 nil→值。为避免类型摩擦，Task 6 构造 inst 时 `ProjectSpaceID: nil`（dedicated 实例不挂项目，靠 binding 关联 app），`$2` 传 nil。**故 CreateInstance 的 `$2` 应传 `inst.ProjectSpaceID`（\*string）**，上面 SQL 已用 `NULLIF($2,'')` 但 *string 传参时改用直接 `$2`。修正见下。

> **修正（替换上面 CreateInstance 的参数行）**：把 `NULLIF($2,'')` 改为 `$2`，参数 `inst.ProjectSpaceID`（*string，nil→NULL）：

```go
		`INSERT INTO appdeploy_service_instance
		   (id, project_space_id, kind, name, supply_mode, host, port, auth_ref, isolation, container_name, status)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),NULLIF($9,'')::jsonb,NULLIF($10,''),$11)
		 ON CONFLICT (id) DO NOTHING`,
	inst.ID, inst.ProjectSpaceID, inst.Kind, inst.Name, inst.SupplyMode,
	inst.Host, inst.Port, inst.AuthRef, inst.Isolation, inst.ContainerName, inst.Status)
```

> 测试中 `inst.ProjectSpaceID` 未设（nil）→ 落 NULL，符合 dedicated 实例不挂项目的设计（靠 binding 关联 app）。

- [ ] **Step 4: 跑测试验证通过**

Run:

```
GOPATH=C:/Users/yxt/go go test -p 1 -run 'TestStore_' ./internal/mwsupply/
```

Expected: PASS（含本 task 3 个 + 既有 shared/bind_existing store 用例）。

- [ ] **Step 5: Commit**

```bash
git add platform/backend/internal/mwsupply/store.go platform/backend/internal/mwsupply/store_test.go
git commit -m "feat(mwsupply): store dedicated 方法 CreateInstance/GetInstance/DeleteInstance"
```

---

## Task 6: supply.go 接入 dedicated 分支（supplyDedicated + writeDedicatedEnv + NewReconciler 新签名）

**Files:**

- Modify: `platform/backend/internal/mwsupply/supply.go`
- Modify: `platform/backend/internal/mwsupply/supply_test.go`（`newReconcilerTest` 加 fakeDocker+host + 全部调用点改 + dedicated 用例）

**Interfaces:**

- Consumes: Task 2（`genShortID`/`genPassword`/`allocPort`/`dedicatedContainerName`/常量）、Task 3（`ReadyChecker`/`redisFlusher.Ping`）、Task 4（`MWDockerRunner`/`NewOSDocker`）、Task 5（`CreateInstance`/`GetInstance`）、既有 `GetBinding`/`UpsertBinding`/`UpsertEnv`/`ConnStr`
- Produces: `NewReconciler(store *Store, env EnvWriter, flusher DBFlusher, ready ReadyChecker, docker MWDockerRunner, host string) *Reconciler` 新签名（Task 8 main.go 消费）

- [ ] **Step 1: 改测试脚手架（newReconcilerTest 加 fakeDocker+host，全部调用点加一位 \_）**

在 `internal/mwsupply/supply_test.go` 顶部 `fakeFlusher` 定义后加 `fakeDocker`，并改 `newReconcilerTest` 签名：

```go
// fakeDocker 记 RunRedisContainer/RmForce 调用；usedPorts 控制端口池；runErr 模拟起容器失败。
type fakeDocker struct {
	usedPorts map[int]struct{}
	runCalls  []fakeDockerRun
	runErr    error
	rmCalls   []string
}

type fakeDockerRun struct {
	name, password string
	port           int
}

func (f *fakeDocker) UsedPorts(_ context.Context) map[int]struct{} { return f.usedPorts }
func (f *fakeDocker) RunRedisContainer(_ context.Context, name, password string, port int) error {
	f.runCalls = append(f.runCalls, fakeDockerRun{name, password, port})
	return f.runErr
}
func (f *fakeDocker) RmForce(_ context.Context, name string) error {
	f.rmCalls = append(f.rmCalls, name)
	return nil
}
```

改 `newReconcilerTest`（返回值加 `*fakeDocker`，Reconciler 多传 ready/docker/host）：

```go
// newReconcilerTest 起 Reconciler（env 用真实 appdeploy.Store；flusher+ready 用同一 fakeFlusher；
// docker 用 fakeDocker）+ 清表 + 保 .28 种子（含 shared）。host=testdeploy（REDIS_ADDR 测试值）。
func newReconcilerTest(t *testing.T) (*Reconciler, *appdeploy.Store, *sqlx.DB, *fakeFlusher, *fakeDocker) {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "appdeploy_service_binding", "appdeploy_env", "appdeploy_application")
	ensureSeed(t, db)
	appStore := appdeploy.NewStore(db)
	f := &fakeFlusher{}            // 同时作 ready（fakeFlusher 不实现 Ping？见下）
	dk := &fakeDocker{usedPorts: map[int]struct{}{}}
	return NewReconciler(NewStore(db), appStore, f, f, dk, "testdeploy"), appStore, db, f, dk
}
```

> **关键**：`fakeFlusher` 需同时满足 `DBFlusher` + `ReadyChecker`（NewReconciler 第 3、4 参）。给 `fakeFlusher` 加 `Ping` 方法（返回 `r.pingErr`，默认 nil 即视为就绪）：

在 `fakeFlusher` 结构体加字段 + 方法（替换原 fakeFlusher 定义）：

```go
type fakeFlusher struct {
	calls   int
	err     error
	pingErr error // dedicated 就绪检测返错（默认 nil=就绪）
}

func (f *fakeFlusher) FlushDB(ctx context.Context, host string, port int, password string, db int) error {
	f.calls++
	return f.err
}

// Ping 满足 ReadyChecker（dedicated 就绪检测）。默认 pingErr=nil → 立即就绪。
func (f *fakeFlusher) Ping(ctx context.Context, host string, port int, password string) error {
	return f.pingErr
}
```

改**全部既有调用点**（4 个 bind_existing + 4 个 shared 用例）的解构：`r, appStore, db, _ := newReconcilerTest(t)` → `r, appStore, db, _, _ := newReconcilerTest(t)`；用到 `fl` 的（shared 用例）→ `r, appStore, db, fl, _ := newReconcilerTest(t)`。逐一改：

- `TestReconcile_bindExisting`: `r, appStore, db, _, _ := newReconcilerTest(t)`
- `TestReconcile_idempotent`: `r, appStore, _, _, _ := newReconcilerTest(t)`
- `TestReconcile_missingInstanceKind`: `r, appStore, db, _, _ := newReconcilerTest(t)`
- `TestReconcile_noManifest`: `r, appStore, db, _, _ := newReconcilerTest(t)`
- `TestReconcile_sharedRedis`: `r, appStore, db, fl, _ := newReconcilerTest(t)`
- `TestReconcile_shared_idempotent`: `r, appStore, _, fl, _ := newReconcilerTest(t)`
- `TestReconcile_shared_flushFailBestEffort`: `r, appStore, db, fl, _ := newReconcilerTest(t)`
- `TestReconcile_shared_poolExhaust`: `r, appStore, db, _, _ := newReconcilerTest(t)`

- [ ] **Step 2: 加 dedicated 失败测试**

在 `supply_test.go` 末尾（`errStr` 之前）追加：

```go
// —— dedicated 用例 ——

// TestReconcile_dedicatedRedis 新供给：起容器 + 就绪 + 登记 + 双 env。
func TestReconcile_dedicatedRedis(t *testing.T) {
	r, appStore, db, _, dk := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "ded1", RepoDir: "/x", InternalPort: 8080}
	if err := appStore.Create(ctx, a); err != nil {
		t.Fatalf("create app: %v", err)
	}
	dir := writeManifest(t, "services:\n  - kind: redis\n    strategy: dedicated\n")
	if err := r.Reconcile(ctx, a.ID, "ps_1", dir); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// 起了一次容器，端口 9600（空池最小号）
	if len(dk.runCalls) != 1 || dk.runCalls[0].port != mwPortMin {
		t.Fatalf("应起 1 容器 port=%d，得 %+v", mwPortMin, dk.runCalls)
	}
	// env：REDIS_ADDR=testdeploy:9600 + REDIS_PASSWORD 非空（secret/platform）
	ra, _ := appStore.GetEnvValue(ctx, a.ID, "REDIS_ADDR")
	if ra != "testdeploy:9600" {
		t.Fatalf("REDIS_ADDR 应 testdeploy:9600，得 %q", ra)
	}
	rp, _ := appStore.GetEnvValue(ctx, a.ID, "REDIS_PASSWORD")
	if rp == "" {
		t.Fatal("REDIS_PASSWORD 应非空")
	}
	rsrc, _ := appStore.GetEnvSource(ctx, a.ID, "REDIS_PASSWORD")
	if rsrc != "platform" {
		t.Fatalf("REDIS_PASSWORD source 应 platform，得 %q", rsrc)
	}
	// 不注入 REDIS_DB
	if rdb, _ := appStore.GetEnvValue(ctx, a.ID, "REDIS_DB"); rdb != "" {
		t.Fatalf("dedicated 不应注入 REDIS_DB，得 %q", rdb)
	}
	// binding bound + 实例行带 container_name
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 1 || binds[0].Status != StatusBound || binds[0].Strategy != ModeDedicated {
		t.Fatalf("binding 应 dedicated/bound，得 %+v", binds)
	}
	inst, _ := NewStore(db).GetInstance(ctx, binds[0].ServiceInstanceID)
	if inst == nil || inst.ContainerName == "" || inst.Port != mwPortMin {
		t.Fatalf("实例行应带 container_name + port=%d，得 %+v", mwPortMin, inst)
	}
}

// TestReconcile_dedicated_idempotent 同 app 重部署：不重启容器、port 不变、env 仍在。
func TestReconcile_dedicated_idempotent(t *testing.T) {
	r, appStore, _, _, dk := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "dedidem", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: redis\n    strategy: dedicated\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	ra1, _ := appStore.GetEnvValue(ctx, a.ID, "REDIS_ADDR")
	dk.runCalls = nil // 重置
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir) // 重部署
	ra2, _ := appStore.GetEnvValue(ctx, a.ID, "REDIS_ADDR")
	if ra1 != ra2 {
		t.Fatalf("重部署 REDIS_ADDR 应不变，%q → %q", ra1, ra2)
	}
	if len(dk.runCalls) != 0 {
		t.Fatalf("重部署复用不应再起容器，得 %d 次", len(dk.runCalls))
	}
}

// TestReconcile_dedicated_poolExhaust 端口池满 → failed、不起容器、不写 env。
func TestReconcile_dedicated_poolExhaust(t *testing.T) {
	r, appStore, db, _, dk := newReconcilerTest(t)
	// 占满 9600-9699
	full := map[int]struct{}{}
	for p := mwPortMin; p <= mwPortMax; p++ {
		full[p] = struct{}{}
	}
	dk.usedPorts = full
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "dedex", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: redis\n    strategy: dedicated\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 1 || binds[0].Status != StatusFailed {
		t.Fatalf("池满应 failed，得 %+v", binds)
	}
	if len(dk.runCalls) != 0 {
		t.Fatalf("池满不应起容器，得 %d", len(dk.runCalls))
	}
	if ra, _ := appStore.GetEnvValue(ctx, a.ID, "REDIS_ADDR"); ra != "" {
		t.Fatalf("池满不应写 REDIS_ADDR，得 %q", ra)
	}
}

// TestReconcile_dedicated_runFail 起容器失败 → failed、不登记实例。
func TestReconcile_dedicated_runFail(t *testing.T) {
	r, appStore, db, _, dk := newReconcilerTest(t)
	dk.runErr = errStr("docker run 失败")
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "dedfail", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: redis\n    strategy: dedicated\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 1 || binds[0].Status != StatusFailed || binds[0].ServiceInstanceID != "" {
		t.Fatalf("起容器失败应 failed + 无实例，得 %+v", binds)
	}
}

// TestReconcile_dedicated_readyFail 就绪失败 → failed、清半成品容器（RmForce 被调）。
func TestReconcile_dedicated_readyFail(t *testing.T) {
	r, appStore, db, fl, dk := newReconcilerTest(t)
	fl.pingErr = errStr("redis 不可达")
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "dedready", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: redis\n    strategy: dedicated\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 1 || binds[0].Status != StatusFailed {
		t.Fatalf("就绪失败应 failed，得 %+v", binds)
	}
	if len(dk.rmCalls) == 0 {
		t.Fatal("就绪失败应 RmForce 清半成品容器")
	}
}
```

- [ ] **Step 3: 跑测试验证失败**

Run:

```
GOPATH=C:/Users/yxt/go go test -p 1 -run 'TestReconcile_dedicated' ./internal/mwsupply/
```

Expected: 编译失败（`NewReconciler` 仍 3 参）或 FAIL。

- [ ] **Step 4: 改 supply.go（Reconciler 字段 + NewReconciler 新签名 + dedicated 分支）**

在 `internal/mwsupply/supply.go` 改 `Reconciler` 结构体 + `NewReconciler`：

```go
// Reconciler 中间件依赖供给。best-effort：失败记 binding，不阻塞部署。
type Reconciler struct {
	store   *Store
	env     EnvWriter
	flusher DBFlusher      // shared 重分配时清空 redis db
	ready   ReadyChecker   // dedicated 起容器后轮询 AUTH+PING 至就绪
	docker  MWDockerRunner  // dedicated 容器管理（run/rm）
	host    string         // AppDeployHost（dedicated REDIS_ADDR host + 就绪检测拨号）
	log     *zap.Logger    // 可选；失败记 Warn（nil 安全）
}

// NewReconciler 构造。
//   env 传 appdeploy.Store（满足 EnvWriter）；
//   flusher+ready 可传同一 *redisFlusher（NewRedisFlusher 同时满足 DBFlusher+ReadyChecker）；
//   docker 传 NewOSDocker()（测试传 fake）；host 为 AppDeployHost。
func NewReconciler(store *Store, env EnvWriter, flusher DBFlusher, ready ReadyChecker, docker MWDockerRunner, host string) *Reconciler {
	return &Reconciler{store: store, env: env, flusher: flusher, ready: ready, docker: docker, host: host}
}
```

在 `supplyOne` 的 `if strategy == ModeShared` 块后、`if strategy != ModeBindExisting` 之前插入 dedicated 分支，并改兜底文案：

```go
	if strategy == ModeShared {
		r.supplyShared(ctx, appID, psID, dep, mkBind)
		return
	}
	if strategy == ModeDedicated {
		r.supplyDedicated(ctx, appID, psID, dep, mkBind)
		return
	}
	if strategy != ModeBindExisting {
		mkBind(StatusFailed, "", "", "策略 "+strategy+" 暂未实现（仅 bind_existing/shared/dedicated）")
		return
	}
```

在 `supply.go` 末尾（`writeSharedEnv` 之后）追加 dedicated 供给 + env 方法：

```go
// supplyDedicated dedicated redis 供给：复用判定（幂等不重启/不换端口/保数据）/ 新供给（端口→起容器→就绪→登记→env）。
func (r *Reconciler) supplyDedicated(ctx context.Context, appID, psID string, dep DepService,
	mkBind func(status, instID, token, lastErr string)) {
	// 复用：同 app 已 bound dedicated 实例 → 不重启、不换端口、保数据，重写 env。
	if b, e := r.store.GetBinding(ctx, appID, dep.Kind); e == nil && b != nil &&
		b.Status == StatusBound && b.ServiceInstanceID != "" {
		if inst, ie := r.store.GetInstance(ctx, b.ServiceInstanceID); ie == nil && inst != nil && inst.Status == "active" {
			r.writeDedicatedEnv(ctx, appID, inst)
			mkBind(StatusBound, inst.ID, "", "")
			return
		}
	}
	// 新供给
	port := allocPort(r.docker.UsedPorts(ctx), mwPortMin, mwPortMax)
	if port == 0 {
		mkBind(StatusFailed, "", "", fmt.Sprintf("redis 端口池 %d-%d 已满", mwPortMin, mwPortMax))
		return
	}
	short := genShortID()
	name := dedicatedContainerName(short)
	pwd := genPassword()
	if err := r.docker.RunRedisContainer(ctx, name, pwd, port); err != nil {
		mkBind(StatusFailed, "", "", "起 redis 容器: "+err.Error())
		return
	}
	// 就绪检测（轮询 AUTH+PING，超时 readyTimeout）；失败清半成品容器。
	readyCtx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()
	if err := r.ready.Ping(readyCtx, r.host, port, pwd); err != nil {
		_ = r.docker.RmForce(ctx, name)
		mkBind(StatusFailed, "", "", "redis 未就绪: "+err.Error())
		return
	}
	inst := &ServiceInstance{
		ID:            "svinst-redis-ded-" + short,
		ProjectSpaceID: nil, // dedicated 实例不挂项目，靠 binding 关联 app
		Kind:          dep.Kind,
		Name:          name,
		SupplyMode:    ModeDedicated,
		Host:          r.host,
		Port:          port,
		AuthRef:       pwd,
		ContainerName: name,
		Status:        "active",
	}
	if err := r.store.CreateInstance(ctx, inst); err != nil {
		_ = r.docker.RmForce(ctx, name) // 登记失败回收容器
		mkBind(StatusFailed, "", "", "登记实例: "+err.Error())
		return
	}
	r.writeDedicatedEnv(ctx, appID, inst)
	mkBind(StatusBound, inst.ID, "", "")
}

// writeDedicatedEnv 写 REDIS_ADDR + REDIS_PASSWORD，均 source=platform（不写 REDIS_DB，dedicated 用默认 db 0）。
func (r *Reconciler) writeDedicatedEnv(ctx context.Context, appID string, inst *ServiceInstance) {
	_ = r.env.UpsertEnv(ctx, appID, EnvKeyFor(inst.Kind), ConnStr(inst), false, "platform") // REDIS_ADDR=host:port
	pwdKey := strings.ToUpper(inst.Kind) + "_PASSWORD"                                      // REDIS_PASSWORD
	_ = r.env.UpsertEnv(ctx, appID, pwdKey, inst.AuthRef, true, "platform")                 // secret
}
```

> 注：`ConnStr(inst)` 返回 `host:port`（既有，不改）。`EnvKeyFor("redis")` = `"REDIS_ADDR"`。`strings` 已在 supply.go import（writeSharedEnv 用）。

- [ ] **Step 5: 跑测试验证通过**

Run:

```
GOPATH=C:/Users/yxt/go go test -p 1 ./internal/mwsupply/
```

Expected: PASS（含既有 bind_existing/shared 全部 + 5 个 dedicated 用例 + Task 1-5 全部）。若 shared 用例因 `NewReconciler` 签名变化编译失败，确认 Step 1 的调用点已全改。

- [ ] **Step 6: Commit**

```bash
git add platform/backend/internal/mwsupply/supply.go platform/backend/internal/mwsupply/supply_test.go
git commit -m "feat(mwsupply): supply dedicated 分支(端口分配+起容器+就绪+登记+双env)+NewReconciler新签名"
```

---

## Task 7: supply.go Cleanup 方法（dedicated 容器回收）

**Files:**

- Modify: `platform/backend/internal/mwsupply/supply.go`（追加 `Cleanup`）
- Test: `platform/backend/internal/mwsupply/supply_test.go`（追加 cleanup 用例）

**Interfaces:**

- Consumes: Task 6 的 `Reconciler.docker`/`store`；`ListBindingsByApp`/`GetInstance`/`DeleteInstance`/`RmForce`
- Produces: `func (r *Reconciler) Cleanup(ctx, appID) error`（满足 Task 9 的 `MWReconciler.Cleanup` 接口）

- [ ] **Step 1: 写失败测试**

追加到 `supply_test.go` 末尾（`errStr` 之前）：

```go
// TestReconcile_cleanup_dedicated 删 dedicated app → docker rm 容器 + 删 instance 行。
func TestReconcile_cleanup_dedicated(t *testing.T) {
	r, appStore, db, _, dk := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "dedclean", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: redis\n    strategy: dedicated\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	instID := binds[0].ServiceInstanceID
	inst, _ := NewStore(db).GetInstance(ctx, instID)
	cname := inst.ContainerName
	dk.rmCalls = nil

	if err := r.Cleanup(ctx, a.ID); err != nil {
		t.Fatalf("Cleanup 不应报错: %v", err)
	}
	// docker rm 了 dedicated 容器
	if len(dk.rmCalls) != 1 || dk.rmCalls[0] != cname {
		t.Fatalf("应 RmForce %q，得 %v", cname, dk.rmCalls)
	}
	// instance 行已删
	if got, _ := NewStore(db).GetInstance(ctx, instID); got != nil {
		t.Fatalf("Cleanup 后实例行应删，得 %+v", got)
	}
}

// TestReconcile_cleanup_skipsSharedAndBindExisting Cleanup 只动 dedicated，不碰 shared/bind_existing（靠 CASCADE）。
func TestReconcile_cleanup_skipsSharedAndBindExisting(t *testing.T) {
	r, _, db, _, dk := newReconcilerTest(t)
	ctx := context.Background()
	// shared app
	as := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "shclean", RepoDir: "/x", InternalPort: 8080}
	_ = appdeploy.NewStore(db).Create(ctx, as)
	_ = r.Reconcile(ctx, as.ID, "ps_1", writeManifest(t, "services:\n  - kind: redis\n    strategy: shared\n"))
	// bind_existing app
	ab := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "beclean", RepoDir: "/x", InternalPort: 8080}
	_ = appdeploy.NewStore(db).Create(ctx, ab)
	_ = r.Reconcile(ctx, ab.ID, "ps_1", writeManifest(t, "services:\n  - kind: redis\n"))
	dk.rmCalls = nil

	_ = r.Cleanup(ctx, as.ID)
	_ = r.Cleanup(ctx, ab.ID)
	if len(dk.rmCalls) != 0 {
		t.Fatalf("shared/bind_existing 不应触发 RmForce，得 %v", dk.rmCalls)
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run:

```
GOPATH=C:/Users/yxt/go go test -p 1 -run 'TestReconcile_cleanup' ./internal/mwsupply/
```

Expected: FAIL（`Cleanup` undefined）。

- [ ] **Step 3: 追加 Cleanup 方法到 supply.go**

在 `supply.go` 末尾追加：

```go
// Cleanup 删 app 的 dedicated 中间件容器（best-effort，不阻塞 Delete）。
// 只动 strategy=dedicated 的 binding（bind_existing/shared 靠 ON DELETE CASCADE，不碰）。
// dedicated 容器是宿主资源，CASCADE 只删 DB 行不删容器 → 必须显式 docker rm + 删 instance 行。
// 总返回 nil（失败记日志，不阻塞删 app）。
func (r *Reconciler) Cleanup(ctx context.Context, appID string) error {
	binds, err := r.store.ListBindingsByApp(ctx, appID)
	if err != nil {
		return nil
	}
	for _, b := range binds {
		if b.Strategy != ModeDedicated || b.ServiceInstanceID == "" {
			continue
		}
		inst, ie := r.store.GetInstance(ctx, b.ServiceInstanceID)
		if ie != nil || inst == nil {
			continue
		}
		if inst.ContainerName != "" {
			if err := r.docker.RmForce(ctx, inst.ContainerName); err != nil && r.log != nil {
				r.log.Warn("dedicated 容器清理失败 (best-effort)",
					zap.String("app", appID), zap.String("container", inst.ContainerName), zap.Error(err))
			}
		}
		_ = r.store.DeleteInstance(ctx, inst.ID) // 删 instance 行（binding/env CASCADE 兜底）
	}
	return nil
}
```

- [ ] **Step 4: 跑测试验证通过**

Run:

```
GOPATH=C:/Users/yxt/go go test -p 1 -run 'TestReconcile_cleanup' ./internal/mwsupply/
```

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add platform/backend/internal/mwsupply/supply.go platform/backend/internal/mwsupply/supply_test.go
git commit -m "feat(mwsupply): Reconciler.Cleanup 回收 dedicated 容器(只动 dedicated,跳过 shared/bind_existing)"
```

---

## Task 8: main.go 装配（NewReconciler 多传 docker + host）

**Files:**

- Modify: `platform/backend/cmd/server/main.go:185`

**Interfaces:**

- Consumes: Task 4 `NewOSDocker()`、Task 6 新 `NewReconciler(store, env, flusher, ready, docker, host)`、`cfg.AppDeployHost`

- [ ] **Step 1: 改 main.go 一行**

`cmd/server/main.go:185` 由：

```go
	mwReconciler := mwsupply.NewReconciler(mwStore, appDeployStore, mwsupply.NewRedisFlusher()) // appDeployStore 满足 mwsupply.EnvWriter
```

改为（`NewRedisFlusher()` 返回 `*redisFlusher`，同时满足 `DBFlusher`+`ReadyChecker`，作 flusher+ready 双用）：

```go
	mwProbe := mwsupply.NewRedisFlusher()                                                                 // *redisFlusher 同时满足 DBFlusher+ReadyChecker
	mwReconciler := mwsupply.NewReconciler(mwStore, appDeployStore, mwProbe, mwProbe, mwsupply.NewOSDocker(), cfg.AppDeployHost) // appDeployStore 满足 mwsupply.EnvWriter
```

> `cfg.AppDeployHost` 已在用（main.go:178 pgsupply.InstanceManager 第 4 参即 `cfg.AppDeployHost`）。

- [ ] **Step 2: 编译 + vet 验证**

Run（from `platform/backend`）:

```
GOPATH=C:/Users/yxt/go go build ./...
GOPATH=C:/Users/yxt/go go vet ./internal/mwsupply/... ./cmd/server/...
```

Expected: 无错。

> 若 `cmd/server` 报 `NewRedisFlusher` 返回类型不满足接口：确认 Task 3 的 `redisFlusher` 已实现 `Ping`（`ReadyChecker`）；`NewRedisFlusher() *redisFlusher` 返回具体指针，赋给 `DBFlusher`/`ReadyChecker` 参自动满足。

- [ ] **Step 3: Commit**

```bash
git add platform/backend/cmd/server/main.go
git commit -m "feat(main): NewReconciler 装配 NewOSDocker+AppDeployHost(P3 dedicated)"
```

---

## Task 9: handler.go（MWReconciler 接口加 Cleanup + Delete 调用）+ 测试 mock

**Files:**

- Modify: `platform/backend/internal/appdeploy/handler.go`（接口加方法 ~77 行 + Delete 调用 ~1864 行）
- Test: `platform/backend/internal/appdeploy/handler_http_test.go`（`fakeMWReconciler` 加 Cleanup）

**Interfaces:**

- Consumes: Task 7 的 `Reconciler.Cleanup`（mwsupply.Reconciler 已满足新接口）
- Produces: `MWReconciler` 接口含 `Cleanup`；Delete handler 删 app 时回收 dedicated 容器

- [ ] **Step 1: 改测试 mock（先让接口变更可见）**

`internal/appdeploy/handler_http_test.go` 的 `fakeMWReconciler`（~422 行）加 `Cleanup` 方法：

```go
type fakeMWReconciler struct{}

func (f *fakeMWReconciler) Reconcile(_ context.Context, _, _, _ string) error { return nil }
func (f *fakeMWReconciler) Cleanup(_ context.Context, _ string) error          { return nil }
```

- [ ] **Step 2: 改接口（handler.go ~77 行）**

`internal/appdeploy/handler.go` 的 `MWReconciler` 接口加 `Cleanup`：

```go
// MWReconciler 中间件依赖供给（部署前读 repo 的 .anp/deps.yaml → 注入 REDIS_ADDR 等连接 env；
// 删 app 时回收 dedicated 中间件容器）。由 mwsupply.Reconciler 实现（经 main.go SetMwReconciler 注入，避免 appdeploy→mwsupply 依赖）。
type MWReconciler interface {
	Reconcile(ctx context.Context, appID, psID, repoDir string) error
	Cleanup(ctx context.Context, appID string) error // P3：docker rm dedicated 容器（best-effort）
}
```

- [ ] **Step 3: 改 Delete handler（~1864 行，pgsupply.Cleanup 后插）**

`internal/appdeploy/handler.go` Delete（现状）：

```go
		// 先删应用库（DropDatabase/Role；库记录在 Store.Delete 级联删 appdeploy_database 前先清）
		if h.provisioner != nil {
			_ = h.provisioner.Cleanup(c.Request.Context(), a.ID)
		}
```

在其后追加（必须在 `h.store.Delete` 之前——CASCADE 删 binding 前读得到 instance_id）：

```go
		// 先删应用库（DropDatabase/Role；库记录在 Store.Delete 级联删 appdeploy_database 前先清）
		if h.provisioner != nil {
			_ = h.provisioner.Cleanup(c.Request.Context(), a.ID)
		}
		// 回收 dedicated 中间件容器（best-effort，不阻塞删 app；shared/bind_existing 靠 CASCADE）
		if h.mwReconciler != nil {
			_ = h.mwReconciler.Cleanup(c.Request.Context(), a.ID)
		}
```

- [ ] **Step 4: 编译 + 跑 handler 测试验证**

Run（from `platform/backend`）:

```
GOPATH=C:/Users/yxt/go go build ./...
GOPATH=C:/Users/yxt/go go test -p 1 -run 'TestHandler_SetMwReconciler' ./internal/appdeploy/
```

Expected: 编译通过；`TestHandler_SetMwReconciler` PASS（mock 已加 Cleanup，接口满足）。

- [ ] **Step 5: Commit**

```bash
git add platform/backend/internal/appdeploy/handler.go platform/backend/internal/appdeploy/handler_http_test.go
git commit -m "feat(appdeploy): MWReconciler加Cleanup接口+Delete回收dedicated中间件容器"
```

---

## Task 10: 全量回归 + `.28` 端到端验证

**Files:** 无代码改动（验证 + 部署）

> 遵循 memory `verify-cross-frontend-backend`（有测试用例 + 真驱动）+ `deploy-28-no-local-test`（本机不跑功能测试，`.28` 是测试库）+ `deploy-prod-10.10.0.28`（scp + docker-compose 重建）。

- [ ] **Step 1: 全量回归（串行）**

Run（from `platform/backend`）:

```
GOPATH=C:/Users/yxt/go go test -p 1 ./...
```

Expected: PASS（含 mwsupply 全部 dedicated/shared/bind_existing、appdeploy handler、pgsupply 等无回归）。如有非本次引入的既有失败，记录但不阻断（仅确认 mwsupply/appdeploy 绿）。

- [ ] **Step 2: push**

```bash
git push origin main
```

- [ ] **Step 3: scp 源码到 .28 + 重建（按 deploy-prod-10.10.0.28）**

向 `10.10.0.28:/opt/anp` scp 改动（或整目录同步），docker-compose 重建后端容器。迁移 000030 自动 apply。

> 具体命令按记忆 `deploy-prod-10.10.0.28`：keyless SSH、`/opt/anp` 源码、scp + docker-compose 重建、入口 8088。

- [ ] **Step 4: 先验镜像 + backend↔dedicated 可达性（§7.3 关键风险）**

在 .28 上：

1. `docker images | grep redis` —— 确认 `redis:7-alpine` 在；不在则 `docker pull redis:7-alpine`（需 .28 联网）或预载
2. 起一个测试 dedicated 容器手动验证 backend 拨得到：`docker run -d --name mwtest -p 9699:6379 redis:7-alpine redis-server --requirepass test`；在 `deploy_backend_1` 内 `redis-cli -h 10.10.0.28 -p 9699 -a test ping`（应回 PONG）；`docker rm -f mwtest`
3. 若拨不通（同 P2 flusher 坑）→ dedicated 供给会 failed；记录，与用户商讨备选（dedicated 容器加入 `deploy_default` 网容器名直连，留后续）

- [ ] **Step 5: `.28` e2e —— dedicated 隔离 + 回收**

造最小 python 应用（仿 P2 e2e：`.28 无 golang 镜像缓存→用 python:3-alpine`，`.anp/deps.yaml` 预写 `services:[{kind:redis, strategy:dedicated}]`，CREATE 带 `repo_dir` 不触发 adapt）→ deploy test，验证：

1. 容器内 `REDIS_ADDR=10.10.0.28:<port>` + `REDIS_PASSWORD=<pwd>` 在；`docker ps` 见 `mwredis-<short>` 容器（端口映射 `0.0.0.0:<port>->6379`）
2. `appdeploy_env` 有 `REDIS_ADDR`+`REDIS_PASSWORD` 两行 `source=platform`；**无 `REDIS_DB`**
3. `appdeploy_service_instance` 一行 `supply_mode=dedicated, container_name` 非空、`status=active`；binding `strategy=dedicated, status=bound`
4. **隔离**：两个 dedicated app → 两个 `mwredis-` 容器、两个端口（9600/9601）、各自独立数据（appA 写 key 读不到 appB 的）
5. app 能 SET/GET 自己的 redis（`redis-cli -h 10.10.0.28 -p <port> -a <pwd>` 验证可写可读）
6. **回收**：删 app → `docker ps` 不再见其 `mwredis-` 容器（`docker rm` 成功）+ `appdeploy_service_instance`/binding/env 行清（CASCADE）
7. **重部署复用**：同 app 重 deploy → 同一 `mwredis-` 容器（不新建）+ 先前写入的 redis 数据仍在
8. **平台保护**：手改 `REDIS_PASSWORD` 返 409（复用 source=platform 保护）

- [ ] **Step 6: 记录结论**

把 e2e 结论（容器内 env、instance/binding 行、隔离、回收复用 + 镜像/可达性验证）补进本 plan 末尾或 memory `import-adapt-reuse-coding` 的 P3 段。

---

## Self-Review（plan 作者自查，已完成）

**1. Spec coverage：**

- §3 粒度=每 app 一容器 → Task 6 `supplyDedicated`（1 app 1 容器 1 instance 行）✓
- §3 容器建模 A1（实例例行带 container_name）→ Task 1 加列 + Task 5 CreateInstance + Task 6 构造 inst ✓
- §3 docker B1（自包含 MWDockerRunner）→ Task 4 ✓
- §3 网络 publish-to-host + REDIS_ADDR=AppDeployHost:port → Task 6 `writeDedicatedEnv`（ConnStr(host:port)）+ Task 8 host=cfg.AppDeployHost ✓
- §3 无 flusher（dedicated）→ Task 6 不调 flusher（用 ready.Ping 替代）✓
- §3 鉴权 requirepass + REDIS_PASSWORD → Task 4 `redisRunArgs`（--requirepass）+ Task 6 `writeDedicatedEnv` ✓
- §3 端口池 9600-9699 → Task 2 常量 + Task 6 allocPort ✓
- §3 配额=端口池不动 quota → Task 6 池满 failed（无 quota 文件）✓
- §3 就绪 AUTH+PING 轮询 → Task 3 `ReadyChecker.Ping`（dialRedis+pingConn 轮询）+ Task 6 调 ready.Ping ✓
- §3 --restart unless-stopped → Task 4 `redisRunArgs` ✓
- §4 迁移 000030 + model.go → Task 1 ✓
- §5 supplyDedicated/状态机/writeDedicatedEnv → Task 6（含复用/新供给/池满/起容器失败/就绪失败 全有用例）✓
- §6 Cleanup（接口+实现+Delete 接入）→ Task 7（实现）+ Task 9（接口+Delete）✓
- §7 网络详解 + §11 可达性风险 → Task 10 Step 4 先验镜像/可达性 ✓
- §8 模块/文件改动 → 每 task 的 Files 块逐一对应（含 redisflush.go 小改、handler.go、main.go）✓
- §10 测试计划 → Task 1-9 单测 + Task 10 e2e ✓
- §11 风险 → Task 10 Step 4（镜像/可达性）+ Task 6/7（并发/边缘由测试覆盖）✓
- §13 验收 8 条 → Task 6 覆盖 2/3/5（供给/env/幂等/池满）、Task 7 覆盖 4（回收）、Task 10 e2e 覆盖 1/4/6/7/8 ✓

**2. Placeholder scan：** 无 TBD/TODO/"add error handling"；每步含完整代码。Task 5 中段有一处 SQL 修正说明（`NULLIF($2,'')` → `$2` 接 *string），已给出最终正确 SQL，非占位。

**3. Type consistency：**

- `NewReconciler(store, env, flusher DBFlusher, ready ReadyChecker, docker MWDockerRunner, host string)` —— Task 3 定义 `ReadyChecker`、Task 4 定义 `MWDockerRunner`、Task 6 定义新签名、Task 8 调用（mwProbe 双用 + NewOSDocker + cfg.AppDeployHost），四处一致 ✓
- `MWDockerRunner.RunRedisContainer(ctx, name, password, port)` —— Task 4 定义、Task 6 调用、Task 7 Cleanup 调 `RmForce`、测试 fake 一致 ✓
- `ReadyChecker.Ping(ctx, host, port, password)` —— Task 3 定义、Task 6 调 `r.ready.Ping(readyCtx, r.host, port, pwd)`、`fakeFlusher.Ping` 一致 ✓
- `CreateInstance(ctx, inst)` / `GetInstance(ctx, id)` / `DeleteInstance(ctx, id)` —— Task 5 定义、Task 6/7 调用一致 ✓
- `MWReconciler.Cleanup(ctx, appID) error` —— Task 7 实现（Reconciler.Cleanup）、Task 9 接口定义、fakeMWReconciler 一致 ✓
- `mkBind(status, instID, token, lastErr)` —— Task 6 dedicated 调用与既有 bind_existing/shared 签名一致 ✓
- `redisRunArgs(name, password, port) []string` —— Task 4 定义与测试一致 ✓
- `inst.ProjectSpaceID` 为 `*string`，Task 6 构造 `nil`、Task 5 CreateInstance `$2` 接 *string —— 一致 ✓

无类型/签名漂移。

---

_本计划把 P3 dedicated redis spec 落成 10 个 TDD task：迁移+model → naming 纯逻辑 → redisflush 重构（dialRedis/pingConn+Ping）→ docker MWDockerRunner → store dedicated 方法 → supply dedicated 分支 → Cleanup → main 装配 → handler 接口+Delete → 回归 + .28 e2e。每 task 含失败测试 + 完整实现 + 命令 + commit。审核通过后用 subagent-driven-development 或 executing-plans 执行。_

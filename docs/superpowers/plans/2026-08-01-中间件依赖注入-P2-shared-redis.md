# 中间件依赖注入 P2 —— shared 共享 Redis（db 号隔离）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让声明 `strategy: shared` 的 redis 依赖从共享 redis 实例分到独占 db 号，注入 `REDIS_ADDR`+`REDIS_DB`，多 app 隔离、删 app 自动回收、重分配 flush。

**Architecture:** 复用 P1 mwsupply 范式。`supplyOne` 加 shared 分支：`LookupShared` → 复用判定（幂等不换号不 flush）或新分配（`pickLowestFree` 最小空闲 db 号 → `FlushDB` → `ClaimSharedToken` 原子登记，唯一索引兜底撞号有界重试）→ 写双 env。删 app 靠 `ON DELETE CASCADE` 自动回收 db 号，Delete handler 零改。flush 用裸 RESP `net.Dial`（后端无 redis 客户端依赖），经 `DBFlusher` 接口注入。

**Tech Stack:** Go 1.25、pgx/v5（`github.com/jackc/pgx/v5`）、sqlx、PG（anp_test 库）、TDD。

**关联 spec：** [`docs/superpowers/specs/2026-08-01-中间件依赖注入-P2-shared-redis-design.md`](../specs/2026-08-01-中间件依赖注入-P2-shared-redis-design.md)

---

## Global Constraints

> 本节为 spec 的项目级硬约束，**每个任务的 requirements 隐式包含本节**。

- **禁 SQLite，只用 PG**：所有测试连 `.28 anp_test` 库（`testutil.TestDB`），不回退 sqlite（memory `no-sqlite-pg-only`）。
- **go test 串行**：`go test` 必带 `-p 1`（并发污染 `anp_test` 库；memory `go-test-serial-p1`）。
- **GOPATH 前缀**：所有 `go` 命令前缀 `GOPATH=C:/Users/yxt/go`（本机 GOPATH 被污染成 go.exe 路径；memory `gopath-pollution-windows`）。
- **工作目录**：所有 `go` 命令在 `platform/backend` 下执行。
- **PG 驱动**：pgx/v5；唯一约束冲突检测用 `errors.As(err, &pgErr)` + `pgErr.Code == "23505"`（`*pgconn.PgError`，import `github.com/jackc/pgx/v5/pgconn`）。
- **db 号池**：`[1,15]`，db 0 留给 bind_existing/系统；池满即 `failed`（配额即 db_range，不动 `internal/quota`）。
- **best-effort**：`Reconcile` 总不返回错、不阻塞部署（沿用 P1）；失败只记 `binding.status=failed` + `last_error`。
- **env 保护**：所有平台注入 env 行 `source=platform`（409 保护已有，复用）。
- **handler.go / Deployer / EnvPairs 零改**；`main.go` 仅 `NewReconciler` 多传一个 `flusher`。
- **TDD**：每任务先写失败测试 → 跑红 → 最小实现 → 跑绿 → commit。frequent commits。

---

## File Structure

| 文件                                                        | 责任                                                                                                                                 | 动作       |
| ----------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ | ---------- |
| `internal/db/migrations/pg/000029_mwsupply_shared.up.sql`   | shared 实例种子 + 部分唯一索引                                                                                                       | 新建       |
| `internal/db/migrations/pg/000029_mwsupply_shared.down.sql` | 回滚                                                                                                                                 | 新建       |
| `internal/mwsupply/isolation.go`                            | `ParseDBRange` + `pickLowestFree`（db_range 纯逻辑）                                                                                 | 新建       |
| `internal/mwsupply/isolation_test.go`                       | 上述纯逻辑单测                                                                                                                       | 新建       |
| `internal/mwsupply/redisflush.go`                           | `DBFlusher` 接口 + 裸 RESP `FlushDB` 实现 + `NewRedisFlusher()`                                                                      | 新建       |
| `internal/mwsupply/redisflush_test.go`                      | RESP 帧/时序单测（net.Pipe fake server）                                                                                             | 新建       |
| `internal/mwsupply/store.go`                                | +`LookupShared`/`GetBinding`/`AllocatedTokens`/`ClaimSharedToken`                                                                    | 改         |
| `internal/mwsupply/norows.go`                               | +`isUniqueViolation`（DB 错误 helper，与 `isNoRows` 同居）                                                                           | 改         |
| `internal/mwsupply/store_test.go`                           | +shared store 单测                                                                                                                   | 改         |
| `internal/mwsupply/supply.go`                               | `Reconciler`+`flusher` 字段、`NewReconciler`+flusher 参、`supplyOne` shared 分支（`supplyShared`/`claimWithRetry`/`writeSharedEnv`） | 改         |
| `internal/mwsupply/supply_test.go`                          | `newReconcilerTest` 传 fakeFlusher + `ensureSeed` 加 shared 种子 + shared 单测                                                       | 改         |
| `cmd/server/main.go`                                        | `NewReconciler` 调用加 `NewRedisFlusher()`                                                                                           | 改（1 行） |

---

## Task 1: 迁移 000029（shared 实例种子 + 部分唯一索引）

**Files:**

- Create: `platform/backend/internal/db/migrations/pg/000029_mwsupply_shared.up.sql`
- Create: `platform/backend/internal/db/migrations/pg/000029_mwsupply_shared.down.sql`
- Test: `platform/backend/internal/mwsupply/store_test.go`（加一个迁移校验测试）

**Interfaces:**

- Produces: 数据库具备 `svinst-redis-shared-28` 种子行 + `uq_svbind_inst_token` 部分唯一索引（后续 task 的 SQL 依赖）

- [ ] **Step 1: 写失败测试（迁移校验，纯 SQL，不依赖 Go 方法）**

追加到 `internal/mwsupply/store_test.go` 末尾：

```go
// TestMigration_000029_sharedSeedAndIndex 迁移后：shared redis 种子在 + 部分唯一索引在。
func TestMigration_000029_sharedSeedAndIndex(t *testing.T) {
	_, db := newTestStore(t)
	// 种子行 + isolation.db_range 解析正确
	var lo, hi int
	err := db.QueryRow(`SELECT (isolation->'db_range'->>0)::int, (isolation->'db_range'->>1)::int
		FROM appdeploy_service_instance
		WHERE id='svinst-redis-shared-28' AND supply_mode='shared' AND project_space_id IS NULL`).Scan(&lo, &hi)
	if err != nil {
		t.Fatalf("shared redis 种子缺失: %v", err)
	}
	if lo != 1 || hi != 15 {
		t.Fatalf("db_range 应 [1,15]，得 [%d,%d]", lo, hi)
	}
	// 部分唯一索引存在
	var idxExists bool
	if err := db.Get(&idxExists, `SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE indexname='uq_svbind_inst_token')`); err != nil {
		t.Fatalf("查索引: %v", err)
	}
	if !idxExists {
		t.Fatal("部分唯一索引 uq_svbind_inst_token 应存在")
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run（from `platform/backend`）:

```
GOPATH=C:/Users/yxt/go go test -p 1 -run TestMigration_000029_sharedSeedAndIndex ./internal/mwsupply/
```

Expected: FAIL（种子行缺失 / 索引不存在）。

- [ ] **Step 3: 写迁移 up**

`internal/db/migrations/pg/000029_mwsupply_shared.up.sql`：

```sql
-- 000029_mwsupply_shared.up.sql
-- P2：shared 共享 redis（db 号隔离）
-- ① 种子平台级 shared redis 实例（复用 .28 同一台 yxt-redis，db 1-15 隔离，db 0 留给 bind_existing/系统）
-- ② 部分唯一索引：防并发分配撞 db 号（兜底；主路径靠乐观选号 + 重试）

INSERT INTO appdeploy_service_instance
  (id, project_space_id, kind, name, supply_mode, host, port, auth_ref, isolation, status)
VALUES
  ('svinst-redis-shared-28', NULL, 'redis', 'yxt-redis-shared', 'shared',
   '10.10.0.28', 6381, NULL, '{"db_range":[1,15]}'::jsonb, 'active')
ON CONFLICT (id) DO NOTHING;

-- 仅对「已分配 token」的 binding 建：NULL 不入索引（多 NULL 不冲突；bind_existing binding token 恒 NULL 不受影响）
CREATE UNIQUE INDEX IF NOT EXISTS uq_svbind_inst_token
  ON appdeploy_service_binding (service_instance_id, isolation_token)
  WHERE isolation_token IS NOT NULL;
```

- [ ] **Step 4: 写迁移 down**

`internal/db/migrations/pg/000029_mwsupply_shared.down.sql`：

```sql
-- 000029_mwsupply_shared.down.sql
DROP INDEX IF EXISTS uq_svbind_inst_token;
DELETE FROM appdeploy_service_instance WHERE id = 'svinst-redis-shared-28';
```

- [ ] **Step 5: 跑测试验证通过**

Run:

```
GOPATH=C:/Users/yxt/go go test -p 1 -run TestMigration_000029_sharedSeedAndIndex ./internal/mwsupply/
```

Expected: PASS（`testutil.TestDB` 会自动 apply 000029）。

- [ ] **Step 6: Commit**

```bash
git add platform/backend/internal/db/migrations/pg/000029_mwsupply_shared.up.sql \
        platform/backend/internal/db/migrations/pg/000029_mwsupply_shared.down.sql \
        platform/backend/internal/mwsupply/store_test.go
git commit -m "feat(mwsupply): 迁移000029 shared redis种子+部分唯一索引uq_svbind_inst_token"
```

---

## Task 2: db_range 纯逻辑（`ParseDBRange` + `pickLowestFree`）

**Files:**

- Create: `platform/backend/internal/mwsupply/isolation.go`
- Test: `platform/backend/internal/mwsupply/isolation_test.go`

**Interfaces:**

- Produces:
  - `func ParseDBRange(isolation string) (lo, hi int, ok bool)` —— 解析 `{"db_range":[1,15]}` → `1,15,true`；空/非法 → `0,0,false`
  - `func pickLowestFree(lo, hi int, allocated []string) (token string, ok bool)` —— 返回 `[lo,hi]` 内不在 `allocated` 的最小号（字符串）；全占 → `"",false`

- [ ] **Step 1: 写失败测试**

`internal/mwsupply/isolation_test.go`：

```go
package mwsupply

import "testing"

func TestParseDBRange(t *testing.T) {
	cases := []struct {
		in       string
		lo, hi   int
		ok       bool
	}{
		{`{"db_range":[1,15]}`, 1, 15, true},
		{`{"db_range": [0, 7]}`, 0, 7, true}, // PG jsonb::text 带空格
		{`{"default_db":0}`, 0, 0, false}, // 无 db_range（bind_existing 的 isolation）
		{``, 0, 0, false},
		{`not json`, 0, 0, false},
		{`{"db_range":[5]}`, 0, 0, false},   // 长度不对
		{`{"db_range":[5,3]}`, 0, 0, false}, // hi<lo
	}
	for _, c := range cases {
		lo, hi, ok := ParseDBRange(c.in)
		if lo != c.lo || hi != c.hi || ok != c.ok {
			t.Errorf("ParseDBRange(%q)=%d,%d,%v 想 %d,%d,%v", c.in, lo, hi, ok, c.lo, c.hi, c.ok)
		}
	}
}

func TestPickLowestFree(t *testing.T) {
	// 空池 → 1
	if tok, ok := pickLowestFree(1, 15, nil); tok != "1" || !ok {
		t.Fatalf("空池应得 1，得 %q,%v", tok, ok)
	}
	// 占了 1,2 → 3
	if tok, _ := pickLowestFree(1, 15, []string{"1", "2"}); tok != "3" {
		t.Fatalf("占 1,2 应得 3，得 %q", tok)
	}
	// 回收 1（不在 allocated）→ 复用 1
	if tok, _ := pickLowestFree(1, 15, []string{"2", "3"}); tok != "1" {
		t.Fatalf("1 空闲应复用 1，得 %q", tok)
	}
	// 全占 → false
	if _, ok := pickLowestFree(1, 3, []string{"1", "2", "3"}); ok {
		t.Fatal("全占应 false")
	}
	// allocated 含 1（string）不漏
	if tok, _ := pickLowestFree(1, 2, []string{"1"}); tok != "2" {
		t.Fatalf("占 1 应得 2，得 %q", tok)
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run:

```
GOPATH=C:/Users/yxt/go go test -p 1 -run 'TestParseDBRange|TestPickLowestFree' ./internal/mwsupply/
```

Expected: FAIL（`ParseDBRange`/`pickLowestFree` undefined）。

- [ ] **Step 3: 写实现**

`internal/mwsupply/isolation.go`：

```go
package mwsupply

import (
	"encoding/json"
	"strconv"
)

// ParseDBRange 解析 isolation JSONB 的 db_range（如 {"db_range":[1,15]}）。
// 返回 [lo,hi]（含）。无 db_range / 非法 → ok=false。
// 兼容 PG jsonb::text 的带空格输出（json.Unmarshal 忽略空白）。
func ParseDBRange(isolation string) (lo, hi int, ok bool) {
	if isolation == "" {
		return 0, 0, false
	}
	var m struct {
		DBRange []int `json:"db_range"`
	}
	if err := json.Unmarshal([]byte(isolation), &m); err != nil {
		return 0, 0, false
	}
	if len(m.DBRange) != 2 {
		return 0, 0, false
	}
	lo, hi = m.DBRange[0], m.DBRange[1]
	if lo < 0 || hi < lo {
		return 0, 0, false
	}
	return lo, hi, true
}

// pickLowestFree 返回 [lo,hi] 内不在 allocated 的最小号（字符串形式）。
// 全占用 → ("", false)。用于 shared redis db 号分配。
func pickLowestFree(lo, hi int, allocated []string) (string, bool) {
	taken := make(map[string]bool, len(allocated))
	for _, t := range allocated {
		taken[t] = true
	}
	for n := lo; n <= hi; n++ {
		if !taken[strconv.Itoa(n)] {
			return strconv.Itoa(n), true
		}
	}
	return "", false
}
```

- [ ] **Step 4: 跑测试验证通过**

Run:

```
GOPATH=C:/Users/yxt/go go test -p 1 -run 'TestParseDBRange|TestPickLowestFree' ./internal/mwsupply/
```

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add platform/backend/internal/mwsupply/isolation.go platform/backend/internal/mwsupply/isolation_test.go
git commit -m "feat(mwsupply): db_range 纯逻辑 ParseDBRange+pickLowestFree"
```

---

## Task 3: redis flusher（`DBFlusher` 接口 + 裸 RESP 实现）

**Files:**

- Create: `platform/backend/internal/mwsupply/redisflush.go`
- Test: `platform/backend/internal/mwsupply/redisflush_test.go`

**Interfaces:**

- Produces:
  - `type DBFlusher interface { FlushDB(ctx context.Context, host string, port int, password string, db int) error }`
  - `func NewRedisFlusher() DBFlusher`
- 消费方（Task 5 `supply.go`）经 `NewReconciler(store, env, flusher DBFlusher)` 注入。

- [ ] **Step 1: 写失败测试（net.Pipe fake server，校验 RESP 帧 + 命令时序）**

`internal/mwsupply/redisflush_test.go`：

```go
package mwsupply

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// startFakeRedis 起一个假 redis（net.Pipe）：每收到一条命令回 +OK；把收到的命令记入 got。
// password！="" 时期望首条 AUTH；db 为 SELECT 的参数。
func startFakeRedis(t *testing.T, expectAuth string) (addr string, got *[]string, closer func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var mu sync.Mutex
	commands := []string{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		bw := bufio.NewWriter(conn)
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			// RESP 多bulk头 *N
			if strings.HasPrefix(line, "*") {
				n := countArgs(line)
				var cmd []string
				for i := 0; i < n; i++ {
					// $len\r\n  data\r\n
					hdr, _ := br.ReadString('\n')
					_ = hdr
					data, _ := br.ReadString('\n')
					data = strings.TrimRight(data, "\r\n")
					cmd = append(cmd, data)
				}
				mu.Lock()
				commands = append(commands, strings.Join(cmd, " "))
				mu.Unlock()
				_, _ = bw.WriteString("+OK\r\n")
				_ = bw.Flush()
			}
		}
	}()
	closer = func() { _ = ln.Close(); select { case <-done: case <-time.After(time.Second): } }
	return ln.Addr().String(), &commands, closer
}

// countArgs 解析 "*3\r\n" → 3。
func countArgs(starLine string) int {
	s := strings.TrimPrefix(strings.TrimRight(starLine, "\r\n"), "*")
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func TestRedisFlush_NoAuth(t *testing.T) {
	addr, got, closer := startFakeRedis(t, "")
	defer closer()
	host, port, _ := net.SplitHostPort(addr)
	p := 0
	for _, c := range port {
		p = p*10 + int(c-'0')
	}
	f := NewRedisFlusher()
	if err := f.FlushDB(context.Background(), host, p, "", 7); err != nil {
		t.Fatalf("FlushDB: %v", err)
	}
	// 应发 SELECT 7、FLUSHDB（无 AUTH）
	want := []string{"SELECT 7", "FLUSHDB"}
	if len(*got) != len(want) {
		t.Fatalf("应 %d 条命令，得 %v", len(want), *got)
	}
	for i, w := range want {
		if (*got)[i] != w {
			t.Errorf("cmd[%d] 想 %q 得 %q", i, w, (*got)[i])
		}
	}
}

func TestRedisFlush_WithAuth(t *testing.T) {
	addr, got, closer := startFakeRedis(t, "secret")
	defer closer()
	host, port, _ := net.SplitHostPort(addr)
	p := 0
	for _, c := range port {
		p = p*10 + int(c-'0')
	}
	if err := NewRedisFlusher().FlushDB(context.Background(), host, p, "secret", 3); err != nil {
		t.Fatalf("FlushDB: %v", err)
	}
	want := []string{"AUTH secret", "SELECT 3", "FLUSHDB"}
	if len(*got) != len(want) {
		t.Fatalf("应 %d 条命令（含 AUTH），得 %v", len(want), *got)
	}
}

// TestRedisFlush_ServerError 假 redis 回 -ERR → FlushDB 返错。
func TestRedisFlush_ServerError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = io.WriteString(c, "-ERR boom\r\n") // 第一条命令回错
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	p := 0
	for _, c := range port {
		p = p*10 + int(c-'0')
	}
	err = NewRedisFlusher().FlushDB(context.Background(), host, p, "", 1)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("应收含 boom 的错，得 %v", err)
	}
}

// 防止未用 import（io 用于 ServerError 场景）
var _ = io.EOF
```

- [ ] **Step 2: 跑测试验证失败**

Run:

```
GOPATH=C:/Users/yxt/go go test -p 1 -run 'TestRedisFlush' ./internal/mwsupply/
```

Expected: FAIL（`NewRedisFlusher` undefined）。

- [ ] **Step 3: 写实现**

`internal/mwsupply/redisflush.go`：

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
// 由 NewReconciler 注入；测试传 fake。FLUSHDB 幂等。
type DBFlusher interface {
	FlushDB(ctx context.Context, host string, port int, password string, db int) error
}

// NewRedisFlusher 构造裸 RESP 实现（net.Dial，不引 go-redis/redigo）。
func NewRedisFlusher() DBFlusher { return &redisFlusher{} }

type redisFlusher struct{}

// FlushDB 连 redis，依次发（可选 AUTH）、SELECT <db>、FLUSHDB，每条读 +OK。
func (f *redisFlusher) FlushDB(ctx context.Context, host string, port int, password string, db int) error {
	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("dial redis: %w", err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	return flushConn(conn, password, db)
}

// flushConn 在已建连上发 AUTH(可选)/SELECT/FLUSHDB。抽出便于用 net.Pipe 单测。
func flushConn(rw io.ReadWriter, password string, db int) error {
	bw := bufio.NewWriter(rw)
	br := bufio.NewReader(rw)
	if password != "" {
		if err := writeCmd(bw, "AUTH", password); err != nil {
			return err
		}
		if err := readOK(br); err != nil {
			return fmt.Errorf("AUTH: %w", err)
		}
	}
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

- [ ] **Step 4: 跑测试验证通过**

Run:

```
GOPATH=C:/Users/yxt/go go test -p 1 -run 'TestRedisFlush' ./internal/mwsupply/
```

Expected: PASS（无 AUTH 发 SELECT+FLUSHDB；有 AUTH 多一条 AUTH；server -ERR → 报错）。

- [ ] **Step 5: Commit**

```bash
git add platform/backend/internal/mwsupply/redisflush.go platform/backend/internal/mwsupply/redisflush_test.go
git commit -m "feat(mwsupply): redisflush 裸RESP FlushDB+DBFlusher接口"
```

---

## Task 4: store 的 shared 方法（`LookupShared`/`GetBinding`/`AllocatedTokens`/`ClaimSharedToken` + `isUniqueViolation`）

**Files:**

- Modify: `platform/backend/internal/mwsupply/store.go`（追加 4 方法）
- Modify: `platform/backend/internal/mwsupply/norows.go`（加 `isUniqueViolation`）
- Test: `platform/backend/internal/mwsupply/store_test.go`（追加测试）

**Interfaces:**

- Consumes: Task 1 的种子行 + 唯一索引；`UpsertBinding`（已有，`ClaimSharedToken` 复用）
- Produces:
  - `func (s *Store) LookupShared(ctx, kind) (*ServiceInstance, error)` —— 平台级 `supply_mode='shared' AND project_space_id IS NULL`；无 → `nil,nil`
  - `func (s *Store) GetBinding(ctx, appID, kind) (*ServiceBinding, error)` —— 无 → `nil,nil`
  - `func (s *Store) AllocatedTokens(ctx, instID) ([]string, error)` —— 该实例所有 `isolation_token IS NOT NULL`
  - `func (s *Store) ClaimSharedToken(ctx, appID, psID, kind, instID, token, envKey) error` —— 设 `strategy=shared,status=bound` 调 `UpsertBinding`；撞 `(instID,token)` → 返 23505
  - `func isUniqueViolation(err error) bool`

- [ ] **Step 1: 写失败测试**

追加到 `internal/mwsupply/store_test.go` 末尾：

```go
// TestStore_LookupShared_seed 平台级 shared redis 种子可查到。
func TestStore_LookupShared_seed(t *testing.T) {
	s, _ := newTestStore(t)
	got, err := s.LookupShared(context.Background(), "redis")
	if err != nil || got == nil {
		t.Fatalf("应命中 shared redis 种子，err=%v got=%+v", err, got)
	}
	if got.ID != "svinst-redis-shared-28" || got.SupplyMode != "shared" || got.Port != 6381 {
		t.Fatalf("shared 种子不符: %+v", got)
	}
	// 无 shared milvus → nil,nil
	gotM, err := s.LookupShared(context.Background(), "milvus")
	if err != nil || gotM != nil {
		t.Fatalf("shared milvus 应 nil,nil，得 %+v err=%v", gotM, err)
	}
}

// TestStore_AllocatedTokens 占用集正确。
func TestStore_AllocatedTokens(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	appA := mkAppRow(t, db, "sh_a", "ps_1")
	appB := mkAppRow(t, db, "sh_b", "ps_1")
	// 直接写两条已 claim 的 binding（token 1/3）
	_ = s.ClaimSharedToken(ctx, appA, "ps_1", "redis", "svinst-redis-shared-28", "1", "REDIS_ADDR")
	_ = s.ClaimSharedToken(ctx, appB, "ps_1", "redis", "svinst-redis-shared-28", "3", "REDIS_ADDR")
	toks, err := s.AllocatedTokens(ctx, "svinst-redis-shared-28")
	if err != nil {
		t.Fatalf("AllocatedTokens: %v", err)
	}
	want := map[string]bool{"1": true, "3": true}
	if len(toks) != 2 || !want[toks[0]] || !want[toks[1]] {
		t.Fatalf("占用集应 {1,3}，得 %v", toks)
	}
}

// TestStore_ClaimSharedToken_uniqueViolation 不同 app 抢同 (inst,token) → 第二个 23505。
func TestStore_ClaimSharedToken_uniqueViolation(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	appA := mkAppRow(t, db, "cu_a", "ps_1")
	appB := mkAppRow(t, db, "cu_b", "ps_1")
	if err := s.ClaimSharedToken(ctx, appA, "ps_1", "redis", "svinst-redis-shared-28", "5", "REDIS_ADDR"); err != nil {
		t.Fatalf("首次 claim: %v", err)
	}
	err := s.ClaimSharedToken(ctx, appB, "ps_1", "redis", "svinst-redis-shared-28", "5", "REDIS_ADDR")
	if !isUniqueViolation(err) {
		t.Fatalf("撞号应 23505，得 %v", err)
	}
}

// TestStore_GetBinding 取/无。
func TestStore_GetBinding(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	app := mkAppRow(t, db, "gb_a", "ps_1")
	if b, err := s.GetBinding(ctx, app, "redis"); err != nil || b != nil {
		t.Fatalf("无 binding 应 nil,nil，得 %+v err=%v", b, err)
	}
	_ = s.ClaimSharedToken(ctx, app, "ps_1", "redis", "svinst-redis-shared-28", "2", "REDIS_ADDR")
	b, err := s.GetBinding(ctx, app, "redis")
	if err != nil || b == nil || b.IsolationToken != "2" || b.Status != StatusBound {
		t.Fatalf("应取到 bound token=2，得 %+v err=%v", b, err)
	}
}

// TestStore_shared_recycle 删 binding → token 回收（AllocatedTokens 不再含）。
func TestStore_shared_recycle(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	app := mkAppRow(t, db, "rc_a", "ps_1")
	_ = s.ClaimSharedToken(ctx, app, "ps_1", "redis", "svinst-redis-shared-28", "4", "REDIS_ADDR")
	if toks, _ := s.AllocatedTokens(ctx, "svinst-redis-shared-28"); !contains(toks, "4") {
		t.Fatalf("应含 4，得 %v", toks)
	}
	if _, err := db.Exec(`DELETE FROM appdeploy_service_binding WHERE app_id=$1`, app); err != nil {
		t.Fatalf("delete binding: %v", err)
	}
	if toks, _ := s.AllocatedTokens(ctx, "svinst-redis-shared-28"); contains(toks, "4") {
		t.Fatalf("删 binding 后 4 应回收，得 %v", toks)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: 跑测试验证失败**

Run:

```
GOPATH=C:/Users/yxt/go go test -p 1 -run 'TestStore_LookupShared|TestStore_AllocatedTokens|TestStore_ClaimSharedToken|TestStore_GetBinding|TestStore_shared_recycle' ./internal/mwsupply/
```

Expected: FAIL（方法 undefined）。

- [ ] **Step 3: 加 `isUniqueViolation` 到 norows.go**

把 `internal/mwsupply/norows.go` 整体替换为：

```go
package mwsupply

import (
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// isNoRows 判断是否「无行」错误（Lookup* 用：无实例/绑定返回 nil,nil）。
func isNoRows(err error) bool { return err == sql.ErrNoRows }

// isUniqueViolation 判断是否 PG 唯一约束冲突（错误码 23505）。
// shared token 并发兜底：partial unique index uq_svbind_inst_token 命中时调用方重试换号。
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
```

- [ ] **Step 4: 追加 4 方法到 store.go**

在 `internal/mwsupply/store.go` 的 `ListBindingsByApp` 后追加：

```go
// LookupShared 取某 kind 的平台级 shared 实例（project_space_id IS NULL）。无则 nil,nil。
func (s *Store) LookupShared(ctx context.Context, kind string) (*ServiceInstance, error) {
	var inst ServiceInstance
	err := s.db.GetContext(ctx, &inst,
		`SELECT `+instCols+` FROM appdeploy_service_instance
		 WHERE kind=$1 AND supply_mode='shared' AND status='active' AND project_space_id IS NULL
		 LIMIT 1`, kind)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return &inst, nil
}

// GetBinding 取某 app 某 kind 的绑定。无则 nil,nil。
func (s *Store) GetBinding(ctx context.Context, appID, kind string) (*ServiceBinding, error) {
	var b ServiceBinding
	err := s.db.GetContext(ctx, &b,
		`SELECT `+bindCols+` FROM appdeploy_service_binding WHERE app_id=$1 AND service_kind=$2`, appID, kind)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return &b, nil
}

// AllocatedTokens 列某实例所有已分配 token（isolation_token IS NOT NULL）。
// shared redis db 号分配的占用集来源。
func (s *Store) AllocatedTokens(ctx context.Context, instID string) ([]string, error) {
	var toks []string
	err := s.db.SelectContext(ctx, &toks,
		`SELECT isolation_token FROM appdeploy_service_binding
		 WHERE service_instance_id=$1 AND isolation_token IS NOT NULL`, instID)
	return toks, err
}

// ClaimSharedToken 原子登记 shared 绑定（strategy=shared,status=bound）。
// 复用 UpsertBinding 的 ON CONFLICT(app_id,service_kind)。
// 撞 (service_instance_id,isolation_token) 唯一索引 → DB 抛 23505，调用方 isUniqueViolation 捕获后换号重试。
func (s *Store) ClaimSharedToken(ctx context.Context, appID, psID, kind, instID, token, envKey string) error {
	return s.UpsertBinding(ctx, &ServiceBinding{
		AppID: appID, ProjectSpaceID: psID, ServiceKind: kind,
		Strategy: ModeShared, ServiceInstanceID: instID, IsolationToken: token,
		EnvKey: envKey, Status: StatusBound,
	})
}
```

- [ ] **Step 5: 跑测试验证通过**

Run:

```
GOPATH=C:/Users/yxt/go go test -p 1 -run 'TestStore_' ./internal/mwsupply/
```

Expected: PASS（含本 task 5 个 + 既有 LookupBindExisting_seed/UpserBinding_upsert）。

- [ ] **Step 6: Commit**

```bash
git add platform/backend/internal/mwsupply/store.go platform/backend/internal/mwsupply/norows.go platform/backend/internal/mwsupply/store_test.go
git commit -m "feat(mwsupply): store shared 方法 LookupShared/GetBinding/AllocatedTokens/ClaimSharedToken+isUniqueViolation"
```

---

## Task 5: supply.go 接入 shared 分支（`supplyShared`/`claimWithRetry`/`writeSharedEnv` + `NewReconciler` 加 flusher）

**Files:**

- Modify: `platform/backend/internal/mwsupply/supply.go`
- Modify: `platform/backend/internal/mwsupply/supply_test.go`（`newReconcilerTest` 传 fakeFlusher + `ensureSeed` 加 shared 种子 + 改既有 4 处调用 + 新增 shared 测试）

**Interfaces:**

- Consumes: Task 2 `ParseDBRange`/`pickLowestFree`、Task 3 `DBFlusher`/`NewRedisFlusher`、Task 4 store 4 方法 + `isUniqueViolation`、既有 `ConnStr`/`EnvKeyFor`/`UpsertEnv`
- Produces: `NewReconciler(store *Store, env EnvWriter, flusher DBFlusher)` 新签名（Task 6 main.go 消费）

- [ ] **Step 1: 写失败测试（先改测试脚手架 + 加 shared 用例）**

整体替换 `internal/mwsupply/supply_test.go` 为：

```go
package mwsupply

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
	"zhiyuan-anp/platform/backend/internal/appdeploy"
	"zhiyuan-anp/platform/backend/internal/testutil"
)

// fakeFlusher 记录 FlushDB 调用；err 非 nil 时返错（测 flush 失败路径）。
type fakeFlusher struct {
	calls int
	err   error
}

func (f *fakeFlusher) FlushDB(ctx context.Context, host string, port int, password string, db int) error {
	f.calls++
	return f.err
}

// newReconcilerTest 起 Reconciler（env 用真实 appdeploy.Store；flusher 用 fake）+ 清表 + 保 .28 种子（含 shared）。
func newReconcilerTest(t *testing.T) (*Reconciler, *appdeploy.Store, *sqlx.DB, *fakeFlusher) {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "appdeploy_service_binding", "appdeploy_env", "appdeploy_application")
	ensureSeed(t, db)
	appStore := appdeploy.NewStore(db)
	f := &fakeFlusher{}
	return NewReconciler(NewStore(db), appStore, f), appStore, db, f
}

// ensureSeed 确保 .28 redis/milvus bind_existing 种子 + shared redis 种子在（Truncate 不动 service_instance；幂等再插）。
func ensureSeed(t *testing.T, db *sqlx.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO appdeploy_service_instance (id, project_space_id, kind, name, supply_mode, host, port, isolation, status) VALUES
	  ('svinst-redis-28',NULL,'redis','yxt-redis','bind_existing','10.10.0.28',6381,'{"default_db":0}'::jsonb,'active'),
	  ('svinst-milvus-28',NULL,'milvus','yxt-milvus','bind_existing','10.10.0.28',19530,NULL,'active'),
	  ('svinst-redis-shared-28',NULL,'redis','yxt-redis-shared','shared','10.10.0.28',6381,'{"db_range":[1,15]}'::jsonb,'active')
	  ON CONFLICT (id) DO NOTHING`)
	if err != nil {
		t.Fatalf("ensureSeed: %v", err)
	}
}

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".anp"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".anp", "deps.yaml"), []byte(body), 0o644)
	return dir
}

// —— 既有 bind_existing 用例（newReconcilerTest 返回值变 4 元，改 _ 接收 flusher）——

func TestReconcile_bindExisting(t *testing.T) {
	r, appStore, db, _ := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "rcapp", RepoDir: "/data/repos/rcapp", InternalPort: 8080}
	if err := appStore.Create(ctx, a); err != nil {
		t.Fatalf("create app: %v", err)
	}
	dir := writeManifest(t, "services:\n  - kind: redis\n  - kind: milvus\n")
	if err := r.Reconcile(ctx, a.ID, "ps_1", dir); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	ra, _ := appStore.GetEnvValue(ctx, a.ID, "REDIS_ADDR")
	if ra != "10.10.0.28:6381" {
		t.Fatalf("REDIS_ADDR 应 10.10.0.28:6381，得 %q", ra)
	}
	ma, _ := appStore.GetEnvValue(ctx, a.ID, "MILVUS_ADDR")
	if ma != "10.10.0.28:19530" {
		t.Fatalf("MILVUS_ADDR 应 10.10.0.28:19530，得 %q", ma)
	}
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 2 {
		t.Fatalf("应 2 binding，得 %d", len(binds))
	}
}

func TestReconcile_idempotent(t *testing.T) {
	r, appStore, _, _ := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "rcapp2", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: redis\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	if err := r.Reconcile(ctx, a.ID, "ps_1", dir); err != nil {
		t.Fatalf("二次 reconcile 不应报错: %v", err)
	}
}

func TestReconcile_missingInstanceKind(t *testing.T) {
	r, appStore, db, _ := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "rcapp3", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: mongodb\n")
	if err := r.Reconcile(ctx, a.ID, "ps_1", dir); err != nil {
		t.Fatalf("未注册 kind 不应报错: %v", err)
	}
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 1 || binds[0].Status != StatusFailed {
		t.Fatalf("未注册 kind 应 binding failed，得 %+v", binds)
	}
}

func TestReconcile_noManifest(t *testing.T) {
	r, appStore, db, _ := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "rcapp4", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	if err := r.Reconcile(ctx, a.ID, "ps_1", t.TempDir()); err != nil {
		t.Fatalf("无清单不应报错: %v", err)
	}
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 0 {
		t.Fatalf("无清单应 0 binding，得 %d", len(binds))
	}
}

// —— shared 用例 ——

// TestReconcile_sharedRedis 两个 shared app 分到不同 db 号；双 env + flush 各 1 次。
func TestReconcile_sharedRedis(t *testing.T) {
	r, appStore, db, fl := newReconcilerTest(t)
	ctx := context.Background()
	dir := writeManifest(t, "services:\n  - kind: redis\n    strategy: shared\n")

	mk := func(name string) string {
		a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: name, RepoDir: "/x", InternalPort: 8080}
		_ = appStore.Create(ctx, a)
		_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
		return a.ID
	}
	a1 := mk("sh1")
	a2 := mk("sh2")

	db1, _ := appStore.GetEnvValue(ctx, a1, "REDIS_DB")
	db2, _ := appStore.GetEnvValue(ctx, a2, "REDIS_DB")
	if db1 == "" || db2 == "" || db1 == db2 {
		t.Fatalf("两 app REDIS_DB 应不同且非空，得 %q / %q", db1, db2)
	}
	for _, aid := range []string{a1, a2} {
		ra, _ := appStore.GetEnvValue(ctx, aid, "REDIS_ADDR")
		if ra != "10.10.0.28:6381" {
			t.Fatalf("REDIS_ADDR 应 10.10.0.28:6381，得 %q", ra)
		}
		src, _ := appStore.GetEnvSource(ctx, aid, "REDIS_DB")
		if src != "platform" {
			t.Fatalf("REDIS_DB source 应 platform，得 %q", src)
		}
	}
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a1)
	if len(binds) != 1 || binds[0].Status != StatusBound || binds[0].IsolationToken != db1 {
		t.Fatalf("a1 binding 应 bound token=%s，得 %+v", db1, binds)
	}
	if fl.calls != 2 {
		t.Fatalf("flush 应调 2 次（每 app 新分配 1 次），得 %d", fl.calls)
	}
}

// TestReconcile_shared_idempotent 同 app 重部署：号不变、不再 flush、env 仍在。
func TestReconcile_shared_idempotent(t *testing.T) {
	r, appStore, _, fl := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "shidem", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: redis\n    strategy: shared\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	db1, _ := appStore.GetEnvValue(ctx, a.ID, "REDIS_DB")
	fl.calls = 0 // 重置计数
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir) // 重部署
	db2, _ := appStore.GetEnvValue(ctx, a.ID, "REDIS_DB")
	if db1 != db2 {
		t.Fatalf("重部署 db 号应不变，%q → %q", db1, db2)
	}
	if fl.calls != 0 {
		t.Fatalf("重部署复用不应 flush，得 %d 次", fl.calls)
	}
}

// TestReconcile_shared_flushFail flush 失败 → binding failed、未写 REDIS_DB。
func TestReconcile_shared_flushFail(t *testing.T) {
	r, appStore, db, fl := newReconcilerTest(t)
	fl.err = errStr("redis 不可达")
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "shfail", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: redis\n    strategy: shared\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 1 || binds[0].Status != StatusFailed {
		t.Fatalf("flush 失败应 binding failed，得 %+v", binds)
	}
	rdb, _ := appStore.GetEnvValue(ctx, a.ID, "REDIS_DB")
	if rdb != "" {
		t.Fatalf("flush 失败不应写 REDIS_DB，得 %q", rdb)
	}
}

// TestReconcile_shared_poolExhaust 占满 1-15 后第 16 个 → failed。
func TestReconcile_shared_poolExhaust(t *testing.T) {
	r, appStore, db, _ := newReconcilerTest(t)
	ctx := context.Background()
	dir := writeManifest(t, "services:\n  - kind: redis\n    strategy: shared\n")
	for i := 0; i < 15; i++ {
		a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "ex" + rune('a'+i), RepoDir: "/x", InternalPort: 8080}
		_ = appStore.Create(ctx, a)
		if err := r.Reconcile(ctx, a.ID, "ps_1", dir); err != nil {
			t.Fatalf("前 15 不应错: %v", err)
		}
	}
	a16 := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "ex16", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a16)
	_ = r.Reconcile(ctx, a16.ID, "ps_1", dir)
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a16.ID)
	if len(binds) != 1 || binds[0].Status != StatusFailed {
		t.Fatalf("第 16 个应 failed（池满），得 %+v", binds)
	}
}

// errStr 造个简单 error（避免引入 errors 包到测试）。
type errStr string
func (e errStr) Error() string { return string(e) }
```

> 注：`TestReconcile_shared_flushFail` 用 `fl.err = errStr(...)`；`errStr` 实现了 `error`。`fakeFlusher.err` 字段类型为 `error`，赋值 `errStr` 合法。

- [ ] **Step 2: 跑测试验证失败**

Run:

```
GOPATH=C:/Users/yxt/go go test -p 1 -run 'TestReconcile_shared' ./internal/mwsupply/
```

Expected: 编译失败（`NewReconciler` 仍 2 参）或 FAIL。

- [ ] **Step 3: 改 supply.go（Reconciler + flusher、NewReconciler、shared 分支）**

整体替换 `internal/mwsupply/supply.go` 为：

```go
package mwsupply

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// EnvWriter 写应用 env（由 appdeploy.Store 实现，避免 mwsupply→appdeploy 循环依赖）。
type EnvWriter interface {
	UpsertEnv(ctx context.Context, appID, key, value string, isSecret bool, source string) error
}

// Reconciler 中间件依赖供给。best-effort：失败记 binding，不阻塞部署。
type Reconciler struct {
	store   *Store
	env     EnvWriter
	flusher DBFlusher // shared 重分配时清空 redis db（Task 3）
}

// NewReconciler 构造。env 传 appdeploy.Store（满足 EnvWriter）；flusher 传 NewRedisFlusher()（测试传 fake）。
func NewReconciler(store *Store, env EnvWriter, flusher DBFlusher) *Reconciler {
	return &Reconciler{store: store, env: env, flusher: flusher}
}

// Reconcile 读 repoDir 的 .anp/deps.yaml → 对每个声明服务按策略供给 → 写 env + binding。
// 幂等（binding 已 bound 且同实例则复用 token 不 flush）。读清单失败=空清单（不报错）。
// 总不返回错（best-effort，不阻塞部署）。
func (r *Reconciler) Reconcile(ctx context.Context, appID, psID, repoDir string) error {
	m, err := LoadDepsManifest(repoDir)
	if err != nil || m == nil {
		return nil
	}
	for _, dep := range m.Services {
		if dep.Kind == "" {
			continue
		}
		r.supplyOne(ctx, appID, psID, dep)
	}
	return nil
}

// supplyOne 供给单个依赖。bind_existing（P1）/ shared（P2 redis）；dedicated 暂 failed（P3）。
func (r *Reconciler) supplyOne(ctx context.Context, appID, psID string, dep DepService) {
	strategy := dep.Strategy
	if strategy == "" {
		strategy = ModeBindExisting
	}
	envKey := EnvKeyFor(dep.Kind)
	// mkBind 幂等 upsert binding（token：bind_existing 空；shared 填分配号）。
	mkBind := func(status, instID, token, lastErr string) {
		_ = r.store.UpsertBinding(ctx, &ServiceBinding{
			AppID: appID, ProjectSpaceID: psID, ServiceKind: dep.Kind,
			Strategy: strategy, ServiceInstanceID: instID, IsolationToken: token,
			EnvKey: envKey, Status: status, LastError: lastErr,
		})
	}

	if strategy == ModeShared {
		r.supplyShared(ctx, appID, psID, dep, mkBind)
		return
	}
	if strategy != ModeBindExisting {
		mkBind(StatusFailed, "", "", "策略 "+strategy+" 暂未实现（仅 bind_existing/shared）")
		return
	}

	// —— bind_existing（P1，不动）——
	inst, err := r.store.LookupBindExisting(ctx, psID, dep.Kind)
	if err != nil || inst == nil {
		mkBind(StatusFailed, "", "", "无可绑定的 "+dep.Kind+" 实例")
		return
	}
	connStr := ConnStr(inst)
	isSecret := inst.AuthRef != ""
	if err := r.env.UpsertEnv(ctx, appID, envKey, connStr, isSecret, "platform"); err != nil {
		mkBind(StatusFailed, inst.ID, "", err.Error())
		return
	}
	mkBind(StatusBound, inst.ID, "", "")
}

// supplyShared shared redis 供给：复用判定（幂等不换号不 flush）/ 新分配（最小空闲号 → flush → claim）。
func (r *Reconciler) supplyShared(ctx context.Context, appID, psID string, dep DepService,
	mkBind func(status, instID, token, lastErr string)) {
	inst, err := r.store.LookupShared(ctx, dep.Kind)
	if err != nil || inst == nil {
		mkBind(StatusFailed, "", "", "无 shared "+dep.Kind+" 实例")
		return
	}
	// 复用：同 app 已 bound 同实例同 token → 不换号、不 flush（保数据）、重写 env。
	if existing, e := r.store.GetBinding(ctx, appID, dep.Kind); e == nil && existing != nil &&
		existing.Status == StatusBound && existing.IsolationToken != "" && existing.ServiceInstanceID == inst.ID {
		r.writeSharedEnv(ctx, appID, inst, existing.IsolationToken)
		mkBind(StatusBound, inst.ID, existing.IsolationToken, "")
		return
	}
	// 新分配
	lo, hi, ok := ParseDBRange(inst.Isolation)
	if !ok {
		mkBind(StatusFailed, inst.ID, "", "shared 实例 isolation 缺 db_range")
		return
	}
	allocated, _ := r.store.AllocatedTokens(ctx, inst.ID)
	first, found := pickLowestFree(lo, hi, allocated)
	if !found {
		mkBind(StatusFailed, inst.ID, "", fmt.Sprintf("shared redis db 号耗尽（池 %d-%d）", lo, hi))
		return
	}
	token, err := r.claimWithRetry(ctx, appID, psID, dep.Kind, inst, lo, hi, first, allocated)
	if err != nil {
		mkBind(StatusFailed, inst.ID, "", err.Error())
		return
	}
	r.writeSharedEnv(ctx, appID, inst, token)
	mkBind(StatusBound, inst.ID, token, "")
}

// claimWithRetry flush 后原子 claim；撞唯一索引（并发抢同号）→ 刷新占用集换号重试，有界 ≤ 池大小。
// 返回最终 claim 到的 token。
func (r *Reconciler) claimWithRetry(ctx context.Context, appID, psID, kind string, inst *ServiceInstance,
	lo, hi int, first string, allocated []string) (string, error) {
	token := first
	seen := append([]string{}, allocated...)
	for attempts := 0; attempts <= (hi - lo + 1); attempts++ {
		dbNum, _ := strconv.Atoi(token)
		if ferr := r.flusher.FlushDB(ctx, inst.Host, inst.Port, inst.AuthRef, dbNum); ferr != nil {
			return "", fmt.Errorf("flush db %s 失败: %w", token, ferr)
		}
		err := r.store.ClaimSharedToken(ctx, appID, psID, kind, inst.ID, token, EnvKeyFor(kind))
		if err == nil {
			return token, nil
		}
		if !isUniqueViolation(err) {
			return "", err // 非冲突，真错
		}
		seen = append(seen, token)
		next, found := pickLowestFree(lo, hi, seen)
		if !found {
			return "", fmt.Errorf("shared redis db 号耗尽（并发重试）")
		}
		token = next
	}
	return "", fmt.Errorf("claim 重试用尽")
}

// writeSharedEnv 写 REDIS_ADDR + REDIS_DB（+ REDIS_PASSWORD 若鉴权），均 source=platform。
func (r *Reconciler) writeSharedEnv(ctx context.Context, appID string, inst *ServiceInstance, token string) {
	kindUp := strings.ToUpper(inst.Kind) // redis→REDIS
	_ = r.env.UpsertEnv(ctx, appID, kindUp+"_ADDR", ConnStr(inst), false, "platform")
	_ = r.env.UpsertEnv(ctx, appID, kindUp+"_DB", token, false, "platform")
	if inst.AuthRef != "" {
		_ = r.env.UpsertEnv(ctx, appID, kindUp+"_PASSWORD", inst.AuthRef, true, "platform")
	}
}
```

- [ ] **Step 4: 跑测试验证通过**

Run:

```
GOPATH=C:/Users/yxt/go go test -p 1 ./internal/mwsupply/
```

Expected: PASS（含既有 bind_existing + 4 个 shared 用例 + Task 1/2/3/4 全部）。

- [ ] **Step 5: Commit**

```bash
git add platform/backend/internal/mwsupply/supply.go platform/backend/internal/mwsupply/supply_test.go
git commit -m "feat(mwsupply): supply shared 分支(db号分配+重分配flush+复用幂等)+NewReconciler加flusher"
```

---

## Task 6: main.go 装配 flusher

**Files:**

- Modify: `platform/backend/cmd/server/main.go:185`

**Interfaces:**

- Consumes: Task 3 `NewRedisFlusher()`、Task 5 新 `NewReconciler(store, env, flusher)`

- [ ] **Step 1: 改 main.go 一行**

`cmd/server/main.go:185` 由：

```go
	mwReconciler := mwsupply.NewReconciler(mwStore, appDeployStore) // appDeployStore 满足 mwsupply.EnvWriter
```

改为：

```go
	mwReconciler := mwsupply.NewReconciler(mwStore, appDeployStore, mwsupply.NewRedisFlusher()) // appDeployStore 满足 mwsupply.EnvWriter
```

- [ ] **Step 2: 编译 + vet 验证**

Run（from `platform/backend`）:

```
GOPATH=C:/Users/yxt/go go build ./...
GOPATH=C:/Users/yxt/go go vet ./internal/mwsupply/... ./cmd/server/...
```

Expected: 无错。

- [ ] **Step 3: Commit**

```bash
git add platform/backend/cmd/server/main.go
git commit -m "feat(main): NewReconciler 装配 NewRedisFlusher(P2 shared)"
```

---

## Task 7: 全量回归 + `.28` 端到端验证

**Files:** 无代码改动（验证 + 部署）

> 遵循 memory `verify-cross-frontend-backend`（有测试用例 + 真驱动）+ `deploy-28-no-local-test`（本机不跑功能测试，`.28` 是测试库）+ `deploy-prod-10.10.0.28`（scp + docker-compose 重建）。

- [ ] **Step 1: 全量回归（串行）**

Run（from `platform/backend`）:

```
GOPATH=C:/Users/yxt/go go test -p 1 ./...
```

Expected: PASS（含 mwsupply 全部、appdeploy handler、pgsupply 等无回归）。如有非本次引入的既有失败，记录但不阻断（仅确认 mwsupply/appdeploy 绿）。

- [ ] **Step 2: push**

```bash
git push origin main
```

- [ ] **Step 3: scp 源码到 .28 + 重建（按 deploy-prod-10.10.0.28）**

向 `10.10.0.28:/opt/anp` scp 改动（或整目录同步），docker-compose 重建后端容器。迁移 000029 自动 apply。

> 具体命令按记忆 `deploy-prod-10.10.0.28`：keyless SSH、`/opt/anp` 源码、scp + docker-compose 重建、入口 8088。

- [ ] **Step 4: `.28` e2e —— shared 隔离 + 回收**

造两个最小 Go 应用（仿 P1 e2e：`golang:1.25-alpine` 本地镜像，`.anp/deps.yaml` 预写 `services:[{kind:redis, strategy:shared}]`，CREATE 带 `repo_dir` 不触发 adapt）→ 各 deploy test，验证：

1. app1 容器内 `REDIS_DB=1`、app2 `REDIS_DB=2`（**隔离号不同**），`REDIS_ADDR=10.10.0.28:6381` 同
2. `appdeploy_env` 每 app 有 `REDIS_ADDR`+`REDIS_DB` 两行 `source=platform`
3. `appdeploy_service_binding` 各一行 `strategy=shared, isolation_token=1/2, status=bound`
4. **回收**：删 app1 → binding/env CASCADE 删 → 新建 app3 deploy → `REDIS_DB=1`（复用最小号）+ db 1 已 flush（app3 写 key 读不到 app1 残留，可用 `redis-cli -h 10.10.0.28 -p 6381 -n 1` 验 db 1 为空后 app3 写入）
5. **平台保护**：手改 `REDIS_DB` 返 409（复用 P1 source=platform 保护）

- [ ] **Step 5: 记录结论**

把 e2e 结论（容器内 env、binding 行、回收复用 + flush 验证）补进本 plan 末尾或 memory `import-adapt-reuse-coding` 的 P2 段。

---

## Self-Review（plan 作者自查，已完成）

**1. Spec coverage：**

- §3 隔离=db 号 → Task 2 `pickLowestFree` + Task 5 ✓
- §3 db_range[1,15] → Task 1 种子 + Task 5 `ParseDBRange` ✓
- §3 配额=db_range 不动 quota → Task 5 池满 `failed`（无 quota 改动，Task 列表无 quota 文件）✓
- §3 乐观+唯一索引+重试 → Task 1 索引 + Task 4 `isUniqueViolation` + Task 5 `claimWithRetry` ✓
- §3 重分配 flush（方案 A）→ Task 3 flusher + Task 5 `claimWithRetry` 调 flush ✓
- §3 裸 RESP net.Dial 不引依赖 → Task 3（无 go.mod 改动）✓
- §3 CASCADE 回收 Delete 零改 → 无 handler.go/Delete task（明确不改）✓
- §3 双 env REDIS_ADDR+REDIS_DB(+PASSWORD) → Task 5 `writeSharedEnv` ✓
- §4 迁移 000029 → Task 1 ✓
- §5 分配算法/状态机 → Task 5（复用/新分配/flush 失败/池满 全有用例）✓
- §6 env 注入 → Task 5 ✓
- §7 flush/DBFlusher → Task 3 ✓
- §8 回收 → Task 7 e2e step 4 验证 ✓
- §9 并发/失败 → Task 4 uniqueViolation 测试 + Task 5 flushFail/poolExhaust/idempotent ✓
- §10 文件改动 → 每个 task 的 Files 块逐一对应 ✓
- §11 测试计划 → Task 1-5 测试 + Task 7 e2e ✓
- §14 验收 8 条 → Task 5 测试覆盖 1/2/3/5/6，Task 4 覆盖 4（回收），Task 7 e2e 覆盖 4/7/8 ✓

**2. Placeholder scan：** 无 TBD/TODO/"add error handling"/"similar to" 占位；每步含完整代码。

**3. Type consistency：**

- `NewReconciler(store, env, flusher DBFlusher)` —— Task 3 定义 `DBFlusher`、Task 5 定义新签名、Task 6 调用，三处一致 ✓
- `mkBind(status, instID, token, lastErr)` —— Task 5 定义与所有调用点（含 bind_existing 既有 3 处 + shared）签名一致（token 为新增第 3 参，已全量改写 supply.go）✓
- `ClaimSharedToken(ctx, appID, psID, kind, instID, token, envKey)` —— Task 4 定义、Task 5 `claimWithRetry` 调用一致 ✓
- `pickLowestFree(lo, hi, allocated) (string, bool)` / `ParseDBRange(isolation) (lo,hi,ok)` —— Task 2 定义、Task 5 调用一致 ✓
- `fakeFlusher.FlushDB(ctx, host, port, password, db)` —— Task 5 测试定义，与 Task 3 `DBFlusher` 接口签名一致 ✓

无类型/签名漂移。

---

_本计划把 P2 shared redis spec 落成 7 个 TDD task：迁移 → db_range 纯逻辑 → redis flusher → store shared 方法 → supply 接入 → main 装配 → 回归 + .28 e2e。每 task 含失败测试 + 完整实现 + 命令 + commit。审核通过后用 subagent-driven-development 或 executing-plans 执行。_

# Dedicated Milvus 实现计划（P4）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让应用在 `.anp/deps.yaml` 声明 `kind: milvus, strategy: dedicated` 时，平台为其起一个专属 milvus standalone 栈（milvus+etcd+minio 三容器 + 专属网络），注入 `MILVUS_ADDR`，删 app 回收，重部署复用保数据。

**Architecture:** 把 `mwsupply` 的 dedicated 流程从 redis 专写重构为「按 kind 分派」的共享骨架（launch/ready/env/rm 四处分派），redis 路径逐字保留；milvus 起专属 docker 网络 + milvus/etcd/minio 三容器（1:1 复刻 .28 yxt-milvus 配方），alpine 探针长超时就绪（best-effort 降级），Cleanup 按 kind rm 三容器 + 网络。零迁移、零 main.go、零 handler 改动（P3 已铺好 Cleanup 接入）。

**Tech Stack:** Go 1.25（`platform/backend`）、PostgreSQL（pgvector，单测用真 PG）、docker CLI（经 `os/exec`）、表驱动 env 注入。

**关联 spec:** `docs/superpowers/specs/2026-08-02-中间件依赖注入-P4-dedicated-milvus-design.md`

## Global Constraints

- **禁 SQLite 只用 PG**：单测用 `testutil.TestDB(t)`（真 PG `anp_test` 库），不回退 sqlite（记忆 `no-sqlite-pg-only` / `sqlite-test-pg-type-trap`）。
- **全量回归串行**：`go test -p 1 ./internal/mwsupply/...`（并发污染 anp_test 库，记忆 `go-test-serial-p1`）。
- **GOPATH 前缀**：所有 `go` 命令前缀 `GOPATH=C:/Users/yxt/go`（本机 GOPATH 被污染，记忆 `gopath-pollution-windows`）。
- **redis dedicated 零回归**：既有 redis dedicated 单测（`TestReconcile_dedicatedRedis` 等）行为须不变、全绿（spec §11.1#9）。
- **零迁移/零 main.go/零 handler**：`container_name` 列复用 000030；`NewReconciler`/`SetMwReconciler`/Delete Cleanup 接入 P3 已完成。
- **镜像/端口常量**（spec §3，逐字）：`milvusdb/milvus:v2.6.15`、`quay.io/coreos/etcd:v3.5.16`、`minio:v20.2.5-2024.7.4`、`alpine:3.19`；milvus 端口池 `9700-9799`（redis 占 9600-9699）。
- **commit 规范**：所有 commit message 末尾加一行 `Co-Authored-By: Claude <noreply@anthropic.com>`；type 用 `feat(mwsupply):` / `test(mwsupply):` / `refactor(mwsupply):`。
- **测试断言可中文**（既有用例即如此）。

## File Structure

| 文件                                                | 责任                                                           | 本计划动作                                                                                                                      |
| --------------------------------------------------- | -------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| `platform/backend/internal/mwsupply/naming.go`      | dedicated 命名/常量纯函数                                      | 改：加 milvus 常量、`portRange`、`dedicatedContainerName(kind,short)`、栈名派生                                                 |
| `platform/backend/internal/mwsupply/naming_test.go` | naming 单测                                                    | 改：更新签名用例 + 加 milvus 用例                                                                                               |
| `platform/backend/internal/mwsupply/docker.go`      | `MWDockerRunner` 接口 + osDocker（docker CLI）+ 纯 args 函数   | 改：接口 +`RunMilvusStack`/`MilvusReady`/`RmMilvusStack`；纯函数 `etcdRunArgs`/`minioRunArgs`/`milvusRunArgs`/`milvusProbeArgs` |
| `platform/backend/internal/mwsupply/docker_test.go` | docker 纯 args 单测                                            | 改：加 milvus args 用例                                                                                                         |
| `platform/backend/internal/mwsupply/supply.go`      | `Reconciler`：supplyDedicated + writeDedicatedEnv + Cleanup    | 改：supplyDedicated kind 分派骨架 + 分派 helper + writeDedicatedEnv milvus 分支 + Cleanup kind 分派                             |
| `platform/backend/internal/mwsupply/supply_test.go` | Reconciler PG 单测 + fakeDocker/fakeFlusher                    | 改：fakeDocker 加 milvus 方法；加 milvus dedicated 用例                                                                         |
| **不改**                                            | model.go / store.go / connstr.go / 迁移 / main.go / handler.go | —                                                                                                                               |

---

## Task 1: naming.go —— milvus 常量与命名 helpers

**Files:**

- Modify: `platform/backend/internal/mwsupply/naming.go`
- Modify: `platform/backend/internal/mwsupply/naming_test.go`
- Modify: `platform/backend/internal/mwsupply/supply.go:200`（仅 1 行调用点跟签名）

**Interfaces:**

- Produces: 常量 `milvusPortMin/Max`、`milvusImage`、`etcdImage`、`minioImage`、`milvusGrpcPort`、`milvusHealthPort`、`etcdInternalPort`、`minioInternalPort`、`milvusReadyTimeout`、`readyAlpineImage`；函数 `portRange(kind string) (int,int)`、`dedicatedContainerName(kind, short string) string`、`milvusEtcdName(base string) string`、`milvusMinioName(base string) string`、`milvusNetName(base string) string`。Task 2/3 消费。

- [ ] **Step 1: 写失败测试（更新 naming_test.go）**

把 `naming_test.go` 的 `TestDedicatedContainerName` 与 `TestConstants` 替换/扩展为下面版本（新增 milvus 用例 + 签名改 2 参）：

```go
func TestDedicatedContainerName(t *testing.T) {
	if n := dedicatedContainerName("redis", "abc123"); n != "mwredis-abc123" {
		t.Fatalf("redis 容器名应 mwredis-abc123，得 %q", n)
	}
	if n := dedicatedContainerName("milvus", "abc123"); n != "mwmilvus-abc123" {
		t.Fatalf("milvus 容器名应 mwmilvus-abc123，得 %q", n)
	}
}

func TestPortRange(t *testing.T) {
	if lo, hi := portRange("redis"); lo != mwPortMin || hi != mwPortMax {
		t.Fatalf("redis 端口池应 %d-%d，得 %d-%d", mwPortMin, mwPortMax, lo, hi)
	}
	if lo, hi := portRange("milvus"); lo != milvusPortMin || hi != milvusPortMax {
		t.Fatalf("milvus 端口池应 %d-%d，得 %d-%d", milvusPortMin, milvusPortMax, lo, hi)
	}
}

func TestMilvusStackNames(t *testing.T) {
	base := dedicatedContainerName("milvus", "abc123") // mwmilvus-abc123
	if milvusEtcdName(base) != "mwmilvus-abc123-etcd" {
		t.Fatalf("etcd 名错: %q", milvusEtcdName(base))
	}
	if milvusMinioName(base) != "mwmilvus-abc123-minio" {
		t.Fatalf("minio 名错: %q", milvusMinioName(base))
	}
	if milvusNetName(base) != "mwmilvus-abc123-net" {
		t.Fatalf("net 名错: %q", milvusNetName(base))
	}
}

func TestConstants(t *testing.T) {
	if mwPortMin != 9600 || mwPortMax != 9699 {
		t.Fatalf("redis 端口池应 9600-9699，得 %d-%d", mwPortMin, mwPortMax)
	}
	if redisImage != "redis:7-alpine" || redisInternalPort != 6379 {
		t.Fatalf("redis 镜像/端口不符: %s/%d", redisImage, redisInternalPort)
	}
	if readyTimeout != 15*time.Second {
		t.Fatalf("readyTimeout 应 15s，得 %v", readyTimeout)
	}
}

func TestMilvusConstants(t *testing.T) {
	if milvusPortMin != 9700 || milvusPortMax != 9799 {
		t.Fatalf("milvus 端口池应 9700-9799，得 %d-%d", milvusPortMin, milvusPortMax)
	}
	if milvusImage != "milvusdb/milvus:v2.6.15" || etcdImage != "quay.io/coreos/etcd:v3.5.16" || minioImage != "minio:v20.2.5-2024.7.4" {
		t.Fatalf("milvus 栈镜像不符: %s/%s/%s", milvusImage, etcdImage, minioImage)
	}
	if milvusGrpcPort != 19530 || milvusHealthPort != 9091 || etcdInternalPort != 2379 || minioInternalPort != 9000 {
		t.Fatalf("milvus 端口不符: grpc=%d health=%d etcd=%d minio=%d", milvusGrpcPort, milvusHealthPort, etcdInternalPort, minioInternalPort)
	}
	if milvusReadyTimeout != 120*time.Second {
		t.Fatalf("milvusReadyTimeout 应 120s，得 %v", milvusReadyTimeout)
	}
	if readyAlpineImage != "alpine:3.19" {
		t.Fatalf("探针镜像应 alpine:3.19，得 %q", readyAlpineImage)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd platform/backend && GOPATH=C:/Users/yxt/go go test -run 'TestDedicatedContainerName|TestPortRange|TestMilvusStackNames|TestMilvusConstants' ./internal/mwsupply/`
Expected: 编译失败（`milvusPortMin` undefined / `dedicatedContainerName` 需要 2 参）。

- [ ] **Step 3: 改 naming.go（加常量 + 改签名 + 加 helper）**

在 `naming.go` 现有 `const (...)` 块**之后**新增 milvus 常量块（不改动既有 redis 常量）：

```go
// dedicated milvus 供给常量（1:1 复刻 .28 yxt-milvus 配方）。
const (
	milvusPortMin     = 9700              // milvus dedicated 端口池下界（redis 占 9600-9699，避开）
	milvusPortMax     = 9799              // 上界（100 槽）
	milvusImage       = "milvusdb/milvus:v2.6.15"
	etcdImage         = "quay.io/coreos/etcd:v3.5.16"
	minioImage        = "minio:v20.2.5-2024.7.4"
	milvusGrpcPort    = 19530             // milvus gRPC（publish 到宿主）
	milvusHealthPort  = 9091              // milvus HTTP 健康/指标（就绪探针用，不 publish）
	etcdInternalPort  = 2379              // etcd 内部端口（milvus 经 ETCD_ENDPOINTS 访问）
	minioInternalPort = 9000              // minio 内部端口（milvus 经 MINIO_ADDRESS 访问）
	milvusReadyTimeout = 120 * time.Second // milvus 慢启动就绪探针上限
	readyAlpineImage  = "alpine:3.19"     // 就绪探针镜像（.28 已缓存）
)

// portRange 按 kind 给 dedicated 端口池上下界。
func portRange(kind string) (int, int) {
	if kind == "milvus" {
		return milvusPortMin, milvusPortMax
	}
	return mwPortMin, mwPortMax
}
```

把现有 `dedicatedContainerName` 改为 2 参（输出不变 + 加 milvus）：

```go
// dedicatedContainerName 按 kind 前缀：redis→mwredis- / milvus→mwmilvus-。
// 现有 redis 调用点改为传 kind="redis"，输出仍是 mwredis-<short>（零回归）。
func dedicatedContainerName(kind, short string) string {
	if kind == "milvus" {
		return "mwmilvus-" + short
	}
	return "mwredis-" + short
}

// milvus 栈命名：从 base（=container_name）确定性派生 sidecar 容器名与网络名。
func milvusEtcdName(base string) string  { return base + "-etcd" }
func milvusMinioName(base string) string { return base + "-minio" }
func milvusNetName(base string) string   { return base + "-net" }
```

- [ ] **Step 4: 改 supply.go:200 调用点跟新签名（保持编译）**

`supply.go` 的 `supplyDedicated` 现有这行：

```go
	name := dedicatedContainerName(short)
```

改为（传 `dep.Kind`，redis 输出不变）：

```go
	name := dedicatedContainerName(dep.Kind, short)
```

> 仅此 1 行改动，让包编译通过；真正的 supplyDedicated 重构在 Task 3。

- [ ] **Step 5: 跑测试确认通过**

Run: `cd platform/backend && GOPATH=C:/Users/yxt/go go test -run 'TestDedicatedContainerName|TestPortRange|TestMilvusStackNames|TestConstants|TestMilvusConstants|TestReconcile_dedicatedRedis|TestReconcile_dedicated_idempotent' ./internal/mwsupply/`
Expected: PASS（新 naming 用例绿；redis 既有用例仍绿，零回归）。

- [ ] **Step 6: 全量 mwsupply 回归 + commit**

Run: `cd platform/backend && GOPATH=C:/Users/yxt/go go test -p 1 ./internal/mwsupply/...`
Expected: PASS（全绿）。

```bash
git add platform/backend/internal/mwsupply/naming.go platform/backend/internal/mwsupply/naming_test.go platform/backend/internal/mwsupply/supply.go
git commit -m "feat(mwsupply): milvus dedicated 命名/常量/portRange(kind)+栈名派生

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 2: docker.go —— milvus 栈编排 / 就绪 / 回收

**Files:**

- Modify: `platform/backend/internal/mwsupply/docker.go`
- Modify: `platform/backend/internal/mwsupply/docker_test.go`
- Modify: `platform/backend/internal/mwsupply/supply_test.go`（fakeDocker 加 3 个方法满足新接口）

**Interfaces:**

- Consumes: Task 1 的 `milvusEtcdName/MinioName/NetName`、`milvusImage/etcdImage/minioImage`、`milvusGrpcPort/HealthPort`、`etcdInternalPort/minioInternalPort`、`milvusReadyTimeout`、`readyAlpineImage`；既有 `runDockerCmd`。
- Produces: `MWDockerRunner` 接口新增 `RunMilvusStack(ctx, base string, port int) error` / `MilvusReady(ctx, base string, timeout time.Duration) error` / `RmMilvusStack(ctx, base string) error`；纯函数 `etcdRunArgs(base) []string` / `minioRunArgs(base) []string` / `milvusRunArgs(base string, port int) []string` / `milvusProbeArgs(base) []string`。Task 3 消费。

- [ ] **Step 1: 写失败测试（docker_test.go 加纯 args 用例）**

在 `docker_test.go` 末尾追加：

```go
func TestMilvusRunArgs(t *testing.T) {
	got := milvusRunArgs("mwmilvus-abc", 9701)
	want := []string{
		"run", "-d", "--name", "mwmilvus-abc",
		"--network", "mwmilvus-abc-net", "--network-alias", "milvus",
		"--restart", "unless-stopped",
		"-e", "ETCD_ENDPOINTS=etcd:2379",
		"-e", "MINIO_ADDRESS=minio:9000",
		"-p", "9701:19530",
		"milvusdb/milvus:v2.6.15",
		"milvus", "run", "standalone",
	}
	if len(got) != len(want) {
		t.Fatalf("milvus args 数应 %d，得 %d: %v", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("milvus args[%d] 想 %q 得 %q", i, w, got[i])
		}
	}
}

func TestEtcdRunArgs(t *testing.T) {
	got := etcdRunArgs("mwmilvus-abc")
	// 关键断言：容器名 mwmilvus-abc-etcd、网络 mwmilvus-abc-net、别名 etcd、镜像、cmd
	for _, w := range []string{"--name", "mwmilvus-abc-etcd", "--network", "mwmilvus-abc-net", "--network-alias", "etcd", etcdImage, "-advertise-client-urls=http://etcd:2379", "--data-dir", "/etcd"} {
		if !contains(got, w) {
			t.Errorf("etcd args 应含 %q，得 %v", w, got)
		}
	}
}

func TestMinioRunArgs(t *testing.T) {
	got := minioRunArgs("mwmilvus-abc")
	for _, w := range []string{"--name", "mwmilvus-abc-minio", "--network", "mwmilvus-abc-net", "--network-alias", "minio", "-e", "MINIO_ACCESS_KEY=minioadmin", "-e", "MINIO_SECRET_KEY=minioadmin", minioImage, "server", "/minio_data"} {
		if !contains(got, w) {
			t.Errorf("minio args 应含 %q，得 %v", w, got)
		}
	}
}

func TestMilvusProbeArgs(t *testing.T) {
	got := milvusProbeArgs("mwmilvus-abc")
	for _, w := range []string{"--rm", "--network", "mwmilvus-abc-net", readyAlpineImage, "wget", "-qO-", "-T", "3", "http://milvus:9091/healthz"} {
		if !contains(got, w) {
			t.Errorf("probe args 应含 %q，得 %v", w, got)
		}
	}
}

// contains 字符串切片包含（测试 helper）。
func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd platform/backend && GOPATH=C:/Users/yxt/go go test -run 'TestMilvusRunArgs|TestEtcdRunArgs|TestMinioRunArgs|TestMilvusProbeArgs' ./internal/mwsupply/`
Expected: 编译失败（`milvusRunArgs` 等未定义）。

- [ ] **Step 3: 改 docker.go —— 扩接口 + 纯函数 + osDocker 实现**

3a. 扩 `MWDockerRunner` 接口（加 3 方法）：

```go
type MWDockerRunner interface {
	UsedPorts(ctx context.Context) map[int]struct{}
	RunRedisContainer(ctx context.Context, name, password string, port int) error
	RunMilvusStack(ctx context.Context, base string, port int) error    // P4：专属网络 + milvus/etcd/minio 三容器
	MilvusReady(ctx context.Context, base string, timeout time.Duration) error // P4：alpine 探针轮询 /healthz
	RmForce(ctx context.Context, name string) error
	RmMilvusStack(ctx context.Context, base string) error               // P4：rm 三容器 + 网络
}
```

3b. 在 `docker.go` 末尾加纯函数 + osDocker 实现（需 import `strings`、`time`，其中 `time` 已由 naming.go 间接——docker.go 自身需新加 `"time"` import）：

```go
import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// etcdRunArgs 构造 etcd 容器 docker run 参数（纯函数，可单测）。
// 在 base-net 网络上别名 etcd，供 milvus 经 ETCD_ENDPOINTS=etcd:2379 解析。
func etcdRunArgs(base string) []string {
	return []string{
		"run", "-d", "--name", milvusEtcdName(base),
		"--network", milvusNetName(base), "--network-alias", "etcd",
		"--restart", "unless-stopped",
		etcdImage,
		"etcd", "-advertise-client-urls=http://etcd:2379",
		"-listen-client-urls", "http://0.0.0.0:2379",
		"--data-dir", "/etcd",
	}
}

// minioRunArgs 构造 minio 容器 docker run 参数（纯函数）。
// 在 base-net 上别名 minio，固定 access/secret=minioadmin（v1 无鉴权要求，内部网络）。
func minioRunArgs(base string) []string {
	return []string{
		"run", "-d", "--name", milvusMinioName(base),
		"--network", milvusNetName(base), "--network-alias", "minio",
		"--restart", "unless-stopped",
		"-e", "MINIO_ACCESS_KEY=minioadmin",
		"-e", "MINIO_SECRET_KEY=minioadmin",
		minioImage, "minio", "server", "/minio_data",
	}
}

// milvusRunArgs 构造 milvus 容器 docker run 参数（纯函数）。
// 经 ETCD_ENDPOINTS/MINIO_ADDRESS 解析 sidecar；仅 publish gRPC 到宿主 port。
func milvusRunArgs(base string, port int) []string {
	return []string{
		"run", "-d", "--name", base,
		"--network", milvusNetName(base), "--network-alias", "milvus",
		"--restart", "unless-stopped",
		"-e", fmt.Sprintf("ETCD_ENDPOINTS=etcd:%d", etcdInternalPort),
		"-e", fmt.Sprintf("MINIO_ADDRESS=minio:%d", minioInternalPort),
		"-p", fmt.Sprintf("%d:%d", port, milvusGrpcPort),
		milvusImage, "milvus", "run", "standalone",
	}
}

// milvusProbeArgs 构造就绪探针参数：--rm 临时 alpine 在 base-net 上 wget milvus healthz。
func milvusProbeArgs(base string) []string {
	return []string{
		"run", "--rm", "--network", milvusNetName(base), readyAlpineImage,
		"wget", "-qO-", "-T", "3", fmt.Sprintf("http://milvus:%d/healthz", milvusHealthPort),
	}
}

// RunMilvusStack 起 dedicated milvus 栈：建网络 → etcd → minio → milvus。
// 任一 run 失败：best-effort rm 已起容器 + 网络，返错（由 supplyDedicated 兜底再 RmMilvusStack）。
func (osDocker) RunMilvusStack(ctx context.Context, base string, port int) error {
	if out, err := runDockerCmd(ctx, "network", "create", milvusNetName(base)); err != nil {
		return fmt.Errorf("docker network create: %w: %s", err, out)
	}
	for _, args := range [][]string{etcdRunArgs(base), minioRunArgs(base), milvusRunArgs(base, port)} {
		if out, err := runDockerCmd(ctx, args...); err != nil {
			_, _ = runDockerCmd(ctx, "rm", "-f", base, milvusEtcdName(base), milvusMinioName(base)) // best-effort 清半成品
			_, _ = runDockerCmd(ctx, "network", "rm", milvusNetName(base))
			return fmt.Errorf("docker run milvus 栈: %w: %s", err, out)
		}
	}
	return nil
}

// MilvusReady 在专属网络上轮询 milvus /healthz 直至就绪或超时。
// 经 docker socket 起临时 alpine 探针，不受 backend↔milvus 网络可达性影响。
func (osDocker) MilvusReady(ctx context.Context, base string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		out, err := runDockerCmd(ctx, milvusProbeArgs(base)...)
		if err == nil && strings.TrimSpace(out) != "" {
			return nil
		}
		lastErr = fmt.Errorf("milvus 未就绪: %v: %s", err, strings.TrimSpace(out))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("milvus 就绪超时 %v", timeout)
	}
	return lastErr
}

// RmMilvusStack 强删 milvus 栈：rm 三容器 + 删网络（best-effort）。
func (osDocker) RmMilvusStack(ctx context.Context, base string) error {
	_, _ = runDockerCmd(ctx, "rm", "-f", base, milvusEtcdName(base), milvusMinioName(base))
	_, err := runDockerCmd(ctx, "network", "rm", milvusNetName(base))
	return err
}
```

- [ ] **Step 4: 改 supply_test.go —— fakeDocker 实现 3 个新方法（满足新接口）**

4a. 给 `fakeDocker` struct 加字段（在现有 `rmCalls []string` 之后）：

```go
type fakeDocker struct {
	usedPorts   map[int]struct{}
	runCalls    []fakeDockerRun
	runErr      error
	rmCalls     []string
	stackCalls  []fakeMilvusStack // milvus：RunMilvusStack 调用
	stackErr    error
	rmStackCalls []string          // milvus：RmMilvusStack 调用（base）
	readyErr    error              // milvus：MilvusReady 返错（默认 nil=就绪）
	readyCalls  int
}

type fakeMilvusStack struct {
	base string
	port int
}
```

4b. 给 `fakeDocker` 加 3 个方法（紧跟现有 `RmForce` 之后）：

```go
func (f *fakeDocker) RunMilvusStack(_ context.Context, base string, port int) error {
	f.stackCalls = append(f.stackCalls, fakeMilvusStack{base, port})
	return f.stackErr
}
func (f *fakeDocker) MilvusReady(_ context.Context, base string, _ time.Duration) error {
	f.readyCalls++
	return f.readyErr
}
func (f *fakeDocker) RmMilvusStack(_ context.Context, base string) error {
	f.rmStackCalls = append(f.rmStackCalls, base)
	return nil
}
```

4c. `supply_test.go` 的 import 块加 `"time"`（MilvusReady 签名需要）。

- [ ] **Step 5: 跑测试确认通过**

Run: `cd platform/backend && GOPATH=C:/Users/yxt/go go test -p 1 ./internal/mwsupply/...`
Expected: PASS（docker 纯 args 用例绿；fakeDocker 满足新接口；redis 既有用例仍绿——supplyDedicated 此刻仍是 redis 专写，但接口已扩，编译通过）。

> 若 fakeDocker 缺方法会编译失败（接口未实现）——Step 4 已覆盖。

- [ ] **Step 6: commit**

```bash
git add platform/backend/internal/mwsupply/docker.go platform/backend/internal/mwsupply/docker_test.go platform/backend/internal/mwsupply/supply_test.go
git commit -m "feat(mwsupply): MWDockerRunner +RunMilvusStack/MilvusReady/RmMilvusStack(三容器栈+alpine就绪探针)

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 3: supply.go —— supplyDedicated kind 分派 + milvus 供给路径

**Files:**

- Modify: `platform/backend/internal/mwsupply/supply.go`（`supplyDedicated` 重构 + `writeDedicatedEnv` milvus 分支 + 3 个分派 helper）
- Modify: `platform/backend/internal/mwsupply/supply_test.go`（加 milvus dedicated 供给用例）

**Interfaces:**

- Consumes: Task 1 `portRange`/`dedicatedContainerName(kind,short)`；Task 2 `docker.RunMilvusStack`/`MilvusReady`/`RmMilvusStack`；既有 `store.GetBinding/GetInstance/CreateInstance`、`env.UpsertEnv`、`ready.Ping`、`docker.RunRedisContainer/RmForce/UsedPorts`。
- Produces: `supplyDedicated` 行为分派（kind→launch/ready/env）；`launchDedicated(ctx,kind,base,port) (authRef string, err error)`、`waitDedicatedReady(ctx,kind,base,port,authRef) error`、`rmDedicated(ctx,kind,base)`。Task 4 的 Cleanup 复用 `rmDedicated`（或直接在 Cleanup 内分派，见 Task 4）。

- [ ] **Step 1: 写失败测试（supply_test.go 加 milvus dedicated 供给用例）**

在 `supply_test.go` 末尾（`errStr` 定义之前）追加：

```go
// —— milvus dedicated 用例 ——

// TestReconcile_dedicatedMilvus 新供给：起栈 + 登记 + MILVUS_ADDR（无 password/db）。
func TestReconcile_dedicatedMilvus(t *testing.T) {
	r, appStore, db, _, dk := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "milded1", RepoDir: "/x", InternalPort: 8080}
	if err := appStore.Create(ctx, a); err != nil {
		t.Fatalf("create app: %v", err)
	}
	dir := writeManifest(t, "services:\n  - kind: milvus\n    strategy: dedicated\n")
	if err := r.Reconcile(ctx, a.ID, "ps_1", dir); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// 起了一次栈，端口 9700（空池最小号）
	if len(dk.stackCalls) != 1 || dk.stackCalls[0].port != milvusPortMin {
		t.Fatalf("应起 1 栈 port=%d，得 %+v", milvusPortMin, dk.stackCalls)
	}
	// env：MILVUS_ADDR=testdeploy:9700；无 MILVUS_PASSWORD；无 MILVUS_DB
	ma, _ := appStore.GetEnvValue(ctx, a.ID, "MILVUS_ADDR")
	if ma != "testdeploy:9700" {
		t.Fatalf("MILVUS_ADDR 应 testdeploy:9700，得 %q", ma)
	}
	if mp, _ := appStore.GetEnvValue(ctx, a.ID, "MILVUS_PASSWORD"); mp != "" {
		t.Fatalf("milvus v1 无 auth，不应写 MILVUS_PASSWORD，得 %q", mp)
	}
	if mdb, _ := appStore.GetEnvValue(ctx, a.ID, "MILVUS_DB"); mdb != "" {
		t.Fatalf("milvus dedicated 不应注入 MILVUS_DB，得 %q", mdb)
	}
	// binding bound + 实例行 kind=milvus / container_name=mwmilvus-<short> / port=9700
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 1 || binds[0].Status != StatusBound || binds[0].Strategy != ModeDedicated {
		t.Fatalf("binding 应 dedicated/bound，得 %+v", binds)
	}
	inst, _ := NewStore(db).GetInstance(ctx, binds[0].ServiceInstanceID)
	if inst == nil || inst.Kind != "milvus" || inst.ContainerName == "" || !strings.HasPrefix(inst.ContainerName, "mwmilvus-") || inst.Port != milvusPortMin {
		t.Fatalf("实例行应 kind=milvus + mwmilvus-<short> + port=%d，得 %+v", milvusPortMin, inst)
	}
}

// TestReconcile_dedicatedMilvus_idempotent 同 app 重部署：不重启栈、port 不变、env 仍在。
func TestReconcile_dedicatedMilvus_idempotent(t *testing.T) {
	r, appStore, _, _, dk := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "mildidem", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: milvus\n    strategy: dedicated\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	ma1, _ := appStore.GetEnvValue(ctx, a.ID, "MILVUS_ADDR")
	dk.stackCalls = nil
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir) // 重部署
	ma2, _ := appStore.GetEnvValue(ctx, a.ID, "MILVUS_ADDR")
	if ma1 != ma2 {
		t.Fatalf("重部署 MILVUS_ADDR 应不变，%q → %q", ma1, ma2)
	}
	if len(dk.stackCalls) != 0 {
		t.Fatalf("重部署复用不应再起栈，得 %d 次", len(dk.stackCalls))
	}
}

// TestReconcile_dedicatedMilvus_poolExhaust milvus 端口池满 → failed、不起栈、不写 env。
func TestReconcile_dedicatedMilvus_poolExhaust(t *testing.T) {
	r, appStore, db, _, dk := newReconcilerTest(t)
	full := map[int]struct{}{}
	for p := milvusPortMin; p <= milvusPortMax; p++ {
		full[p] = struct{}{}
	}
	dk.usedPorts = full
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "mildex", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: milvus\n    strategy: dedicated\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 1 || binds[0].Status != StatusFailed {
		t.Fatalf("池满应 failed，得 %+v", binds)
	}
	if len(dk.stackCalls) != 0 {
		t.Fatalf("池满不应起栈，得 %d", len(dk.stackCalls))
	}
	if ma, _ := appStore.GetEnvValue(ctx, a.ID, "MILVUS_ADDR"); ma != "" {
		t.Fatalf("池满不应写 MILVUS_ADDR，得 %q", ma)
	}
}

// TestReconcile_dedicatedMilvus_stackFail 起栈失败 → failed、不登记实例。
func TestReconcile_dedicatedMilvus_stackFail(t *testing.T) {
	r, appStore, db, _, dk := newReconcilerTest(t)
	dk.stackErr = errStr("docker run milvus 失败")
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "mildfail", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: milvus\n    strategy: dedicated\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 1 || binds[0].Status != StatusFailed || binds[0].ServiceInstanceID != "" {
		t.Fatalf("起栈失败应 failed + 无实例，得 %+v", binds)
	}
}

// TestReconcile_dedicatedMilvus_readyTimeout_bestEffort 就绪探针超时(best-effort) → 仍 bound、不 RmMilvusStack、env 已写。
func TestReconcile_dedicatedMilvus_readyTimeout_bestEffort(t *testing.T) {
	r, appStore, db, _, dk := newReconcilerTest(t)
	dk.readyErr = errStr("milvus 就绪超时")
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "mildready", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: milvus\n    strategy: dedicated\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 1 || binds[0].Status != StatusBound {
		t.Fatalf("就绪 best-effort 失败应仍 bound，得 %+v", binds)
	}
	if len(dk.rmStackCalls) != 0 {
		t.Fatalf("best-effort 不应 RmMilvusStack，得 %v", dk.rmStackCalls)
	}
	if ma, _ := appStore.GetEnvValue(ctx, a.ID, "MILVUS_ADDR"); ma == "" {
		t.Fatal("best-effort 应已写 MILVUS_ADDR")
	}
}
```

> `supply_test.go` import 块需加 `"strings"`（TestReconcile_dedicatedMilvus 用 `strings.HasPrefix`）。

- [ ] **Step 2: 跑测试确认失败**

Run: `cd platform/backend && GOPATH=C:/Users/yxt/go go test -run 'TestReconcile_dedicatedMilvus' ./internal/mwsupply/`
Expected: FAIL（当前 supplyDedicated 走 redis 路径：会调 RunRedisContainer 而非 RunMilvusStack，`dk.stackCalls` 为空 → 第一个断言失败）。

- [ ] **Step 3: 重构 supply.go —— supplyDedicated kind 分派 + helper + writeDedicatedEnv 分支**

3a. 把现有 `supplyDedicated`（redis 专写整段，约 supply.go:182-238）**整体替换**为下面 kind 分派版本（复用判定/端口/登记/binding 共享，launch/ready/env 分派；redis 行为逐字保留）：

```go
// supplyDedicated dedicated 供给（按 kind 分派）：复用判定（幂等）/ 新供给（端口→launch→ready→登记→env）。
// redis：1 容器 + AUTH+PING；milvus：专属网络 + milvus/etcd/minio 三容器 + /healthz 探针。
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
	lo, hi := portRange(dep.Kind)
	port := allocPort(r.docker.UsedPorts(ctx), lo, hi)
	if port == 0 {
		mkBind(StatusFailed, "", "", fmt.Sprintf("%s 端口池 %d-%d 已满", dep.Kind, lo, hi))
		return
	}
	short := genShortID()
	base := dedicatedContainerName(dep.Kind, short)
	// launch（按 kind）：redis 起 1 容器（返密码）；milvus 起三容器栈（返空 auth）。
	authRef, launchErr := r.launchDedicated(ctx, dep.Kind, base, port)
	if launchErr != nil {
		mkBind(StatusFailed, "", "", "起 "+dep.Kind+" 容器: "+launchErr.Error())
		return
	}
	// 就绪检测（best-effort，失败不阻塞）：redis AUTH+PING(5s) / milvus /healthz 探针(120s)。
	if err := r.waitDedicatedReady(ctx, dep.Kind, base, port, authRef); err != nil {
		if r.log != nil {
			r.log.Warn("dedicated 就绪检测失败 (best-effort, proceed to bound)",
				zap.String("app", appID), zap.String("kind", dep.Kind),
				zap.Int("port", port), zap.Error(err))
		}
	}
	// 登记实例行（容器单一事实源）
	inst := &ServiceInstance{
		ID:             "svinst-" + dep.Kind + "-ded-" + short,
		ProjectSpaceID: nil, // dedicated 实例不挂项目，靠 binding 关联 app
		Kind:           dep.Kind,
		Name:           base,
		SupplyMode:     ModeDedicated,
		Host:           r.host,
		Port:           port,
		AuthRef:        authRef,
		ContainerName:  base,
		Status:         "active",
	}
	if err := r.store.CreateInstance(ctx, inst); err != nil {
		r.rmDedicated(ctx, dep.Kind, base) // 登记失败回收（redis RmForce / milvus RmMilvusStack）
		mkBind(StatusFailed, "", "", "登记实例: "+err.Error())
		return
	}
	r.writeDedicatedEnv(ctx, appID, inst)
	mkBind(StatusBound, inst.ID, "", "")
}

// launchDedicated 起 dedicated 容器/栈，返回 authRef（redis=密码 / milvus=""）。
func (r *Reconciler) launchDedicated(ctx context.Context, kind, base string, port int) (string, error) {
	switch kind {
	case "redis":
		pwd := genPassword()
		return pwd, r.docker.RunRedisContainer(ctx, base, pwd, port)
	case "milvus":
		return "", r.docker.RunMilvusStack(ctx, base, port)
	default:
		return "", fmt.Errorf("dedicated 不支持 kind %q", kind)
	}
}

// waitDedicatedReady 就绪检测（best-effort 由调用方处理）：redis AUTH+PING / milvus /healthz 探针。
func (r *Reconciler) waitDedicatedReady(ctx context.Context, kind, base string, port int, authRef string) error {
	switch kind {
	case "redis":
		readyCtx, cancel := context.WithTimeout(ctx, readyPingTimeout)
		defer cancel()
		return r.ready.Ping(readyCtx, r.host, port, authRef)
	case "milvus":
		return r.docker.MilvusReady(ctx, base, milvusReadyTimeout)
	default:
		return nil
	}
}

// rmDedicated 回收半成品/失败容器栈（best-effort）：redis RmForce / milvus RmMilvusStack。
func (r *Reconciler) rmDedicated(ctx context.Context, kind, base string) {
	switch kind {
	case "redis":
		_ = r.docker.RmForce(ctx, base)
	case "milvus":
		_ = r.docker.RmMilvusStack(ctx, base)
	}
}
```

3b. 把现有 `writeDedicatedEnv`（supply.go:241-245，当前无条件写 PASSWORD）替换为加 kind 分支：

```go
// writeDedicatedEnv 写 <KIND>_ADDR（+ redis 专 _PASSWORD），均 source=platform。
// milvus v1 无 auth：只写 MILVUS_ADDR（不写 password、不写 db token）。
func (r *Reconciler) writeDedicatedEnv(ctx context.Context, appID string, inst *ServiceInstance) {
	_ = r.env.UpsertEnv(ctx, appID, EnvKeyFor(inst.Kind), ConnStr(inst), false, "platform") // REDIS_ADDR / MILVUS_ADDR
	if inst.Kind == "redis" {
		_ = r.env.UpsertEnv(ctx, appID, strings.ToUpper(inst.Kind)+"_PASSWORD", inst.AuthRef, true, "platform") // REDIS_PASSWORD
	}
}
```

- [ ] **Step 4: 跑 milvus + redis 用例确认通过（零回归）**

Run: `cd platform/backend && GOPATH=C:/Users/yxt/go go test -run 'TestReconcile_dedicatedMilvus|TestReconcile_dedicatedRedis|TestReconcile_dedicated_idempotent|TestReconcile_dedicated_poolExhaust|TestReconcile_dedicated_runFail|TestReconcile_dedicated_readyFail_bestEffort|TestReconcile_cleanup_dedicated|TestReconcile_cleanup_skipsSharedAndBindExisting' ./internal/mwsupply/`
Expected: PASS（milvus 新用例绿；redis dedicated 既有用例全绿——零回归）。

> `TestReconcile_dedicatedRedis` 仍检查 `dk.runCalls[0].port==mwPortMin`、`REDIS_ADDR=testdeploy:9600`、`REDIS_PASSWORD` 非空——重构后 redis 路径逐字等价，须仍绿。

- [ ] **Step 5: 全量回归 + commit**

Run: `cd platform/backend && GOPATH=C:/Users/yxt/go go test -p 1 ./internal/mwsupply/...`
Expected: PASS（全绿）。

```bash
git add platform/backend/internal/mwsupply/supply.go platform/backend/internal/mwsupply/supply_test.go
git commit -m "feat(mwsupply): supplyDedicated kind分派+milvus供给路径(redis零回归)

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 4: supply.go —— Cleanup kind 分派（rm 三容器 + 网络）

**Files:**

- Modify: `platform/backend/internal/mwsupply/supply.go`（`Cleanup` 的 rm 步骤按 kind 分派）
- Modify: `platform/backend/internal/mwsupply/supply_test.go`（加 milvus cleanup 用例）

**Interfaces:**

- Consumes: Task 2 `docker.RmMilvusStack`/`RmForce`；既有 `store.ListBindingsByApp/GetInstance/DeleteBinding/DeleteInstance`。
- Produces: `Cleanup` 按 `inst.Kind` 分派回收（redis `RmForce` / milvus `RmMilvusStack`），行为对 redis 逐字等价。

- [ ] **Step 1: 写失败测试（supply_test.go 加 milvus cleanup 用例）**

在 supply_test.go 末尾（errStr 之前）追加：

```go
// TestReconcile_cleanup_dedicatedMilvus 删 milvus dedicated app → RmMilvusStack(base) + 删 instance 行。
func TestReconcile_cleanup_dedicatedMilvus(t *testing.T) {
	r, appStore, db, _, dk := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "mildclean", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: milvus\n    strategy: dedicated\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	instID := binds[0].ServiceInstanceID
	inst, _ := NewStore(db).GetInstance(ctx, instID)
	base := inst.ContainerName
	dk.rmStackCalls = nil

	if err := r.Cleanup(ctx, a.ID); err != nil {
		t.Fatalf("Cleanup 不应报错: %v", err)
	}
	// docker rm 了 milvus 栈（按 base）
	if len(dk.rmStackCalls) != 1 || dk.rmStackCalls[0] != base {
		t.Fatalf("应 RmMilvusStack %q，得 %v", base, dk.rmStackCalls)
	}
	// redis 的 RmForce 不应被调（此 app 只有 milvus dedicated）
	if len(dk.rmCalls) != 0 {
		t.Fatalf("milvus cleanup 不应触发 redis RmForce，得 %v", dk.rmCalls)
	}
	// instance 行已删
	if got, _ := NewStore(db).GetInstance(ctx, instID); got != nil {
		t.Fatalf("Cleanup 后实例行应删，得 %+v", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd platform/backend && GOPATH=C:/Users/yxt/go go test -run 'TestReconcile_cleanup_dedicatedMilvus' ./internal/mwsupply/`
Expected: FAIL（当前 Cleanup 无条件走 `RmForce`，`dk.rmStackCalls` 为空、`dk.rmCalls` 非空 → 断言失败）。

- [ ] **Step 3: 改 supply.go —— Cleanup rm 按 kind 分派**

把现有 `Cleanup`（supply.go:251-276）里这段 rm 逻辑：

```go
		if inst.ContainerName != "" {
			if err := r.docker.RmForce(ctx, inst.ContainerName); err != nil && r.log != nil {
				r.log.Warn("dedicated 容器清理失败 (best-effort)",
					zap.String("app", appID), zap.String("container", inst.ContainerName), zap.Error(err))
			}
		}
```

替换为按 kind 分派：

```go
		if inst.ContainerName != "" {
			switch inst.Kind {
			case "milvus":
				if err := r.docker.RmMilvusStack(ctx, inst.ContainerName); err != nil && r.log != nil {
					r.log.Warn("dedicated milvus 栈清理失败 (best-effort)",
						zap.String("app", appID), zap.String("base", inst.ContainerName), zap.Error(err))
				}
			default: // redis
				if err := r.docker.RmForce(ctx, inst.ContainerName); err != nil && r.log != nil {
					r.log.Warn("dedicated 容器清理失败 (best-effort)",
						zap.String("app", appID), zap.String("container", inst.ContainerName), zap.Error(err))
				}
			}
		}
```

> 其余（`if b.Strategy != ModeDedicated ...` 过滤、`DeleteBinding`/`DeleteInstance`）不动；redis 行为逐字等价。

- [ ] **Step 4: 跑 cleanup 用例确认通过（含 redis 零回归）**

Run: `cd platform/backend && GOPATH=C:/Users/yxt/go go test -run 'TestReconcile_cleanup_dedicatedMilvus|TestReconcile_cleanup_dedicated|TestReconcile_cleanup_skipsSharedAndBindExisting' ./internal/mwsupply/`
Expected: PASS（milvus cleanup 绿；redis cleanup 仍绿——`TestReconcile_cleanup_dedicated` 检查 `dk.rmCalls[0]==cname`，redis 走 default 分支 `RmForce`）。

- [ ] **Step 5: 全量回归 + commit**

Run: `cd platform/backend && GOPATH=C:/Users/yxt/go go test -p 1 ./internal/mwsupply/...`
Expected: PASS（全绿）。

```bash
git add platform/backend/internal/mwsupply/supply.go platform/backend/internal/mwsupply/supply_test.go
git commit -m "feat(mwsupply): Cleanup 按 kind 分派 rm(milvus 三容器+网络,redis 零回归)

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 5: 全量回归 + .28 端到端验证

**Files:** 无代码改动（验证 + 部署）。

**Interfaces:** —

> 遵循记忆 `deploy-28-no-local-test`（开发完直接 .28 测，本机不跑功能测试）、`verify-cross-frontend-backend`（真打接口验证）、`deploy-prod-10.10.0.28`（部署链路）。.28 是测试库。

- [ ] **Step 1: 后端全量回归（串行）**

Run: `cd platform/backend && GOPATH=C:/Users/yxt/go go test -p 1 ./...`
Expected: PASS（全后端绿，含 mwsupply；确认 redis/milvus dedicated、shared、bind_existing 全链路无回归）。

> 若有与本次无关的既有失败，记录但本次只对 mwsupply/appdeploy 负回归责任。

- [ ] **Step 2: push origin main**

```bash
git push origin main
```

- [ ] **Step 3: 部署到 .28（scp + 重建 backend）**

> .28 源码在 `/opt/anp/`，从 tar 包解压。改动集中在 mwsupply 包 → 全量 tar 部署（排除 data 与 .env.prod）。

```bash
# 本机打包（排除产物/密钥/数据）
tar --force-local -czf /tmp/anp-milvus.tar.gz --exclude=node_modules --exclude=.next --exclude=.git --exclude=tmp --exclude='*.exe' --exclude=.claude --exclude=data --exclude='deploy/.env.prod' -C "D:/Projects/智源-ANP平台" .
# 上传 + 解压
scp -i ~/.ssh/miscode /tmp/anp-milvus.tar.gz root@10.10.0.28:/root/
ssh -i ~/.ssh/miscode root@10.10.0.28 "cd /opt/anp && tar -xzf /root/anp-milvus.tar.gz"
# 重建 backend（用 docker-compose v1，非 docker compose）
ssh -i ~/.ssh/miscode root@10.10.0.28 "cd /opt/anp && docker-compose -f deploy/docker-compose.prod.yml --env-file deploy/.env.prod up -d --build backend"
```

> 部署后必查三项（记忆 `deploy-prod-10.10.0.28`「push-prod.sh 假成功」教训）：源码关键字、容器 CreatedAt、迁移版本。

- [ ] **Step 4: e2e 预检（镜像 + 端口段 + 就绪探针路径）**

```bash
ssh -i ~/.ssh/miscode root@10.10.0.28 '
echo "=== 镜像缓存 ==="; docker images | grep -E "milvusdb/milvus:v2.6.15|coreos/etcd:v3.5.16|minio:v20.2.5-2024.7.4|alpine.*3.19";
echo "=== 9700-9799 占用 ==="; docker ps --format "{{.Ports}}" | grep -E "97[0-9]{2}->" || echo "9700-9799 空闲";
'
```

Expected: 四镜像均在（前三个已确认缓存，确认 alpine:3.19）；9700-9799 空闲。

> 就绪探针路径校准：起临时 milvus 栈，alpine 探针 `http://milvus:9091/healthz` 应返 200。若路径/端口不符（如非 `/healthz`），回 Task 2 Step 3 修 `milvusProbeArgs` 并重测。

- [ ] **Step 5: e2e 造最小 milvus 应用 + 部署**

造一个 python:3-alpine 应用，仓库根 `.anp/deps.yaml`：

```yaml
services:
  - kind: milvus
    strategy: dedicated
```

应用代码用 pymilvus 连 `$MILVUS_ADDR` 做 create collection → insert → search（参考 P3 e2e fixture 风格，宿主 `/opt/anp/data/milded1` = 容器 `/data/milded1`）。经平台 CREATE（带 repo_dir，不触发 adapt）→ deploy test。

- [ ] **Step 6: e2e 验证供给 + app 可用 + 隔离 + 回收 + 复用**

逐项验证（spec §14 验收）：

1. **供给**：`docker ps` 见 `mwmilvus-<short>` + `-etcd` + `-minio` 三容器（仅 milvus `0.0.0.0:<port>->19530`）；`docker network ls` 见 `<base>-net`；容器内 `MILVUS_ADDR=10.10.0.28:<port>`（无 MILVUS_PASSWORD/MILVUS_DB）；`appdeploy_service_instance` 一行 `kind=milvus, supply_mode=dedicated, container_name=mwmilvus-<short>, port∈9700-9799`；binding `strategy=dedicated, status=bound`。
2. **app 可用**：pymilvus connect(MILVUS_ADDR) → create collection → insert → search round-trip 成功。
3. **隔离**：两个 milvus dedicated app → 两套栈、两端口、两网络、独立 collection。
4. **回收**：DELETE app → 三容器 + `<base>-net` 消失 + instance/binding/env 行清。
5. **重部署复用**：同 app 重 deploy → 同 `<base>` 三容器（不新建）+ 先前 collection/数据仍在。
6. **平台保护**：手改 `MILVUS_ADDR` 返 409（source=platform）。

PG 直查（记忆 `deploy-prod-10.10.0.28`）：

```bash
ssh -i ~/.ssh/miscode root@10.10.0.28 "docker exec -e PGPASSWORD=anp_dev_pwd deploy_pg_dev psql -U anp -d anp_dev -tAc \"SELECT id,kind,supply_mode,container_name,port,status FROM appdeploy_service_instance WHERE kind='milvus' AND supply_mode='dedicated';\""
```

- [ ] **Step 7: e2e 结论回写 spec + commit**

把 e2e 实测结论（含就绪探针路径校准、Ping 实测、资源占用）追加到 spec §15「e2e 验证结论」（仿 P3 redis §15 体例），commit。

```bash
git add docs/superpowers/specs/2026-08-02-中间件依赖注入-P4-dedicated-milvus-design.md
git commit -m "docs(mwsupply): P4 milvus dedicated .28 e2e 结论

Co-Authored-By: Claude <noreply@anthropic.com>"
git push origin main
```

- [ ] **Step 8: 收尾**

- 更新记忆 `headless-runtime-closed` 旁的新闭环记忆（或新增 `milvus-dedicated-closed`）：milvus dedicated 已闭环 + 教训（如就绪探针实测路径、3 容器资源账）。
- 给「下个 session 开场白」（记忆 `handoff-prompt-for-next-session`）。

---

## Self-Review（写计划后自检）

**1. Spec 覆盖：**

- §2.1 范围（专属网络 + 三容器 + 就绪 + 登记 + MILVUS_ADDR + 回收）→ Task 2（栈）+ Task 3（供给）+ Task 4（回收）。✓
- §3 关键决策（拓扑/镜像/端口/别名/就绪 R3/无 auth/不 bind-mount/泛化）→ Global Constraints + Task 1-4。✓
- §4 数据模型（无新迁移，container_name 复用）→ Global Constraints「零迁移」。✓
- §5 供给流程 kind 分派 → Task 3（supplyDedicated + launchDedicated/waitDedicatedReady/writeDedicatedEnv）。✓
- §6 容器编排 RunMilvusStack → Task 2。✓
- §7 就绪 MilvusReady（alpine 探针 + best-effort）→ Task 2 + Task 3 waitDedicatedReady + 测试 readyTimeout_bestEffort。✓
- §8 Cleanup kind 分派 → Task 4。✓
- §9 naming 改动 → Task 1。✓
- §10 模块改动表 → File Structure + 各 Task。✓
- §11 测试计划（单测 #1-9 + e2e）→ Task 1-4 单测 + Task 5 e2e。✓
- §14 验收 9 条 → Task 5 Step 6 逐项。✓

**2. 占位扫描：** 无 TBD/TODO；每步含完整代码或确切命令。✓

**3. 类型一致性：**

- `dedicatedContainerName(kind, short)` —— Task 1 定义，Task 3 supplyDedicated 调用，签名一致。✓
- `portRange(kind) (int,int)` —— Task 1 定义，Task 3 调用（`lo, hi := portRange(dep.Kind)`）。✓
- `RunMilvusStack(ctx, base, port)` / `MilvusReady(ctx, base, timeout)` / `RmMilvusStack(ctx, base)` —— Task 2 接口定义，Task 3 launchDedicated/waitDedicatedReady/rmDedicated 调用，Task 4 Cleanup 调用，签名一致。✓
- `etcdRunArgs(base)` / `minioRunArgs(base)` / `milvusRunArgs(base, port)` / `milvusProbeArgs(base)` —— Task 2 定义 + 测试，一致。✓
- `fakeDocker` 新字段 `stackCalls/stackErr/rmStackCalls/readyErr/readyCalls` —— Task 2 加，Task 3/4 测试用，一致。✓
- 常量名（`milvusPortMin` 等）—— Task 1 定义，Task 2/3/测试用，逐字一致。✓

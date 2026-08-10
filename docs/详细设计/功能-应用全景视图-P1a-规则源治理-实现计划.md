# 应用全景视图 P1-a（规则源治理）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 ANP 的规则知识源（`AGENTS.md` 固定段）与部署引擎实现保持一致——补全 4 处脱节事实（端口段 / PORT 注入 / CONFIG_PATH 注入 / needs 消费现状），用共享常量从根上防漂移，并在部署链路补一次 `RefreshAgentsMD` 为 P2 备料铺垫。

**Architecture:** 端口段常量的**单一源**放在底层 `standard` 包（导出 `PortTestMin/Max` 等）；`standard.BuildAgentsMarkdown` 据常量渲染 AGENTS.md 固定段；`appdeploy/deployer.go` 的端口常量改为 `standard` 的编译期别名（`portTestMin = standard.PortTestMin`）——改一处，AGENTS.md 文本与引擎端口分配同步。依赖方向 `appdeploy → standard` 是单向的（已核实 `standard` 不反向 import `appdeploy`），故常量源放 `standard` 零循环风险。`Deploy` handler 在 `go buildAndDeploy` 前补一次 best-effort `RefreshAgentsMD`（镜像 workspace/导入 3 处既有调用点）。

**Tech Stack:** Go 1.x（module `zhiyuan-anp/platform/backend`），`testing`，PG（仅 `standard`/`appdeploy` 既有测试用，本计划不改表）。

## Scope Note

本计划**只覆盖 P1-a（规则源治理，地基）**。全景视图规范的 P1-b（引擎消费 needs 全字段）、P1-c（全景聚合 + 前端）各自独立、可分别测试提交，将在 P1-a 落地后另起实现计划。这样每个 plan 都产出可独立工作、可测试的软件（遵循 writing-plans 的 scope 准则）。

## Global Constraints

- **P1 全程不改表结构**（零迁移风险）——本计划纯改 Go 代码 + 测试，不碰 `internal/db/migrations`。
- **conventional commits**：type(scope): subject，中文 body 可，**body 每行 ≤ 100 字符**，提交带 `Co-Authored-By: Claude <noreply@anthropic.com>` trailer。
- **分支工作流**：在 `feat/app-panorama-p1a` 分支开发，逐 task 提交，P1-a 完成后合 main（main 保持稳定可部署，不直接推 main）。
- **后端测试**：`cd platform/backend` 后跑 `go test -p 1 -count=1 ./internal/standard/... ./internal/appdeploy/...`（需 PG service 容器 + pgvector；`-p 1` 串行避免 mwsupply 等测试隔离冲突）。
- **以事实为根据**：所有 file:line 与代码片段均已核实（见各 Task「事实基线」），勿臆测。
- **module 路径**：`zhiyuan-anp/platform/backend`（见 `handler.go:32` 既有 import）。

---

## File Structure

| 文件                                  | 责任                                                             | 本计划改动                                 |
| ------------------------------------- | ---------------------------------------------------------------- | ------------------------------------------ |
| `internal/standard/model.go`          | `Standard` 结构 + `BuildAgentsMarkdown` 渲染（含部署适配固定段） | 加导出端口常量；重写固定段补 4 事实        |
| `internal/standard/model_test.go`     | `BuildAgentsMarkdown` / `BuildPromptSection` 单测                | 加 `TestBuildAgentsMarkdown_DeployFacts`   |
| `internal/appdeploy/deployer.go`      | `Deployer` + 端口分配常量 + `ensurePortEnv`                      | 端口常量改 `standard` 别名 + 加 import     |
| `internal/appdeploy/handler.go`       | `Deploy`/`buildAndDeploy` 等 HTTP handler                        | `Deploy` 补 `RefreshAgentsMD`              |
| `internal/appdeploy/deployer_test.go` | `Deployer` 单测（端口段/挂载/slug 等）                           | 不改（既有 `TestPortRangeConstants` 覆盖） |

不改：`internal/standard/store.go`（`RefreshAgentsMD` 已存在，签名不变）、`internal/db/migrations/*`、前端。

---

## 事实基线（已核实，写代码的依据）

1. **依赖方向**：`appdeploy/handler.go:32` import `internal/standard`；`internal/standard` 下无任何文件 import `internal/appdeploy`（已 grep 确认）。→ 端口常量单一源放 `standard` 不产生循环。
2. **固定段位置**：`standard/model.go:97-108` 是「ANP 部署适配规范」固定段（`BuildAgentsMarkdown` 末尾，11 个 `b.WriteString` + `return`）。
3. **端口常量现状**：`appdeploy/deployer.go:16-22` 定义 `portTestMin=9100`/`portTestMax=9199`/`portProdMin=9200`/`portProdMax=9300`（包私有，小写）。
4. **既有测试不冲突**：
   - `standard/model_test.go:29` `TestBuildAgentsMarkdown_DeploySpecSection` 断言含 `.anp/deploy.yaml`/`needs`/`actual`/`mounts`/`config.yaml`——重写后这些 token 仍保留，不破坏。
   - `standard/store_test.go:257` `TestRefreshAgentsMD_WritesAggregateMarkdown` 只断言 scoped 规范名（`### G-plat` 等），不碰固定段 token。
   - `appdeploy/deployer_test.go:132` `TestPortRangeConstants` 断言 `portTestMin==9100` 等——改别名后值不变，继续过。
5. **`RefreshAgentsMD` 签名**：`func (s *Store) RefreshAgentsMD(ctx context.Context, repoDir, psID, module string) error`（`standard/store.go:223`）。既有 3 处调用点（`handler.go:326`/`1475`/`1593`）均为 best-effort `if h.standards != nil && repoDir != "" { _ = h.standards.RefreshAgentsMD(ctx, repoDir, psID, "") }`，**均无单测**（DB 编排接线，惯例不强测）。
6. **`Deploy` handler**：`handler.go:1621`，`a.RepoDir` 可用（`a := h.store.Get(...)` :1623）；`buildDir`（工作台部署传 worktree 路径，否则空串）在 :1665-1689 算出；:1690 `h.markBuilding`；:1691 `go h.buildAndDeploy(...)`。插入点在 :1689 与 :1690 之间。

---

### Task 1: standard 共享端口常量 + 补全 AGENTS.md 四事实

**Files:**

- Modify: `platform/backend/internal/standard/model.go`（加端口常量 + 重写 :97-108 固定段）
- Test: `platform/backend/internal/standard/model_test.go`（加 `TestBuildAgentsMarkdown_DeployFacts`）

**Interfaces:**

- Consumes: 无（地基任务）
- Produces: 导出常量 `standard.PortTestMin` / `standard.PortTestMax` / `standard.PortProdMin` / `standard.PortProdMax`（Task 2 的 `deployer.go` 别名引用它们）；`BuildAgentsMarkdown` 输出新增 4 事实文本（Task 3 的 `RefreshAgentsMD` 会把它写进应用 AGENTS.md）。

- [ ] **Step 1: 写失败测试**

在 `internal/standard/model_test.go` 末尾追加。注意：文件顶部 import 块当前只有 `strings`、`testing`，需补 `"fmt"`。

先改 import（`model_test.go:3-6`）：

```go
import (
	"fmt"
	"strings"
	"testing"
)
```

再在文件末尾（第 36 行 `}` 之后）追加测试：

```go
// TestBuildAgentsMarkdown_DeployFacts 断言「ANP 部署适配规范」固定段含与引擎实现一致的事实：
//   - 自动注入 PORT / CONFIG_PATH=/app/config.yaml
//   - 端口段（从 PortTestMin/Max · PortProdMin/Max 常量渲染，改常量→文本随之变→此处即漂移告警）
//   - needs 消费现状真相（仅 mounts 生效）
// 防规则源（AGENTS.md）与引擎实现脱节。
func TestBuildAgentsMarkdown_DeployFacts(t *testing.T) {
	got := BuildAgentsMarkdown(nil, "")
	for _, want := range []string{
		"`PORT`",                            // 自动注入 PORT（带反引号，定位到 bullet 标题）
		"`CONFIG_PATH=/app/config.yaml`",    // 自动注入 CONFIG_PATH
		fmt.Sprintf("%d-%d", PortTestMin, PortTestMax), // test 端口段（常量渲染）
		fmt.Sprintf("%d-%d", PortProdMin, PortProdMax), // prod 端口段（常量渲染）
		"仅消费",                              // needs 消费现状真相
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("BuildAgentsMarkdown 固定段应含 %q\n输出:\n%s", want, got)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd platform/backend && go test -run TestBuildAgentsMarkdown_DeployFacts ./internal/standard/`
Expected: 编译失败——`undefined: PortTestMin`（常量与渲染尚未加）。

- [ ] **Step 3: 加端口常量**

`internal/standard/model.go`，在模块常量块（:24-30）之后插入导出端口常量。把：

```go
// module 子字段取值常量（scope=module 时）。
const (
	ModuleAPI  = "api"
	ModuleForm = "form"
	ModuleDB   = "db"
	ModuleCode = "code"
	ModuleUI   = "ui"
)
```

替换为：

```go
// module 子字段取值常量（scope=module 时）。
const (
	ModuleAPI  = "api"
	ModuleForm = "form"
	ModuleDB   = "db"
	ModuleCode = "code"
	ModuleUI   = "ui"
)

// ANP 部署端口段（宿主端口分配区间）—— 单一源。
// BuildAgentsMarkdown 固定段据此渲染；appdeploy/deployer.go 的端口常量别名引用同一组值，
// 改一处 → AGENTS.md 文本与引擎端口分配同步（防规则源与实现脱节）。
// 依赖方向 appdeploy→standard 单向，常量源放本包零循环风险。
const (
	PortTestMin = 9100 // test 环境宿主端口下限
	PortTestMax = 9199 // test 环境宿主端口上限
	PortProdMin = 9200 // prod 环境宿主端口下限
	PortProdMax = 9300 // prod 环境宿主端口上限
)
```

- [ ] **Step 4: 重写固定段补四事实**

`internal/standard/model.go`，把 :97-108 的固定段（从 `// ANP 部署适配规范` 注释到 `return b.String()`）整体替换。旧内容：

```go
	// ANP 部署适配规范（固定段，导入/部署适配用；opencode 据此把应用适配成可部署）。
	b.WriteString("## ANP 部署适配规范（导入/部署适配用）\n\n")
	b.WriteString("导入或部署应用到 ANP 时按本规范适配代码（ANP 注入连接信息、容器化运行）：\n\n")
	b.WriteString("- **配置优先读环境变量（env-over-config）**：应用配置须优先读环境变量；ANP 注入 `DATABASE_URL`(PG)、可注入 `REDIS_ADDR`/`MILVUS_ADDR`/`PG_*` 等。**禁止硬编码 `127.0.0.1`/`localhost` 访问中间件**（容器内不可达）。\n")
	b.WriteString("- **构建**：仓库根须有 Dockerfile（推荐多阶段）；`EXPOSE` 应用监听端口；构建上下文 = 仓库根。\n")
	b.WriteString("- **依赖**：中间件由 ANP 供给或绑定已有，连接信息经环境变量注入；应用读 env，不写死地址。\n")
	b.WriteString("- **依赖声明（回写 `.anp/deps.yaml`）**：若应用用到 redis/milvus 等中间件，在仓库根写 `.anp/deps.yaml` 声明依赖，ANP 据此注入连接 env（`REDIS_ADDR`/`MILVUS_ADDR`）。格式：`services: [{kind: redis}, {kind: milvus}]`（kind 必填；strategy 可选，不写走默认 `bind_existing`）。无中间件依赖则不写此文件。\n")
	b.WriteString("- **部署需求回写（`.anp/deploy.yaml`）**：声明应用的部署需求，ANP 据此确定性重放部署（每次升级保持原先部署方式，不因引擎变更而漂移）。文件分两段：`needs`（**你维护**：`mounts`/`env_keys`/`ports`/`command`）+ `actual`（**引擎成功后自动回填，你只读别改**：`image_digest`/`mounts_src`/`host_port`/`engine_version`）。`mounts` 用于密钥/配置文件挂载（`src` 仓库相对路径 → `dst` 容器内路径，不进镜像层）。**仓库根有 `config.yaml` 的应用务必声明** `mounts: [{src: config.yaml, dst: /app/config.yaml, readonly: true}]`，否则挂载缺失/错位。无特殊部署需求（普通 web 无 config）可不写此文件，引擎自动探测。\n")
	b.WriteString("- **网络**：默认 bridge（隔离）；需 host 网络须审批，优先改配置走 env。\n")
	b.WriteString("- **形态**：web（HTTP，有端口+URL）/ headless（bot/worker 等长驻外发，无 URL，健康=进程或外连）。\n")
	b.WriteString("- **缺失服务**：若部署机缺某依赖，在变更里报明（kind/原因），由 ANP 经审批后受控安装（白名单内），连接回填注入 env。\n\n")
	return b.String()
```

新内容（补：端口段从常量渲染、运行时注入 PORT/CONFIG_PATH、needs 消费现状）：

```go
	// ANP 部署适配规范（固定段，导入/部署适配用；opencode 据此把应用适配成可部署）。
	// 与引擎实现保持一致：端口段取自本包 PortTestMin/Max·PortProdMin/Max 常量；注入 env/消费现状
	// 据引擎实际（deployer.go ensurePortEnv、handler.go CONFIG_PATH 注入、deploy_manifest.go 消费子集）。
	b.WriteString("## ANP 部署适配规范（导入/部署适配用）\n\n")
	b.WriteString("导入或部署应用到 ANP 时按本规范适配代码（ANP 注入连接信息、容器化运行）：\n\n")
	b.WriteString("- **配置优先读环境变量（env-over-config）**：应用配置须优先读环境变量。**禁止硬编码 `127.0.0.1`/`localhost` 访问中间件**（容器内不可达）。\n")
	b.WriteString("- **运行时自动注入的 env（应用须读取，勿硬编码）**：\n")
	b.WriteString("  - `PORT`：容器监听端口（= 宿主映射的容器端口）。PORT-driven 应用（node `process.env.PORT` / python `int(os.getenv(\"PORT\"))`）据此监听；应用显式设了 `PORT` 则尊重不覆盖。\n")
	b.WriteString("  - `CONFIG_PATH=/app/config.yaml`：配置文件在容器内的挂载路径，应用据此加载配置（无 config 则忽略）。\n")
	b.WriteString("  - 中间件连接：`DATABASE_URL`(PG)、可注入 `REDIS_ADDR`/`MILVUS_ADDR`/`PG_*` 等，由 ANP 供给注入。\n")
	fmt.Fprintf(&b, "- **端口段**：宿主端口分配 test %d-%d / prod %d-%d（ANP 自动按环境从区间内分配首个空闲端口；容器内监听端口由 `PORT` 指定，`-p` 映射宿主:容器）。\n", PortTestMin, PortTestMax, PortProdMin, PortProdMax)
	b.WriteString("- **构建**：仓库根须有 Dockerfile（推荐多阶段）；`EXPOSE` 应用监听端口；构建上下文 = 仓库根。\n")
	b.WriteString("- **依赖**：中间件由 ANP 供给或绑定已有，连接信息经环境变量注入；应用读 env，不写死地址。\n")
	b.WriteString("- **依赖声明（回写 `.anp/deps.yaml`）**：若应用用到 redis/milvus 等中间件，在仓库根写 `.anp/deps.yaml` 声明依赖，ANP 据此注入连接 env（`REDIS_ADDR`/`MILVUS_ADDR`）。格式：`services: [{kind: redis}, {kind: milvus}]`（kind 必填；strategy 可选，不写走默认 `bind_existing`）。无中间件依赖则不写此文件。\n")
	b.WriteString("- **部署需求回写（`.anp/deploy.yaml`）**：声明应用的部署需求，ANP 据此确定性重放部署（每次升级保持原先部署方式，不因引擎变更而漂移）。文件分两段：`needs`（**你维护**：`mounts`/`env_keys`/`ports`/`command`）+ `actual`（**引擎成功后自动回填，你只读别改**：`image_digest`/`mounts_src`/`host_port`/`engine_version`）。`mounts` 用于密钥/配置文件挂载（`src` 仓库相对路径 → `dst` 容器内路径，不进镜像层）。**仓库根有 `config.yaml` 的应用务必声明** `mounts: [{src: config.yaml, dst: /app/config.yaml, readonly: true}]`，否则挂载缺失/错位。无特殊部署需求（普通 web 无 config）可不写此文件，引擎自动探测。\n")
	b.WriteString("  - **引擎消费现状（务必知晓，避免误判）**：当前引擎**仅消费 `needs.mounts`**（且 `dst=/app/config.yaml` 的挂载）；`needs.ports`/`command`/`env_keys` 为**预留字段**，待 P1-b 接入后才生效。备料时仍可填全字段（前瞻），但勿误以为现在就全部驱动部署。\n")
	b.WriteString("- **网络**：默认 bridge（隔离）；需 host 网络须审批，优先改配置走 env。\n")
	b.WriteString("- **形态**：web（HTTP，有端口+URL）/ headless（bot/worker 等长驻外发，无 URL，健康=进程或外连）。\n")
	b.WriteString("- **缺失服务**：若部署机缺某依赖，在变更里报明（kind/原因），由 ANP 经审批后受控安装（白名单内），连接回填注入 env。\n\n")
	return b.String()
```

注：`fmt` 已在 `model.go:11` import，无需加。

- [ ] **Step 5: 运行测试确认通过**

Run: `cd platform/backend && go test -run 'TestBuildAgentsMarkdown' ./internal/standard/`
Expected: PASS（新 `TestBuildAgentsMarkdown_DeployFacts` 过 + 既有 `TestBuildAgentsMarkdown_DeploySpecSection` 仍过——token 未破坏）。

- [ ] **Step 6: 提交**

```bash
cd platform/backend
git add internal/standard/model.go internal/standard/model_test.go
git commit -m "feat(standard): 补全 AGENTS.md 部署适配固定段四事实 + 端口常量单一源

- 加导出常量 PortTestMin/Max·PortProdMin/Max（端口段单一源，防漂移）
- 固定段补四事实：运行时注入 PORT/CONFIG_PATH、端口段（常量渲染）、
  needs 消费现状（仅 mounts 生效，余预留待 P1-b）
- 加 TestBuildAgentsMarkdown_DeployFacts 断言

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: deployer.go 端口常量改 standard 别名

**Files:**

- Modify: `platform/backend/internal/appdeploy/deployer.go`（:3-14 import 块加 standard；:16-22 const 块改别名）

**Interfaces:**

- Consumes: `standard.PortTestMin` / `PortTestMax` / `PortProdMin` / `PortProdMax`（Task 1 产出）。
- Produces: `deployer.go` 的 `portTestMin` 等仍为包私有常量（值不变），下游 `envPortRange`/`Deploy` 零改动。

**为什么没有新单测：** 防漂移的保证是**编译期别名表达式本身**（`portTestMin = standard.PortTestMin`）——一旦这样写，`standard` 改值则 `appdeploy` 编译期跟随，结构性不可漂移。既有 `deployer_test.go:132 TestPortRangeConstants`（断言 `portTestMin==9100` 等）与 `:103 TestEnvPortRange` 已覆盖「值正确」——改别名后值不变，二者继续过。再加一个「别名相等」的运行时断言是同义反复（值等就过，无法区分别名 vs 字面量），属冗余测试，按良好测试设计原则不加。

- [ ] **Step 1: 改 const 块为 standard 别名**

`internal/appdeploy/deployer.go:16-22`。把：

```go
// 各环境宿主端口分配区间（互不冲突；避开 .28 上 lowcode/帆软/ANP 已用端口）。
const (
	portTestMin = 9100
	portTestMax = 9199
	portProdMin = 9200
	portProdMax = 9300
)
```

替换为：

```go
// 各环境宿主端口分配区间（互不冲突；避开 .28 上 lowcode/帆软/ANP 已用端口）。
// 单一源在 standard 包（PortTestMin 等）；此处编译期别名引用——改 standard 一处，
// AGENTS.md 渲染与本引擎端口分配同步，防规则源与实现脱节。
const (
	portTestMin = standard.PortTestMin
	portTestMax = standard.PortTestMax
	portProdMin = standard.PortProdMin
	portProdMax = standard.PortProdMax
)
```

- [ ] **Step 2: 加 standard import**

`internal/appdeploy/deployer.go:3-14`。把：

```go
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)
```

替换为（goimports 规范：标准库一组，项目 import 一组）：

```go
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"zhiyuan-anp/platform/backend/internal/standard"
)
```

- [ ] **Step 3: 构建验证（别名表达式编译期解析）**

Run: `cd platform/backend && go build ./internal/appdeploy/`
Expected: 成功（无 undefined / 循环 import）。若报 `import cycle`，说明改错了方向——核对 `standard` 不可 import `appdeploy`。

- [ ] **Step 4: 跑既有端口测试确认值不变**

Run: `cd platform/backend && go test -run 'TestPortRangeConstants|TestEnvPortRange|TestAllocFreePort' ./internal/appdeploy/`
Expected: PASS（值 9100-9199/9200-9300 未变）。

- [ ] **Step 5: 提交**

```bash
cd platform/backend
git add internal/appdeploy/deployer.go
git commit -m "refactor(appdeploy): 端口常量改 standard 别名（防规则源与实现脱节）

deployer.go 端口段常量从字面量改为 standard.PortTestMin 等编译期别名。
单一源在 standard（AGENTS.md 据此渲染），改一处两边同步。
值不变，既有 TestPortRangeConstants/TestEnvPortRange 继续过。

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: Deploy handler 补 RefreshAgentsMD

**Files:**

- Modify: `platform/backend/internal/appdeploy/handler.go`（`Deploy` :1689-1691 之间插入）

**Interfaces:**

- Consumes: `h.standards`（`*standard.Store`，Handler 既有字段，:325/1474 已用）；`standard.Store.RefreshAgentsMD(ctx, repoDir, psID, module)`。
- Produces: 每次 `POST .../deploy` 部署前刷新目标 repo 的 `AGENTS.md`（P2 opencode 备料的前置；也让导入/工作台/部署四条链路刷新一致）。

**为什么没有新单测：** 此改动是 best-effort 编排接线（`_ = h.standards.RefreshAgentsMD(...)`），镜像既有 3 处调用点（`handler.go:326/1475/1593`），它们也都无单测——`RefreshAgentsMD` 依赖 DB-backed `*standard.Store`，单测需注入 store + gin context mock，成本高、收益低，且与 codebase 既有处理一致。验证靠 `go build`+`go vet` + .28 e2e。

- [ ] **Step 1: 在 Deploy 插入 RefreshAgentsMD**

`internal/appdeploy/handler.go`，定位 `Deploy` 末尾（:1689-1691）：

```go
		buildDir = wt
		}
	}
	h.markBuilding(c.Request.Context(), psID, aid, env) // 同步标 building，前端立即看到进度条
	go h.buildAndDeploy(psID, aid, "", env, in.NodeID, buildDir)
```

替换为（在 `}` 与 `h.markBuilding` 之间插入刷新块）：

```go
		buildDir = wt
		}
	}
	// 部署前刷新 AGENTS.md：让 opencode（P2 部署前备料）/ 导入适配读到与引擎实现一致的最新 ANP 规则。
	// best-effort：失败不阻断部署（与 workspace/导入 三处调用点一致）。
	// 工作台部署优先刷 worktree(buildDir)，普通部署刷主仓 a.RepoDir。
	repoDir := buildDir
	if repoDir == "" {
		repoDir = a.RepoDir
	}
	if h.standards != nil && repoDir != "" {
		_ = h.standards.RefreshAgentsMD(c.Request.Context(), repoDir, psID, "")
	}
	h.markBuilding(c.Request.Context(), psID, aid, env) // 同步标 building，前端立即看到进度条
	go h.buildAndDeploy(psID, aid, "", env, in.NodeID, buildDir)
```

（`old_string` 用 :1687-1691 的 `buildDir = wt` 到 `go h.buildAndDeploy` 整段，确保唯一匹配。）

- [ ] **Step 2: 构建验证**

Run: `cd platform/backend && go build ./internal/appdeploy/ && go vet ./internal/appdeploy/`
Expected: 成功，无 vet 告警。

- [ ] **Step 3: 提交**

```bash
cd platform/backend
git add internal/appdeploy/handler.go
git commit -m "feat(appdeploy): Deploy 前刷新 AGENTS.md（为 P2 备料铺垫）

POST .../deploy 在 go buildAndDeploy 前 best-effort 调 RefreshAgentsMD，
让部署链路与 workspace/导入一致地刷新应用 AGENTS.md。
工作台部署刷 worktree，普通部署刷主仓。失败不阻断。

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 4: 全量验证 + 合 main + 部署 .28

**Files:** 无新改（本 task 是验证 + 集成 + 部署）。

- [ ] **Step 1: 全量后端测试（standard + appdeploy）**

Run: `cd platform/backend && go test -p 1 -count=1 ./internal/standard/... ./internal/appdeploy/...`
Expected: 全 PASS（需 PG service 容器 + pgvector；`-p 1` 串行避测试隔离冲突）。本计划不改表，无迁移。

- [ ] **Step 2: 全包构建冒烟**

Run: `cd platform/backend && go build ./...`
Expected: 成功。

- [ ] **Step 3: 合 main**

```bash
cd platform/backend
git checkout main
git pull --ff-only
git merge --no-ff feat/app-panorama-p1a -m "Merge feat/app-panorama-p1a: 规则源治理（AGENTS.md 四事实+端口常量单一源+Deploy 刷新)

P1-a 地基：补全 AGENTS.md 部署适配固定段四脱节事实（端口段/PORT/CONFIG_PATH/needs 消费现状），
端口常量统一到 standard 单一源防漂移，Deploy 链路补 RefreshAgentsMD。
全程不改表。详见 docs/详细设计/功能-应用全景视图-P1a-规则源治理-实现计划.md

Co-Authored-By: Claude <noreply@anthropic.com>"
```

origin 推送**待用户确认**（按既有约定，推送外部动作前确认）。

- [ ] **Step 4: 部署 backend 到 .28**

仅 backend（P1-a 无前端、无迁移）。.28 共享机**只动 `deploy_` 前缀容器**，用 docker-compose v1。

```bash
# 增量 scp 改动的后端文件（4 个）到 .28:/opt/anp/platform/backend/...
# （scp 选项变量不可含 host；tar/scp 排除 data/ 与 deploy/.env.prod）
# 在 .28 上重建 backend：
ssh -o PubkeyAcceptedAlgorithms=+ssh-rsa -o StrictHostKeyChecking=no -i ~/.ssh/miscode root@10.10.0.28 \
  'cd /opt/anp && docker-compose -f deploy/docker-compose.yml up -d --build deploy_backend'
```

确认 `deploy_backend_1` 正常运行（`docker ps | grep deploy_backend`），旧容器无孤儿（scp 增量不删残留，必要时 `ssh ... rm` 清理）。

- [ ] **Step 5: .28 e2e 抽查（AGENTS.md 含新事实）**

部署后触发任一应用刷新 AGENTS.md（打开某应用编码工作台 / 触发一次部署），再查该应用 repo 的 AGENTS.md：

```bash
ssh -o PubkeyAcceptedAlgorithms=+ssh-rsa -o StrictHostKeyChecking=no -i ~/.ssh/miscode root@10.10.0.28 \
  'grep -E "9100-9199|CONFIG_PATH=/app/config.yaml|仅消费" /opt/anp/data/repos/*/AGENTS.md | head'
```

Expected: 命中端口段 / CONFIG_PATH / needs 消费现状三条新事实 = 规则源已与引擎实现一致。标记为**待用户浏览器/SSH e2e 确认**（与平台其他功能惯例一致）。

---

## Self-Review（spec 覆盖 / 占位符 / 类型一致性）

**1. Spec 覆盖**（对照 `功能-应用全景视图与部署中枢.md` §4.1 P1-a 三点）：

- 「补全 AGENTS.md 固定段四条事实」→ Task 1 Step 4（端口段/PORT/CONFIG_PATH/needs 消费现状）✅
- 「常量共享防漂移」→ Task 1 Step 3（源在 standard）+ Task 2（deployer 别名）✅
- 「部署链路补 RefreshAgentsMD」→ Task 3 ✅
- 「不做：固定段挪 DB」→ 未做 ✅

**2. 占位符扫描**：无 TBD/TODO；每步含完整代码；commit message 完整；测试代码完整。✅

**3. 类型/命名一致性**：

- 常量名全流程一致：`PortTestMin/PortTestMax/PortProdMin/PortProdMax`（standard 定义 → deployer 别名 → test 断言）。✅
- `RefreshAgentsMD` 签名 `(ctx, repoDir, psID, module)` 与既有 3 处调用一致。✅
- `h.standards` 字段名与 :325/1474 一致。✅
- `EnvTest`/`EnvProd`、`a.RepoDir`、`buildDir` 均为既有符号（已核实）。✅

无遗留问题。

---

## Execution Handoff

计划已保存到 `docs/详细设计/功能-应用全景视图-P1a-规则源治理-实现计划.md`。两种执行方式：

1. **Subagent-Driven（推荐）**——逐 task 派新 subagent，两阶段评审，迭代快。
2. **Inline 执行**——本会话内按 executing-plans 批量执行，带检查点。

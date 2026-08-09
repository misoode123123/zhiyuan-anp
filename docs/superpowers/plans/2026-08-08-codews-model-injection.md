# codews 模型注入（per-user 授权模型进入编码工作台）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 编码工作台（opencode 交互式 + `/code` 手动派发）启动时，按「当前用户授权的模型」配置/注入工具，越权即拒——完成模型中心三需求的最后一环（用户在编码界面用自己被授权的模型）。

**Architecture:** 用户在编码工作台 `ModelSelect(taskType="code")` 选模型（值 = `compute_model.id` = `cmd_xxx`）→ 随 `/workspace` POST `model` 字段传入后端 → appdeploy `Workspace` handler 用 `CtxUserDBID`(usr_xxx) 做 `IsGranted` 校验（越权 403，不 fallback）→ `codews.Manager.Ensure(model)` 为该 (app,user) 生成只含该授权模型的 `opencode.json`，写入 per-user `XDG_CONFIG_HOME` 目录，opencode 进程 env 注入 `XDG_CONFIG_HOME=<dir>`。`/code` headless 路径把 `cmd_xxx` 解析成 opencode `provider/name` 传给 `opencode run -m`。claude 工具 `ANTHROPIC_MODEL` 改取授权模型名。

**Tech Stack:** Go（gin, codews/appdeploy/compute/dev 包）+ Next.js 16（原生 HTML + Tailwind v4，复用 `ModelSelect`）。

## 关键实测结论（已验证，勿再质疑）

- **opencode v1.18.9 认 `XDG_CONFIG_HOME`**：`opencode debug paths` 的 config 路径随 `XDG_CONFIG_HOME` 变；标记 provider 在 XDG config 里被 `opencode debug config` 读到。`.28 deploy_backend_1` 容器内实测通过（2026-08-08）。
- **`opencode serve` 无 `--config`/`--model` flag**（只有 --port/--hostname/--mdns/--cors/--pure）。
- **session data 是共享 1GB `opencode.db`**（`/root/.local/share/opencode/`）→ **HOME 整体覆盖出局**（会每用户复制 1GB）。**只用 `XDG_CONFIG_HOME`**（只挪 config，不碰 data DB，不污染 repo worktree）。
- **provider key = `slugify(provider.Name)`，model key = `model.Name`**（`opencode_gen.go:58,94`）→ `ResolveOpencodeModelID = slugify(provider.Name)+"/"+model.Name`，还原 `"zai-coding/glm-5.1"`。
- `/code`（dev/handler.go:60-94）**已有 `Model` 字段 + `IsGranted` 校验**（用 `CtxUserDBID`）。
- `/workspace`（appdeploy/handler.go:239-270）**无 model 字段、无 IsGranted、未接 computeStore**，且用 `CtxUserID`(用户名) 而非 `CtxUserDBID`(usr_xxx)。
- `codews` 包**零 compute 导入**；`IsGranted` 在 `compute/grant.go:64-70`（method on `*compute.Store`）；duck-typed 接口范本 `dev/handler.go:15-17`。
- `codews.Manager.Ensure` 唯一调用点 `appdeploy/handler.go:259`；`toolEnv` 唯一调用点 `manager.go:151`。
- `Ensure` 的 userID（= `CtxUserID` 用户名）用于 worktree 分支名 `dev-<userID>` 与 session key `appID:userID`——**保持用户名不动**（避免孤立现存 worktree），grant 校验单独用 `CtxUserDBID`。

## Global Constraints（遵循 `docs/详细设计/开发标准与规范.md`）

- **凭证明文维持**：`compute_provider.api_key` 明文，生成的 per-user `opencode.json` 直接含 `apiKey`（与现全局 config 一致）。
- **模型标识符**：授权/`IsGranted` 用 `cmd_xxx`（`compute_model.id`）；opencode config 用 `model.Name`+`slugify(provider.Name)`；`opencode run -m` 用 `"provider/name"`。
- **越权即拒，不 fallback**：`/workspace` 选了未授权模型 → 403，不回退路由默认。
- **空授权兜底**：用户未被授权任何模型 → `/workspace` 不传 model → Manager 不生成 per-user config → 用全局 config（不阻断，渐进迁移）。
- **Go**：`golangci-lint`+`gofmt`；`context.Context` 贯穿；显式 `error`；codews 不直接 import compute（用窄接口解耦）。
- **测试**：`go test -p 1 -count=1 ./...`（CI 权威，串行避 PG 竞争）；纯逻辑单测 + `.28` 实测 workspace 启动。
- **提交**：Conventional Commits，body 每行 ≤100 字符，AI 提交带 `Co-authored-by: Claude`。
- **前端**：非 shadcn，原生 HTML + Tailwind v4；`ModelSelect` 已存在（`app/_components/model-select.tsx`，发 `cmd_xxx`）；`lib/api-types.ts` 不可手改（swag regen + CI drift check）。
- **CI 启动冒烟**会拦路由注册 panic/启动 fatal；改完路由/handler/config.Load 后这条兜底。
- **.28 共享机**：只动 `deploy_` 前缀容器；SSH `ssh -o PubkeyAcceptedAlgorithms=+ssh-rsa -i ~/.ssh/miscode root@10.10.0.28`。

## File Structure

| 文件                                           | 职责                    | 本计划改动                                                                                                                                         |
| ---------------------------------------------- | ----------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| `internal/compute/opencode_gen.go`             | opencode.json 生成      | **增** `GenerateOpenCodeConfigForModels` / `WriteOpenCodeConfigForModels` / `ResolveOpencodeModelID` / `ModelName`                                 |
| `internal/compute/opencode_gen_test.go`        | 上面的测试              | **增** 过滤/解析单测                                                                                                                               |
| `internal/codews/manager.go`                   | workspace 会话/工具启动 | **改** `Ensure` 加 model 参；**增** per-user XDG config 写入 + env 注入；`toolEnv` 加 model 参（claude）；**增** `ModelConfigWriter` 窄接口 + 字段 |
| `internal/codews/manager_test.go`              | 纯逻辑单测              | **增** env 构建 / 路径推导单测                                                                                                                     |
| `internal/appdeploy/handler.go`                | `/workspace` 等 HTTP    | **改** `Workspace` 加 model 字段 + `IsGranted`（CtxUserDBID）+ 传 model 给 Ensure；**增** grant 接线                                               |
| `internal/appdeploy/handler_test.go`           | workspace 单测          | **增** model 透传/越权 403 单测                                                                                                                    |
| `internal/dev/handler.go`                      | `/code` handler         | **改** Submit 前解析 `cmd_xxx`→`provider/name`                                                                                                     |
| `internal/dev/coding.go`                       | headless opencode run   | model 默认值注释更新（逻辑不变，名字由 handler 解析后传入）                                                                                        |
| `cmd/server/main.go`                           | DI 装配                 | **改** `appdeploy.Register` 传 computeStore；codews Manager 注入 ModelConfigWriter                                                                 |
| `app/workspace/workspace-frame.tsx`            | opencode iframe 宿主    | **改** 加 model state + `/workspace` POST body 加 model                                                                                            |
| `app/workspace/workspace-toolbar.tsx`          | 工作台工具栏            | **改** 加 ModelSelect 槽位                                                                                                                         |
| `app/dev/page.tsx`                             | 手动派发编码            | **改** input→ModelSelect，初值 `""`，body 用 `model                                                                                                |     | undefined` |
| `platform/backend/docs/*` + `lib/api-types.ts` | OpenAPI 契约            | swag regen（`/workspace` body 增 model）                                                                                                           |

---

### Task 1: compute per-user config 与模型解析 helper

**Files:**

- Modify: `platform/backend/internal/compute/opencode_gen.go`
- Test: `platform/backend/internal/compute/opencode_gen_test.go`（若无则建）

**Interfaces:**

- Produces（供 Task 2/3 调用，签名固定）:
  ```go
  // 只含指定 modelIDs（及其 provider）的 opencode config；未命中模型跳过。
  func (s *Store) GenerateOpenCodeConfigForModels(ctx context.Context, modelIDs []string) (*OpenCodeConfig, error)
  // 写到 path（mkdir as needed）。
  func (s *Store) WriteOpenCodeConfigForModels(ctx context.Context, modelIDs []string, path string) error
  // cmd_xxx → "slugify(provider.Name)/model.Name"；未命中返 ("", nil)。
  func (s *Store) ResolveOpencodeModelID(ctx context.Context, modelID string) (string, error)
  // cmd_xxx → model.Name（claude ANTHROPIC_MODEL 用）；未命中返 ("", nil)。
  func (s *Store) ModelName(ctx context.Context, modelID string) (string, error)
  ```
- 复用现有：`slugify`、`firstNonEmpty`、`ListProviders`、`ListModels`、model/provider 查询。provider key 与 model key 推导**必须与 `GenerateOpenCodeConfig` 完全一致**（同 `slugify(p.Name)` / `m.Name`）。

- [ ] **Step 1: 写失败测试**

```go
func TestStore_GenerateOpenCodeConfigForModels(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "compute_model", "compute_provider")
	s := compute.NewStore(db)
	ctx := context.Background()
	prov := &compute.Provider{Name: "ZAI Coding", Type: "api", BaseURL: "http://x", APIKey: "k1", Enabled: true}
	if err := s.CreateProvider(ctx, prov); err != nil { t.Fatal(err) }
	m1 := &compute.Model{ProviderID: prov.ID, Name: "glm-5.1", Modality: "code", ContextWindow: 204800, MaxOutput: 131072, Enabled: true}
	if err := s.CreateModel(ctx, m1); err != nil { t.Fatal(err) }
	m2 := &compute.Model{ProviderID: prov.ID, Name: "glm-5-turbo", Modality: "code", ContextWindow: 204800, MaxOutput: 131072, Enabled: true}
	if err := s.CreateModel(ctx, m2); err != nil { t.Fatal(err) }

	cfg, err := s.GenerateOpenCodeConfigForModels(ctx, []string{m1.ID})
	if err != nil { t.Fatalf("GenerateOpenCodeConfigForModels: %v", err) }
	p, ok := cfg.Provider["zai-coding"]
	if !ok { t.Fatalf("期望 provider key zai-coding，got %v", cfg.Provider) }
	if p.Options.APIKey != "k1" { t.Errorf("apiKey 期望 k1，got %v", p.Options.APIKey) }
	if _, has := p.Models["glm-5.1"]; !has { t.Error("期望含 glm-5.1") }
	if _, has := p.Models["glm-5-turbo"]; has { t.Error("不应含 glm-5-turbo（未授权）") }

	id, err := s.ResolveOpencodeModelID(ctx, m1.ID)
	if err != nil || id != "zai-coding/glm-5.1" {
		t.Fatalf("ResolveOpencodeModelID 期望 zai-coding/glm-5.1，got %q err=%v", id, err)
	}
	name, err := s.ModelName(ctx, m1.ID)
	if err != nil || name != "glm-5.1" {
		t.Fatalf("ModelName 期望 glm-5.1，got %q err=%v", name, err)
	}
	// 写盘
	tmp := t.TempDir() + "/opencode/opencode.json"
	if err := s.WriteOpenCodeConfigForModels(ctx, []string{m1.ID}, tmp); err != nil { t.Fatal(err) }
	if _, err := os.Stat(tmp); err != nil { t.Errorf("config 未写入: %v", err) }
}
```

- [ ] **Step 2: 跑测试确认失败** — `go test -run TestStore_GenerateOpenCodeConfigForModels ./internal/compute/...` → FAIL（方法未定义）

- [ ] **Step 3: 实现** — 在 `opencode_gen.go` 加 4 个方法。`GenerateOpenCodeConfigForModels`：构造 `modelIDs` set → 复用 `GenerateOpenCodeConfig` 的遍历逻辑，但 model 仅当 `m.ID ∈ set` 才加入，provider 仅当有 ≥1 命中 model 才加入。`WriteOpenCodeConfigForModels`：`mkdir(filepath.Dir(path))` + `MarshalIndent` + `WriteFile`（参照 `WriteOpenCodeConfig:106-123`）。`ResolveOpencodeModelID`：查 model→provider，返 `slugify(p.Name)+"/"+m.Name`。`ModelName`：查 model 返 `m.Name`。

- [ ] **Step 4: 跑测试确认通过** — `go test -run TestStore_GenerateOpenCodeConfigForModels ./internal/compute/...` → PASS

- [ ] **Step 5: 提交** — `git add internal/compute/opencode_gen.go internal/compute/opencode_gen_test.go && git commit -m "feat(compute): add per-model opencode config + model id resolution helpers"`

---

### Task 2: codews Manager per-user XDG config 注入 + toolEnv model

**Files:**

- Modify: `platform/backend/internal/codews/manager.go`
- Test: `platform/backend/internal/codews/manager_test.go`（若无则建）

**Interfaces:**

- Consumes（Task 1 产出）：`compute.Store` 的 `WriteOpenCodeConfigForModels` / `ModelName`。
- Produces（Task 3 调用）：`Ensure` 新增 `model string` 末参。
- 新增窄接口（codews 不 import compute）：
  ```go
  // ModelConfigWriter 解耦 codews ↔ compute（codews 不直接依赖 compute 包）。
  type ModelConfigWriter interface {
      WriteOpenCodeConfigForModels(ctx context.Context, modelIDs []string, path string) error
      ModelName(ctx context.Context, modelID string) (string, error)
  }
  ```
- Manager 增字段 `writer ModelConfigWriter`（可空=兜底全局 config）；`Ensure` 末参加 `model string`。

- [ ] **Step 1: 写失败测试**（纯逻辑：env 构建 + XDG 路径推导，不启真实进程）

```go
func TestManager_xdgConfigDir(t *testing.T) {
	// per-(app,user) XDG 根目录推导
	got := xdgConfigDir("/root/.cache/anp-codews", "app1", "alice")
	want := "/root/.cache/anp-codews/app1-alice"
	if got != want { t.Errorf("xdgConfigDir 期望 %s，got %s", want, got) }
}

func TestManager_opencodeEnv_includesXDG(t *testing.T) {
	// model 非空 → opencode env 含 XDG_CONFIG_HOME 指向 per-user dir
	env := buildOpenCodeEnv("/root/.cache/anp-codews/app1-alice")
	if !contains(env, "XDG_CONFIG_HOME=/root/.cache/anp-codews/app1-alice") {
		t.Errorf("opencode env 缺 XDG_CONFIG_HOME: %v", env)
	}
}

func TestManager_claudeEnv_includesModel(t *testing.T) {
	// claude env 的 ANTHROPIC_MODEL 取传入 model 名
	env := buildClaudeEnv("https://x", "key", "glm-4.6")
	if !contains(env, "ANTHROPIC_MODEL=glm-4.6") {
		t.Errorf("claude env 缺 ANTHROPIC_MODEL: %v", env)
	}
}
```

（`buildOpenCodeEnv`/`buildClaudeEnv`/`xdgConfigDir` 为从 `toolEnv`/`Ensure` 抽出的纯函数，便于单测。）

- [ ] **Step 2: 跑测试确认失败** — `go test -run TestManager_ ./internal/codews/...` → FAIL

- [ ] **Step 3: 实现**
  - `manager.go`：加 `ModelConfigWriter` 接口 + `Manager.writer` 字段。
  - 抽出 `xdgConfigDir(base, appID, userID) string` = `filepath.Join(base, appID+"-"+sanitize(userID))`。
  - `Ensure` 末参加 `model string`：model 非空且 `m.writer != nil` →
    1. `dir := xdgConfigDir(base, appID, userID)`；`cfgPath := filepath.Join(dir, "opencode", "opencode.json")`；
    2. `m.writer.WriteOpenCodeConfigForModels(ctx, []string{model}, cfgPath)`；
    3. 把 `dir` 存入 session（供 env 用）。
  - `toolEnv` 改签名 `toolEnv(toolName, model string)`：
    - opencode：返 `[]string{"XDG_CONFIG_HOME=" + dir}`（dir 由 Ensure 经 session/参数传入；若 model 空 → 返 nil 用全局）。
    - claude：`ANTHROPIC_MODEL` 从 `m.cfg.Get("claude_model",...)` 改为：model 非空 → `m.writer.ModelName(ctx, model)`；空 → 沿用全局 `claude_model`。
  - `tool.Start(workDir, port, m.toolEnv(tool, model))`（manager.go:151）传新参。
  - `Session` struct 加 `Model string` 字段（内存，不持久化，无 migration）。
  - base 目录常量：`const codewsXDGBase = "/root/.cache/anp-codews"`（容器内可写；或取 `os.Getenv("CODEWS_XDG_BASE")` 覆盖）。

- [ ] **Step 4: 跑测试确认通过** — `go test -run TestManager_ ./internal/codews/...` → PASS；再 `go build ./...` 确保全包编译（Ensure 签名变更，调用点 Task 3 修）。

- [ ] **Step 5: 提交** — `git commit -m "feat(codews): inject per-user authorized model via XDG_CONFIG_HOME"`

> ⚠️ 本 task 改 `Ensure` 签名 → `appdeploy/handler.go:259` 暂编译失败，Task 3 修。`go build ./...` 在 Task 3 后才全绿；本 task 仅 `go build ./internal/codews/...` + 单测绿。

---

### Task 3: appdeploy `/workspace` + dev `/code` handler 接线 + main.go DI

**Files:**

- Modify: `platform/backend/internal/appdeploy/handler.go`
- Modify: `platform/backend/internal/appdeploy/handler_test.go`（若无则建）
- Modify: `platform/backend/internal/dev/handler.go`
- Modify: `cmd/server/main.go`

**Interfaces:**

- Consumes：Task 1 `ResolveOpencodeModelID`；Task 2 `Ensure(..., model)` + `ModelConfigWriter`。
- appdeploy 新增本地 duck-typed 接口（拷 `dev/handler.go:15-17` 模式）：

  ```go
  type grantChecker interface { IsGranted(ctx context.Context, userID, modelID string) (bool, error) }
  ```

- [ ] **Step 1: 写失败测试**（appdeploy workspace：model 透传 + 越权 403）

```go
func TestHandler_Workspace_modelGrant(t *testing.T) {
	// mock codeWS.Ensure 捕获传入 model；mock grantChecker
	// 1) POST /workspace model=m1，授权通过 → Ensure 收到 model=m1
	// 2) POST /workspace model=m2，未授权 → 403 biz code 40302，Ensure 不被调
}
```

（dev `/code` 已有 IsGranted 测试覆盖；本 task 仅加「Submit 收到解析后的 provider/name」断言。）

- [ ] **Step 2: 跑测试确认失败**

- [ ] **Step 3: 实现**
  - `appdeploy/handler.go`：
    - `Handler` 加 `grants grantChecker` 字段。
    - `Workspace`（:239-270）：请求 struct 加 `Model string \`json:"model,omitempty"\``；校验：
      ```go
      if in.Model != "" && h.grants != nil {
          uid := c.GetString(auth.CtxUserDBID) // usr_xxx（grant 表 user_id）
          ok, err := h.grants.IsGranted(c.Request.Context(), uid, in.Model)
          if err != nil { /* log warn，不泄细节 */ }
          if !ok { httpx.Err(c, 403, 40302, "无权使用该模型"); return }
      }
      user := c.GetString(auth.CtxUserID) // 用户名（worktree 用，保持不变）
      sess, err := h.codeWS.Ensure(psID, aid, a.RepoDir, user, in.Tool, in.RequirementID, in.Model)
      ```
    - `Register` 签名加 `grants grantChecker`（或 `computeStore`），传入构造 `Handler`。
  - `dev/handler.go`：`Code` 在 IsGranted 通过后、`Submit` 前，把 `req.Model`(cmd_xxx) 解析成 opencode id：
    ```go
    modelForRun := req.Model
    if req.Model != "" && h.resolver != nil {
        if id, err := h.resolver.ResolveOpencodeModelID(ctx, req.Model); err == nil && id != "" {
            modelForRun = id
        }
    }
    h.agent.Submit(..., modelForRun)
    ```
    （`h.resolver` = 复用已注入的 computeStore，扩 `computeGrantChecker` 接口加 `ResolveOpencodeModelID`，或直接持 `*compute.Store`。）
  - `main.go`：
    - `appdeploy.Register(v1, ..., computeStore)`（:269）加 computeStore → 内部传给 `codews.NewManager`（设 `Manager.writer = computeStore`）+ `Handler.grants = computeStore`。
    - `dev.Register` 已传 computeStore（:299）；确保 dev handler 能调 `ResolveOpencodeModelID`。
  - swag 注解：`Workspace` 请求 struct 加 `model` 字段的 `@Param`；dev `/code` 的 `@Param model` 已存。

- [ ] **Step 4: 跑测试 + 全包编译** — `go test -p 1 -count=1 ./internal/appdeploy/... ./internal/dev/... ./internal/codews/...` → PASS；`go build ./...` 全绿。

- [ ] **Step 5: 提交** — `git commit -m "feat(appdeploy,dev): enforce model grant on /workspace and resolve model id for /code"`

---

### Task 4: 前端 ModelSelect 挂载 + swag regen + .28 实测

**Files:**

- Modify: `platform/frontend/app/workspace/workspace-frame.tsx`
- Modify: `platform/frontend/app/workspace/workspace-toolbar.tsx`
- Modify: `platform/frontend/app/dev/page.tsx`
- Modify: `platform/frontend/app/workspace/__tests__/*` 或就近 vitest（若有）
- Regenerate: `platform/backend/docs/*` + `platform/frontend/lib/api-types.ts`

**Interfaces:**

- Consumes：`ModelSelect`（`app/_components/model-select.tsx`，发 `cmd_xxx`）；`/workspace` body 增 `model`（Task 3 后端就绪）。

- [ ] **Step 1: 写失败测试**（workspace-frame：POST body 含 model）

```ts
// 仿现有 fetch 测试模式：mount workspace-frame，选模型，触发 /workspace POST，
// 断言 fetch body.model === "cmd_xxx"；未选时 body 不含 model（或 undefined）。
```

（dev/page：断言 ModelSelect 渲染、POST /code body.model = model||undefined。）

- [ ] **Step 2: 跑测试确认失败** — `pnpm --filter frontend test`

- [ ] **Step 3: 实现**
  - `workspace-frame.tsx`：加 `const [model, setModel] = useState("")`；`/workspace` POST body（:143）加 `model: model || undefined`；`<WorkspaceToolbar>` 加 `model={model} onModelChange={setModel}` props。
  - `workspace-toolbar.tsx`：props 加 `model/onModelChange`；工具栏渲染 `<ModelSelect taskType="code" value={model} onChange={onModelChange} className="..." />`（与 `requirements/page.tsx:266` 同模式）。
  - `dev/page.tsx`：`:35` `useState("zai-coding/glm-5.1")`→`useState("")`；`:134-141` `<input>`→`<ModelSelect taskType="code" value={model} onChange={setModel} className="w-full..." />`（顶部 `import { ModelSelect } from "@/app/_components/model-select"`）；`:77` `model`→`model: model || undefined`。
  - swag regen：`cd platform/backend && swag init -g cmd/server/main.go -o docs --parseDependency --parseInternal`；`cd platform/frontend && pnpm exec swagger2openapi ../backend/docs/swagger.json -o ../backend/docs/openapi.json --patch && pnpm exec openapi-typescript ../backend/docs/openapi.json -o lib/api-types.ts`。

- [ ] **Step 4: 跑测试 + 类型/lint/build** — `pnpm --filter frontend test` → PASS；`pnpm --filter frontend exec tsc --noEmit`；`pnpm --filter frontend lint`；`pnpm --filter frontend build`。

- [ ] **Step 5: 提交** — `git add ... && git commit -m "feat(frontend): mount ModelSelect in coding workbench + dev dispatch"`

- [ ] **Step 6: .28 实测**（部署后）— admin 给某用户授权一个 code 模型 → 该用户开工作台 → `docker exec deploy_backend_1 ls /root/.cache/anp-codews/<app>-<user>/opencode/` 确认 per-user config 生成 → `opencode` iframe 内模型下拉只含授权模型 → 未授权用户开工作台走全局 config（兜底）。

---

## 自查（写计划后）

- **Spec 覆盖**：§4.5 opencode（XDG 注入，Task 2）✓、claude（ANTHROPIC_MODEL，Task 2）✓、`/code`（Task 3）✓、`/workspace`（Task 3+4）✓；§5.2 dev/page.tsx（Task 4）✓、workspace-frame（Task 4）✓。**requirements/chat 不在本计划**（net-new、非编码主路径，列 follow-up）。
- **占位符**：无 TBD/TODO；关键签名与代码齐。
- **类型一致**：`Ensure` 末参 `model string`（Task 2 产 / Task 3 用）；`ModelConfigWriter.WriteOpenCodeConfigForModels`+`ModelName`（Task 1 产 / Task 2 用）；`ResolveOpencodeModelID`（Task 1 产 / Task 3 用）—— 跨 task 名字一致。
- **风险**：`Ensure` 签名变更致 Task 2→3 间 `go build ./...` 暂红（已注明）；claude model-name 语义（授权模型是否在 anthropic-compat endpoint 可用）是数据/配置问题非代码问题；per-user config 不持久化（每次 Ensure 重生成，无 migration）。

## 执行后 follow-up（不在本计划）

- requirements/chat/page.tsx 加 model 字段（net-new，非编码路径）。
- m2（ModelSelect fallback 未在 .catch 重置）、m4（saveGrants 无 catch）等 PR#10 遗留 Minor。
- 共享 1GB `opencode.db` 的 per-user data 隔离（运维项，非本期）。

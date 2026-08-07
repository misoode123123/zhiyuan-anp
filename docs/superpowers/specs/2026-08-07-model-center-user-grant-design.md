# ANP 模型中心 · 用户模型授权与选择（Phase 1）

> **取代** `2026-08-07-model-center-credential-security-design.md`（凭证 AES 加密方向，已废弃；凭证维持现状）。
> 基于：用户三需求（① 系统对接供应商 ② 管理员给用户分配模型 ③ 用户在界面选具体模型）+ 2026-08-07 `compute_*` 全代码核验。
> **范围**：ANP 平台自身 AI 功能（需求规格生成 / QA 测试生成 / dev 手动派发编码）。**不含**产出应用运行时的模型选择。

---

## 一、目标与非目标

### 目标（本期）

1. **管理员授权**：管理员给每个用户分配「可使用的模型」集合。
2. **用户选模型**：平台 AI 功能界面提供模型下拉，选项 = 当前用户被授权的模型。
3. **授权校验**：模型调用转发前校验「该用户是否被授权此模型」，防越权。

### 非目标（不做）

- 用户自带凭证 / per-user key（平台持有 key，用户不带 key、看不到 key）。
- 凭证 AES 加密（`compute_provider.api_key` 明文维持现状）。
- 产出应用运行时的模型选择（与本次无关）。
- 收拢 `appdeploy` 两处散落模型调用（非用户选模型路径，留后续）。

---

## 二、现状核验（决策依据，file:line 锚定真实代码）

| 维度                         | 现状（核验确认）                                                                                                                                                                  | 说明                                                        |
| ---------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------- |
| 模型目录                     | `compute_provider`/`compute_model`/`compute_route`（migration `000011_compute_provider_model.up.sql`）                                                                            | 已是多 provider/多 model/按 task_type 路由的目录            |
| **Gateway 已支持指定 model** | `compute.Gateway.Chat`（`route.go:125`）：`req.Model != ""` → 直接用该 model ID **绕过路由**；否则 `GetRoute(taskType)`（`route.go:126-143`）                                     | 🔑 **转发能力已就绪**，只差把前端选择透传进来 + 授权校验    |
| `ChatRequest` 字段           | `route.go:100-105`：`task_type` / `model` / `messages` / `project_space_id`                                                                                                       | **无 `user_id`** → 授权校验需补                             |
| 调用方现状                   | `requirement/service.go:299`（spec）、`qa/service.go:71`（test）调 `gateway.Chat` **均不传 Model**；`/compute/chat` handler（`provider_handler.go:319`）可收前端 model            | service 调用链要加 model 透传                               |
| `codews` 不走 Gateway        | `internal/codews` 全包 grep `compute\|gateway` **零命中**；编码走 opencode（配置由 `opencode_gen.go` 生成）                                                                       | 编码工具模型控制在 Tool 接口层注入（§4.5），非 Gateway 路径 |
| 前端 AI 入口                 | 需求生成（`requirements/page.tsx:106`）、对话式（`requirements/chat/page.tsx:129`）、测试（`testing/page.tsx:81`）、工作台（`workspace-frame.tsx:290/363/421`）**全部不传 model** | 由后端 route 默认                                           |
| 唯一传 model 的前端          | `dev/page.tsx:35` 硬编码 `"zai-coding/glm-5.1"`，自由文本 `<input>`（`:137-140`）                                                                                                 | 最低成本改造点：换下拉                                      |
| 用户管理页                   | `app/admin/users/page.tsx`（用户 CRUD + 空间成员 + 角色），**无任何模型授权代码**                                                                                                 | 授权 UI 从零挂此页                                          |
| `users` 表                   | `000001_init.up.sql:37-44`：无 model/权限字段（连 `role` 列都没有；权限在 `auth/guard.go` 硬编码）                                                                                | 需新授权表                                                  |
| 模型/路由数据源              | `GET /compute/models`（`compute/page.tsx:82`）、`GET /compute/routes`（`:86`）                                                                                                    | 下拉选项与默认值的数据源已就绪                              |

**核心判断**：目录 + 网关（含 model 透传）已就绪，真正空白是**授权数据 + 授权校验 + 前端下拉**。本期填这三块。

---

## 三、数据模型

### 3.1 新表 `user_model_grant`（migration `000035_user_model_grant`）

```sql
-- 000035_user_model_grant.up.sql
CREATE TABLE IF NOT EXISTS user_model_grant (
    user_id     TEXT        NOT NULL,
    model_id    TEXT        NOT NULL REFERENCES compute_model(id) ON DELETE CASCADE,
    granted_by  TEXT,                       -- 授权人（审计），管理员 user_id
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, model_id)
);

-- 000035_user_model_grant.down.sql
DROP TABLE IF EXISTS user_model_grant;
```

**设计决策**：

- **用户级全局授权**（不带 `project_space_id`）：用户需求是「给每个用户分配」，授权是跨空间的全局策略，非租户资源。
  > 注：`开发标准与规范.md` §2.4 建议「所有表带 `project_space_id`」。本表有意不带——它是**用户级权限**而非租户业务数据；未来若要按空间细分授权，再加 `project_space_id` 列（`NULL`=全局）。这是对规范的有意偏离，原因如上。
- **`ON DELETE CASCADE`**：模型被删 → 授权自动收回，无悬挂引用。
- **审计字段** `granted_by` / `created_at` 遵循规范。

---

## 四、后端设计

### 4.1 `compute.Store` 增授权方法（`internal/compute/grant.go` 新文件）

```go
// 用户模型授权 CRUD。user_id/model_id 来自 user_model_grant 表。
func (s *Store) ListGrants(ctx context.Context, userID string) ([]Model, error)        // 该用户已授权的模型（JOIN compute_model 取详情）
func (s *Store) GrantModels(ctx context.Context, userID string, modelIDs []string, grantedBy string) error  // 批量授权（INSERT ... ON CONFLICT DO NOTHING）
func (s *Store) RevokeModel(ctx context.Context, userID, modelID string) error          // 收回
func (s *Store) IsGranted(ctx context.Context, userID, modelID string) (bool, error)    // 单点校验
```

### 4.2 Gateway 授权校验（`internal/compute/route.go`）

`ChatRequest` 增字段 `UserID string`（`route.go:100-105`）。`Chat` 里在「`req.Model != ""`」分支补一步校验：

```go
if req.Model != "" {
    if req.UserID != "" {
        ok, err := g.store.IsGranted(ctx, req.UserID, req.Model)
        if err != nil { /* 记 warn，不泄露细节 */ }
        if !ok {
            return nil, ErrModelNotAuthorized   // 明确拒绝，不 fallback 到路由
        }
    }
    // 校验通过 → 用 req.Model（现有逻辑不变）
}
```

- **越权即拒**：不 fallback 到路由默认（否则用户以为用了 A 模型，实际走了 B，语义混乱）。
- 空 `UserID`（兼容老调用）→ 不校验，保持现状（渐进迁移；service 改造后都会有 user_id）。
- 错误信息不含 key / 内部细节。

### 4.3 service 调用链透传 model + user_id

| service          | 现状（file:line）                                                             | 改造                                                                             |
| ---------------- | ----------------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| 需求规格生成     | `requirement/service.go:299` 调 `gateway.Chat`，`TaskType:"spec"`，不传 Model | `Generate(...)` 入参增 `model`、`userID`；透传到 `ChatRequest.Model` / `.UserID` |
| QA 测试生成      | `qa/service.go:71`，`TaskType:"test"`，不传 Model                             | 同上，`GenerateTests(...)` 增 `model`、`userID`                                  |
| `/code` 手动派发 | `dev/coding.go`（headless `opencode run`，不经 Gateway）                      | 在 `/code` handler（`dev` 路由）调 `IsGranted` 校验后再 run（统一防线，防绕过）  |

> `requirement` / `qa` handler 从 gin context 取 `user_id`（鉴权中间件已注入）传给 service。

### 4.4 HTTP API（新增端点）

| Method   | Path                          | 说明                                 | 鉴权     |
| -------- | ----------------------------- | ------------------------------------ | -------- |
| `GET`    | `/users/:id/models`           | 查某用户已授权模型（管理员授权页用） | 管理员   |
| `POST`   | `/users/:id/models`           | 批量授权 `{model_ids:[]}`            | 管理员   |
| `DELETE` | `/users/:id/models/:model_id` | 收回单个                             | 管理员   |
| `GET`    | `/users/me/models`            | **当前用户**可用模型（前端下拉用）   | 登录即可 |

- 挂载：新建 `GrantHandler.Register`，路由注册于 `main.go:282-283` 区域（与 compute 路由同组）。
- 鉴权：管理员判定复用 `auth/guard.go` 的 `config.manage`（与 `PUT /compute/routes` 同属模型配置范畴，不新增权限，YAGNI）；`/users/me/models` 仅需登录态。
- `swagger.json`/`api-types.ts` 同步。

### 4.5 编码工具模型控制（codews · 所有工具统一）

**架构原则**：任何编码工具（opencode / claude / codex / 未来接入）启动时，系统都按「当前用户授权的模型」配置工具——模型控制是 `Tool` 接口层（`internal/codews/tool.go`）的职责，与具体工具无关，不因工具而异绕过。

`codews.Manager.Ensure` 已持有 `userID`（session key = `app:userID`，`manager.go:123`）。启动工具前：

1. **解析编码模型**：前端工作台 `ModelSelect(taskType="code")` 选定值，随 `/workspace` POST 的 `model` 字段传入；未传则取用户授权模型中 `code` 路由的 primary，再否则第一个授权模型。
2. **授权校验**：`IsGranted(userID, model)`——未授权即拒（与 Gateway 同防线，防绕过）。
3. **按工具落地**（`toolEnv` / `Start` 注入）：

| 工具         | 现状（核验）                                                                                                                                    | 改造（注入用户授权模型）                                                                                                                                                                                                                                             |
| ------------ | ----------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| opencode     | `tool.go:24-34` serve 只读全局 `$HOME/.config/opencode/opencode.json`；核验 `serve --help` **无 `--config` flag**，也不读 `OPENCODE_CONFIG` env | per-session 生成 opencode.json（`compute.GenerateOpenCodeConfig` 用该用户模型）+ **config 路径隔离**：优先 `XDG_CONFIG_HOME=<dir>` env（若 opencode 遵循 XDG，最干净、不动 data）；否则 `HOME=<dir>` 隔离（session data 随之隔离，`ensureSession` 恢复路径同构调整） |
| claude       | `manager.go:90-92` 已注 `ANTHROPIC_MODEL`，但来自全局 `claude_model` 配置                                                                       | `ANTHROPIC_MODEL` 改取**用户授权模型**（env 注入机制已在，只改来源）                                                                                                                                                                                                 |
| codex / 未来 | `Tool` 接口预留（`tool.go:53`）                                                                                                                 | 接入时在 `Start`/`toolEnv` 遵守同一原则                                                                                                                                                                                                                              |

> **待实现时核验**：opencode 是否遵循 `XDG_CONFIG_HOME`（决定走 XDG 还是 HOME 隔离）。两者皆可落地——XDG 更干净（只隔离 config，不影响 session data 目录与恢复）；HOME 隔离则顺带把 session 存储隔离到 per-(app,user)，恢复逻辑同构。

---

## 五、前端设计

### 5.1 统一模型下拉组件 `ModelSelect`

新建 `platform/frontend/app/components/model-select.tsx`（shadcn 风格 `<Select>`）：

- 数据源：`GET /users/me/models`（当前用户授权模型）。
- 默认值：授权列表非空取第一个；**空列表 fallback** 到 `GET /compute/routes/{task_type}` 的 `primary_model`（不阻断工作，附提示「未授权模型，使用平台默认」）。
- 受控组件，`value`/`onChange`，`taskType` prop 决定默认值来源。

### 5.2 挂载点（各 AI 入口）

| 入口         | file:line                                                   | 改造                                                                                                                              |
| ------------ | ----------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| 需求规格生成 | `requirements/page.tsx:230-263`（空间/应用选择行）          | 加 `<ModelSelect taskType="spec">`，值进 `generate()` body（`:109`）                                                              |
| 对话式需求   | `requirements/chat/page.tsx:392-441`（工具栏）              | 加 `<ModelSelect taskType="chat">`，值进消息 body（`:129`）                                                                       |
| dev 手动派发 | `dev/page.tsx:35,137-140`                                   | **替换硬编码自由文本 input** 为 `<ModelSelect taskType="code">`                                                                   |
| 测试生成     | `testing/page.tsx:194-207`                                  | 加 `<ModelSelect taskType="test">`                                                                                                |
| 工作台       | `workspace/workspace-frame.tsx:466-485`（WorkspaceToolbar） | `<ModelSelect taskType="code">`：选定值随 `/workspace` POST 传 codews 启动工具（§4.5）；inject/breakdown/submit 另按各自 taskType |

各 fetch body 增 `model` 字段（用户选了才传，空则后端走 route）。

### 5.3 管理员授权页（挂 `app/admin/users/page.tsx`）

- 用户卡片（`:128-153`）增「授权模型」入口（按钮 / chips 展示已授权，类似现展示 spaces/role 的方式 `:140-149`）。
- 点开抽屉/弹层：多选 `compute_model`（数据源 `GET /compute/models`），已授权预勾选，保存调 `POST /users/:id/models`。
- 全新代码（前端无任何现成授权 UI）。

---

## 六、Global Constraints（遵循 `docs/详细设计/开发标准与规范.md`）

本期实现须遵循平台开发规范：

- **Go**：`golangci-lint` + `gofmt`；`context.Context` 贯穿；错误显式 `error` 返回；package 边界（跨域调 Service 接口，`compute.Store` 方法供 requirement/qa 调）。
- **SQL**：迁移版本化（`000035`，幂等 `IF NOT EXISTS`）、审计字段、`ON DELETE CASCADE` 防悬挂。
- **TypeScript**：`eslint` strict；统一 shadcn/ui `<Select>`；API 调用统一经 `API_BASE_URL`。
- **提交**：Conventional Commits，body 每行 ≤100 字符，AI 提交带 `Co-authored-by`。
- **测试**：覆盖授权校验、越权拒绝、级联回收（见 §7）。
- **安全**：错误信息不含 key/内部细节；越权拒绝不 fallback。

---

## 七、测试策略

`compute` 集成（复用 `testutil.TestDB` 连 .28 `anp_test` PG）：

- **Store**：`GrantModels`→`ListGrants` 往返；重复授权幂等（`ON CONFLICT DO NOTHING`）；`RevokeModel` 后 `IsGranted=false`；删 model → 授权级联消失（CASCADE）。
- **Gateway 校验**：`req.Model` 已授权 → 放行；未授权 → 返 `ErrModelNotAuthorized` 且**不 fallback**；空 `UserID` → 不校验（兼容）。
- **API**：`/me/models` 返回当前用户授权列表；非管理员调授权端点 → 403。
- **串行**：`go test -p 1 -count=1 ./internal/compute/... ./internal/requirement/... ./internal/qa/...`（CI 权威；本地 .28 PG）。

---

## 八、范围边界与已知限制

- **`appdeploy` 散落调用 — 本期不收拢**：`summarizeChange`/`checkRequirement` 两处手写硬编码调用，非用户选模型路径，留后续。
- **codex 等未接入工具**：本期 opencode/claude 落地模型控制；codex 接入时按 §4.5 同一原则实现。
- **授权粒度 = 用户级全局**：不按 project_space 细分（YAGNI；用户需求明确是「每个用户」）。

---

## 九、风险与注意点

- **越权防线一致性**：Gateway 校验只保护「经 Gateway」的调用；`/code`（dev）不经 Gateway，须在 handler 单独 `IsGranted`，否则成漏网入口。
- **service 签名变更 blast radius**：`requirement.Generate` / `qa.GenerateTests` 加参数，影响所有调用点（含 handler、可能的测试），需一并改。
- **空授权兜底**：用户未被授权任何模型时，下拉空 → fallback 平台默认 route（不阻断），避免新部署后用户突然用不了 AI。
- **渐进迁移**：老调用（空 `UserID`）不校验直接放行，保证改造期间不破坏现有流程；service 全部带上 user_id 后即全面生效。

---

## 十、一句话总结

复用已支持 `req.Model` 的 `compute.Gateway` 与 `compute_*` 目录，新增 `user_model_grant` 授权表 + 管理员授权页 + 平台 AI 功能的模型下拉；Gateway、`/code` handler、**codews 编码工具启动**（Tool 接口层：opencode config 隔离、claude env 注入）三处防线均校验/注入用户授权模型（越权即拒）；凭证维持现状，用户级/per-user 凭证留后续。

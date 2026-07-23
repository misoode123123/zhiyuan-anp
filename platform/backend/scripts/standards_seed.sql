-- standards_seed.sql
-- 全局编码规范（coding_standard, project_space_id IS NULL）幂等升级 SQL。
--
-- 数据源：internal/db/standards_seed.go（StandardSeeds）。
-- 重新生成：cd platform/backend && go run ./cmd/genseedstandards > scripts/standards_seed.sql
--
-- 执行方式（.28 等已运行环境）：
--   psql "$DATABASE_URL" -f scripts/standards_seed.sql
--
-- 幂等说明：DELETE 全局规范（含旧的 5 条占位）后 INSERT 最新全套。
-- 项目级规范（project_space_id 非空）不动，保留用户在 /governance 的调整。
-- 新环境（coding_standard 为空）无需跑此 SQL——main.go 启动时 SeedDemoStandards 自动播种。

-- 1. 清空全局规范（保留项目级）
DELETE FROM coding_standard WHERE project_space_id IS NULL;

-- 2. 插入全套规范（platform 6 / app 5 / module: api 6 + db 5 + code 5 + form 3 + ui 4 = 34 条）
INSERT INTO coding_standard (id, project_space_id, name, category, content, priority, enabled, scope, module) VALUES
  ('std_platform_1', NULL, '产出五约束', 'general', 'AI 产出必须满足五项约束（验收前置条件）：

- **可校验**：产出必须有可执行的验证手段（单测 / curl / 前端驱动渲染），不能只「看起来对」。
- **可追溯**：变更经 git commit + change 记录登记，能定位到来源需求与任务。
- **可回滚**：迁移配 down.sql；部署保留上一版本镜像，可回退到上一个稳定态。
- **守边界**：不跨模块直接访问他人 store/内部结构；经 main 注入的接口调用。
- **守权限**：写/危险操作必须挂 RBAC（AutoRequire 登记 routeOp），按角色最小授权。', 10, TRUE, 'platform', ''),
  ('std_platform_2', NULL, '安全基线', 'security', '安全底线（任何产出不得违反）：

- **密钥不硬编码**：API Key / 密码 / Token 一律走环境变量或 config.Store（system_config 表），不入源码、不入 git。
- **输入必校验**：外部输入（HTTP body/param、命令行参数）用 validator/v10 或显式校验；不信任前端。
- **凭据不泄露**：日志不打 Authorization / 密码字段；响应里 DATABASE_URL 做 mask；错误 message 不带敏感串。
- **SQL 参数化**：一律 $N 占位符，禁止字符串拼接 SQL（防注入）。
- **依赖收敛**：不引未审计的第三方库；CI 跑 govulnerability check。', 20, TRUE, 'platform', ''),
  ('std_platform_3', NULL, '错误处理规范', 'general', 'Go 错误处理统一约定：

- **层层包装**：`fmt.Errorf("op name: %w", err)` 保留错误链，便于上层 `errors.Is` 判断。
- **哨兵错误**：业务关键错误定义包级 `var ErrNotFound = errors.New(...)`，上层用 `errors.Is` 分支。
- **不 panic**：库代码不 panic；handler 用 `httpx.Err` 返回错误；意外 panic 由 `server.Recovery` 兜底回 500。
- **store 层不吞错**：`sql.ErrNoRows` 向上抛（不直接转 nil），由 handler 层按需映射 404。
- **业务码**：错误经 `httpx.Err(c, status, code, msg)` 返回，code 用模块错误码段（见 api 错误码规范）。', 30, TRUE, 'platform', ''),
  ('std_platform_4', NULL, '日志规范', 'general', '使用 zap 结构化日志（internal/log）：

- **关键路径 Info**：服务启动/监听地址、迁移完成、后台任务 tick 结果、外部调用结果。
- **错误 Error**：含 trace_id + 错误原因字段（`zap.Error(err)` + `zap.String("trace_id", ...)`）。
- **请求日志**：`server.RequestLogger` 自动记 method/path/status/latency，无需手写。
- **禁止明文凭据**：不打 API Key / Token / 密码；打 DATABASE_URL 时脱敏密码段。
- **禁 fmt.Println**：调试用 `logger.Debug`；临时排查后清理，不留垃圾日志。', 40, TRUE, 'platform', ''),
  ('std_platform_5', NULL, '事务与并发', 'general', '数据库事务与并发约束：

- **迁移事务**：每个迁移 SQL 事务包裹（migrate.go migrateUp 已实现），失败回滚不影响已应用版本。
- **业务事务**：跨表写操作用 `db.BeginTxx` + 显式 `Commit`/`Rollback`；单写可选 `ExecContext`。
- **并发靠约束**：用唯一约束（`CREATE UNIQUE INDEX ... WHERE`）+ `ON CONFLICT` 兜底竞态，不依赖应用层锁。
- **23505 处理**：唯一约束冲突（PG 错误码 23505）应用层捕获后重查复用（GetOrCreate 模式）。
- **长事务禁用**：事务中不调外部 HTTP / 不跑慢查询；外部调用放事务外。', 50, TRUE, 'platform', ''),
  ('std_platform_6', NULL, 'PostgreSQL 专用约定', 'general', '开发/生产强制 PostgreSQL（禁 SQLite，见 config.Load 校验）：

- **占位符 $N**：用 `$1, $2, ...`（pgx 风格），禁用 `?`（SQLite 风格，PG 跑会报错）。
- **BOOLEAN 传 bool**：PG BOOLEAN 列直接传 Go `bool`，不传 `int`（sqlite 驱动隐式转换已撤，PG 严格类型会报错）。
- **可空列 COALESCE**：查询时 `COALESCE(col, '''') AS col`，避免 NULL 扫到 `string` 触发 scan 错误。
- **库大小查询**：用 `pg_database_size()` / `pg_size_pretty()`（pgsupply.Collector 采集）。
- **测试走 PG**：testutil.TestDB 连 anp_test（PG），禁 sqlite `:memory:`（漏 PG 类型 bug）。
- **连接池**：MaxOpenConns 20 / MaxIdleConns 5（db.Open 默认），生产可调。', 60, TRUE, 'platform', ''),
  ('std_app_1', NULL, '应用 API 统一信封', 'general', '产出应用的 API 响应必须用与平台后端一致的统一信封：

```json
{"code": 0, "data": {...}, "message": "ok", "trace_id": "..."}
```

- **code=0 成功**；非 0 = 业务错误码（应用段，避开平台 5xxxx，见 api 错误码规范）。
- **成功**：HTTP 200 + code=0；**创建**：HTTP 201 + code=0；**错误**：对应 HTTP status + 非 0 code。
- **trace_id 透传**：从请求头 `X-Trace-Id` 读，无则生成；响应头回写 `X-Trace-Id`，便于跨 Go/Python 链路追踪。', 70, TRUE, 'app', ''),
  ('std_app_2', NULL, '应用认证', 'security', '应用通过 appgw 反代时，平台透传身份头（应用信任这些头）：

- **X-User**：登录用户名（appgw 验 JWT 后注入，应用可读作当前用户）。
- **X-Project-Space-Id**：当前项目空间 ID（多租户路由键）。
- **应用自校验**：可基于 X-User 做自身鉴权；不得仅信前端直传的身份（前端可伪造）。
- **直连场景**（不走 appgw）：应用自管 JWT，密钥经环境变量（appdeploy_env 注入）。', 80, TRUE, 'app', ''),
  ('std_app_3', NULL, '应用连库', 'general', '应用库由 pgsupply 供给（每项目一个独立 PG 实例 + 应用库）：

- **DATABASE_URL**：应用从环境变量读连接串（appdeploy_env 配置，部署时 `-e` 注入）。
- **应用 role 直连**：用 pgsupply 创建的应用 role（非 superuser），最小权限。
- **$N 占位符**：与平台一致，禁 `?`。
- **禁 sqlite**：应用库也是 PG；测试走 testutil PG。', 90, TRUE, 'app', ''),
  ('std_app_4', NULL, '应用路由与健康检查', 'general', '应用 HTTP 服务约定：

- **路由前缀 `/api/v1/...`**：appgw `/apps/<code>/` 反代后保留原路径。
- **必须实现 `/healthz`**：返回 `200 + {"status":"ok"}`，供 appgw/ops 探活。
- **推荐 `/version`**：返回 `{name, version}`，便于运维定位版本。
- **监听 `0.0.0.0:$PORT`**：容器内 EXPOSE internal_port，绑定 0.0.0.0（不是 127.0.0.1，否则反代不通）。', 100, TRUE, 'app', ''),
  ('std_app_5', NULL, '应用部署', 'general', '产出应用的可部署约定（appdeploy.Deployer 构建 + 运行）：

- **单容器**：一个 Dockerfile + `docker run`（多服务用 supervisor 或拆应用）。
- **EXPOSE internal_port**：Dockerfile 声明内部端口；host_port 由平台分配。
- **URL 规则**：`http://<appdeploy_host>:<host_port>`（AppDeployHost 配置对外主机）。
- **PORT 驱动**：应用读 `PORT` 环境变量决定监听端口（不硬编码 8080）。
- **环境变量**：接 `-e DATABASE_URL` / `-e PORT` / 应用 env（appdeploy_env 配置面板）。', 110, TRUE, 'app', ''),
  ('std_module_api_1', NULL, '响应信封', 'general', '所有 API handler 必须用 `httpx.OK` / `httpx.Created` / `httpx.Err` 返回统一信封：

- 成功：`httpx.OK(c, data)` → 200 + code=0
- 创建：`httpx.Created(c, data)` → 201 + code=0
- 失败：`httpx.Err(c, status, code, msg)` → 对应 HTTP status + 业务 code

禁止裸 `c.JSON({})` 或 `gin.H{}` 直接返回（`/healthz` / `/version` 例外）。', 100, TRUE, 'module', 'api'),
  ('std_module_api_2', NULL, '错误码', 'general', '错误码格式：5 位数字 = HTTP status（前 3 位）+ 模块内序号（后 2 位）。

**通用段**：40001 参数错误 / 40101 未登录 / 40301 无权限 / 40401 不存在 / 40901 冲突 / 42901 限流。

**5xx 模块段**（每模块独占一段，避免冲突）：
- workspace 50001 / dev 50002 / requirement 50003-04 / config 50005 / rule 50006
- standard 50007 / qa 50008 / release 50009 / auth-appgw-conversation 50010-12 / attendance 50013
- appdeploy 50020-22 / pgsupply 50030-32 / security 50050 / quota 50060-61 / ops 50070 / capability 50090

- body 的 `message` 用中文，面向用户可读（如「应用 xxx 不存在」）。
- 新模块接入时申领新段（如 500xx），不与他人复用。', 110, TRUE, 'module', 'api'),
  ('std_module_api_3', NULL, '分页', 'general', 'list 接口分页约定：

- **入参**：`?page=1&page_size=20`（page 从 1 起）。
- **返回**：`{items: [...], total: N, page: 1, page_size: 20}`。
- 大结果集必须分页（禁全量返回）；缺省 `page_size=20`，上限 100（防拉爆内存）。
- 排序用 `?order=created_at:desc` 风格；白名单字段（不透传任意列名，防注入）。', 120, TRUE, 'module', 'api'),
  ('std_module_api_4', NULL, '命名', 'general', '命名约定（URL + JSON）：

- **URL**：kebab-case + 资源复数（`/project-spaces/:id/apps/:aid`）。
- **JSON 字段**：snake_case（`project_space_id`, `created_at`, `is_secret`）。
- **路径参数**：`:id`, `:aid` 等短名（与 swag 注解 path 对齐）。
- **query 参数**：snake_case（`project_space_id`），与 JSON 字段一致。', 130, TRUE, 'module', 'api'),
  ('std_module_api_5', NULL, 'OpenAPI 文档', 'general', '每个 handler 必须写 swag 注解（生成 /swagger/index.html）：

```go
// @Summary      应用列表
// @Tags         appdeploy
// @Param        id   path  string  true  "项目空间ID"
// @Success      200  {object}  map[string]interface{}  "应用列表"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps [get]
```

- Tags 用模块名（appdeploy/standard/pgsupply）。
- 新接口必须补 swag，否则不出现在 Swagger 文档。
- 路径用 `{id}` 占位（swag 风格），不是 `:id`。', 140, TRUE, 'module', 'api'),
  ('std_module_api_6', NULL, '幂等与并发', 'general', '写操作考虑幂等与并发安全：

- **幂等**：重复请求不产生副作用，用 `ON CONFLICT DO UPDATE` 或先查后插（GetOrCreate 模式）。
- **并发靠约束**：DB 唯一约束兜底（`CREATE UNIQUE INDEX ... WHERE status=''active''`），应用层捕获 23505 重查复用。
- **乐观锁**：必要时加 `version` 列 + `WHERE version=$n`（更新时校验版本）。
- **避免跨进程锁**：不用应用层 Mutex（多副本失效）；分布式锁用 PG advisory lock 或 Redis。', 150, TRUE, 'module', 'api'),
  ('std_module_db_1', NULL, '命名', 'general', '数据库对象命名：

- **表名**：snake_case + 模块前缀（`appdeploy_application`, `capability_skill`, `ops_sop`, `coding_standard`）。
- **列名**：snake_case（`project_space_id`, `created_at`, `is_secret`, `risk_level`）。
- **索引**：`idx_表_列`（`idx_std_scope`）；唯一索引 `uq_表_列`（`uq_pginstance_ps_active`）。
- **保留字加引号**：`"user"`, `"order"`（PG 关键字冲突时双引号包裹）。', 100, TRUE, 'module', 'db'),
  ('std_module_db_2', NULL, '主键与外键', 'general', '主键与外键约定：

- **主键**：TEXT id，前缀 + uuid 短码（`app_xxx`, `ins_xxx`, `env_xxx`, `std_xxx`，用 `uuid.NewString()[:20]`）。
- **外键**：`REFERENCES 父表(id) ON DELETE CASCADE`（子表随父表删，如 appdeploy_instance 随 application）。
- **业务唯一**：`CREATE UNIQUE INDEX`（含 partial `WHERE` 子句过滤有效行，如 `WHERE status=''active''`）。
- **多租户列**：几乎所有业务表带 `project_space_id`，索引覆盖。', 110, TRUE, 'module', 'db'),
  ('std_module_db_3', NULL, '迁移机制', 'general', '迁移机制（internal/db/migrate.go）：

- **文件**：`migrations/pg/NNNNNN_name.up.sql` + `.down.sql`，6 位版本前缀（000001_init）。
- **embed 打包**：`//go:embed migrations/pg/*.sql`，编译进二进制。
- **执行顺序**：按 version 升序；`schema_migrations` 表记录已应用版本。
- **事务可执行**：每个 SQL 必须能在事务内跑（禁 `CREATE DATABASE` / `VACUUM` 等不能事务的语句）。
- **up/down 配对**：down 必须能完整回滚 up 的所有变更（DROP TABLE / DROP COLUMN）。
- **不修改已应用迁移**：新需求加新迁移文件，不回头改老文件（已应用的不会重跑）。', 120, TRUE, 'module', 'db'),
  ('std_module_db_4', NULL, '可空列处理', 'general', 'NULL 处理（PG 严格类型，NULL 扫 string 报错）：

- **查询 COALESCE**：可空文本列 `COALESCE(col, '''') AS col`（参见 appdeploy.appCols / insCols / envCols）。
- **Go struct**：可空字段用 `*string` / `*T` 指针；或在查询 COALESCE 后用 `string`（推荐后者，简化）。
- **写入**：可空列显式 `INSERT NULL` 或省略（让默认值生效）。
- **禁 SELECT \***：显式列名（列顺序脆弱，加列会错位；提取 `xxxXXCols` 常量复用）。', 130, TRUE, 'module', 'db'),
  ('std_module_db_5', NULL, '索引策略', 'general', '索引策略：

- **覆盖查询路径**：WHERE / ORDER BY / JOIN 的列建索引。
- **多租户索引**：`project_space_id` 几乎都要索引（按空间过滤是主查询路径）。
- **部分索引**：`WHERE status=''active''` 只索引有效行，省空间 + 提速（参见 uq_pginstance_ps_active）。
- **外键列建索引**：避免级联删除全表扫（FK 不自动建索引）。
- **复合索引顺序**：高选择性列在前；多租户场景 `(project_space_id, 其他列)`。', 140, TRUE, 'module', 'db'),
  ('std_module_code_1', NULL, '模块分层', 'general', '模块内分层（不跨层调用）：

- **handler.go**：HTTP 层，解析参数 + 调 store + httpx 返回；不直接写 SQL。
- **store.go**：数据访问，sqlx 查询；不碰 HTTP / gin.Context。
- **service.go**（可选）：业务逻辑，handler 与 store 间；复杂模块才拆。
- **model.go**：类型定义 + 领域模型（struct + 常量）。
- **不跨模块直接 new**：经 main 注入接口（如 `AppQuotaChecker`），避免循环依赖。', 100, TRUE, 'module', 'code'),
  ('std_module_code_2', NULL, '模块自包含装配', 'general', '模块自包含装配（cmd/server/main.go 解耦，8 人并行不冲突）：

- **每模块导出 `Register(r gin.IRouter, deps...)`**：内部 new handler + 注册路由。
- **main.go 只调 Register**：不 new 具体 handler（避免 main.go 成为合并冲突热点）。
- **跨模块依赖**：main 构造枢纽（`reqRepo`, `devAgent`, `appDeployHandler`），注入给需要者。
- **模块内小工具**（buildpack/naming 等）放模块包内，不外泄到 internal/ 公共区。', 110, TRUE, 'module', 'code'),
  ('std_module_code_3', NULL, '错误处理', 'general', 'Go 错误规范（与平台错误处理规范对齐）：

- **包装**：`fmt.Errorf("op name: %w", err)` 保留错误链。
- **哨兵**：`var ErrNotFound = errors.New(...)`；上层 `errors.Is` 分支判断。
- **store 层不吞错**：`sql.ErrNoRows` 向上抛；handler 层按需映射 404。
- **错误消息分层**：日志/error 面向开发者（英文 op 名 + err）；handler 返回的 message 面向用户（中文）。', 120, TRUE, 'module', 'code'),
  ('std_module_code_4', NULL, '命名约定', 'general', 'Go 命名约定（gofmt + 导出性）：

- **导出**：驼峰大写首字母（`Standard`, `NewStore`, `Register`）。
- **包内私有**：小写首字母（`standardSeedData`, `appCols`, `routeOps`）。
- **接口**：`-er` 后缀（`Reader`, `QuotaChecker`, `RouteWriter`, `EnvValueReader`）。
- **常量**：驼峰分组（`ScopePlatform`, `ModuleAPI`, `HeaderUserID`）。
- **包名**：短小写单数（`standard`, `appdeploy`, `pgsupply`）。', 130, TRUE, 'module', 'code'),
  ('std_module_code_5', NULL, '测试规范', 'testing', '测试规范（PG 而非 sqlite）：

- **集成测试**：`testutil.TestDB(t)` 连 anp_test（PG），`testutil.Truncate` 清表隔离。
- **禁 sqlite `:memory:`**：PG 类型严格，sqlite 漏类型 bug（参见 memory sqlite-test-pg-type-trap）。
- **串行执行**：`go test -p 1 ./...`（共享 anp_test 库，串行避免连接冲突）。
- **接口抽象**：依赖注入便于 fake（如 `checkFunc`, `AppQuotaChecker`，测试注入 mock）。
- **表驱动优先**：`cases := []struct{...}{...}`，遍历断言。', 140, TRUE, 'module', 'code'),
  ('std_module_form_1', NULL, '字段定义', 'general', '前端表单字段约定：

- 每个字段必须有：**label**（中文文案）+ **placeholder**（示例提示）+ **校验规则**。
- 必填字段前缀 `*`（视觉提示，红色星号）。
- 长文本用 `<textarea>`，短文本用 `<input>`；枚举用 `<select>` 或 radio。
- 字段名（name）与后端 JSON 字段对齐（snake_case）。', 100, TRUE, 'module', 'form'),
  ('std_module_form_2', NULL, '校验（前后端双校验）', 'security', '前后端双校验（绝不只信前端）：

- **前端**：提交前 client 校验（必填 / 格式 / 长度），失败阻断 + 友好提示。
- **后端**：handler 用 `validator/v10` `ShouldBindJSON` + `binding:"required"` 再校验（前端可被绕过）。
- **错误提示**：中文，可定位字段（「名称必填」而非 「invalid」）。
- **危险操作**（删除 / 发布 / 上线）必须 `confirm("确认...？")` 二次确认。', 110, TRUE, 'module', 'form'),
  ('std_module_form_3', NULL, '布局', 'general', '表单布局约定：

- **卡片分组**：相关字段一组（基本信息 / 配置 / 权限），用 `<div className="space-y-4">` 分隔。
- **提交按钮**：在底部；提交中显示 loading + 禁用（防重复提交）。
- **取消按钮**：明确返回路径，不依赖浏览器后退。
- **响应式**：移动端字段堆叠，桌面端可两列。', 120, TRUE, 'module', 'form'),
  ('std_module_ui_1', NULL, '组件约定', 'general', '前端组件约定：

- **自研 + Tailwind**：不引第三方 UI 库（保持视觉一致 + 包体精简 + 可控）。
- **共享组件**：放 `app/_components/`（或同层 `_components/`）。
- **Next.js App Router**：`app/` 目录 + `page.tsx`（不用旧 pages router）。
- **props 类型化**：用 TypeScript interface 定义 props，不透传 any。', 100, TRUE, 'module', 'ui'),
  ('std_module_ui_2', NULL, '样式约定', 'general', '样式约定（Tailwind 优先）：

- **Tailwind class 直接写 JSX**：不抽独立 CSS 文件（除全局少量）。
- **状态色用 map 常量**：`STATUS_COLOR`, `ACTION_COLOR`, `SCOPE_COLOR`（参见 governance/page.tsx），不内联三元。
- **卡片布局**：`<div className="space-y-4">` + 圆角 + 边框（`rounded-lg border p-4`）。
- **暗色模式**：用 Tailwind `dark:` 变体（可选，初期不强制）。', 110, TRUE, 'module', 'ui'),
  ('std_module_ui_3', NULL, 'API 调用', 'general', '前端 API 调用约定：

- **用 `lib/api.ts` 封装**：`apiGet` / `apiPost`（统一 baseURL + Envelope<T> 类型）。
- **installAuthInterceptor**：自动加 `Authorization: Bearer <token>`（登录后存 localStorage）。
- **Envelope<T>**：`{code, data: T, message?, trace_id?}`；`r.code !== 0` 显示 `r.message`（中文）。
- **不用裸 fetch**：每次重写鉴权 + 错误处理会漏边角，统一走封装。', 120, TRUE, 'module', 'ui'),
  ('std_module_ui_4', NULL, '客户端组件', 'general', '客户端组件约定：

- **顶部 `"use client"`**：用到 useState/useEffect/fetch/event handler 时标记。
- **不引重型依赖**：状态管理用 `useState` + prop drilling（不引 Redux/Zustand，初期够用）。
- **useEffect 依赖数组**：完整声明依赖（`[]` 仅挂载时跑；`[dep]` dep 变时跑），避免无限请求。
- **加载/错误态**：fetch 期间显示 loading；失败显示错误 + 重试按钮。', 130, TRUE, 'module', 'ui');

-- 3. 校验：各层条数
-- SELECT scope, COALESCE(NULLIF(module,''),'(none)') AS module, COUNT(*)
--   FROM coding_standard WHERE project_space_id IS NULL
--   GROUP BY scope, module ORDER BY scope, module;

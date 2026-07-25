# 智源 ANP 平台 · 日志与可观测体系设计（方案 C：完整体系）

- **日期**：2026-07-25
- **状态**：待评审
- **选定方案**：C（完整体系：排障 + 落盘 + 审计 + 全链路 + 智能体消费）
- **关联**：部署进度条/失败不可见 bug 修复（task #8，已验证）；为"配合开发智能体实现自动运维"铺路
- **交付形态**：分 5 个里程碑（M1–M5），每个独立可交付、可验证

---

## 1. 背景与动机

### 1.1 触发现象

部署 bug 排查中发现：应用 `hello-service` 构建失败时，前端只显示 `❌ 构建失败：exit status 1`，**真实的编译错误**（`response.go` 与 `main.go` 重复声明）躺在 `appdeploy_instance.build_log` 里，前端不展示，必须查数据库才能定位。这是"日志/错误信息未被正确收集与透传"的典型。

### 1.2 现状一句话

平台已经搭好了日志**地基**（zap + `platform_log` 表 + `DualLogger` + 前端错误上报 + 请求中间件），但**业务层没有接进水管**——handler 报错只 `httpx.Err` 返回前端、不打日志；4xx 不入库；构建详情不透到前端。

### 1.3 终极目标

**配合开发智能体实现系统自动运维与开发**。日志首先要让 AI 智能体能读、能据此定位与修复，其次才是给人看。这一目标决定了"结构化、全链路可串联、错误带可操作上下文"是硬需求而非锦上添花。

---

## 2. 设计原则：为智能体可消费（Agent-Consumable Observability）

所有设计决策服从以下 5 条原则：

1. **结构化（JSON）**：智能体能直接解析字段，而非正则猜文本。stdout/file/DB 三层在 prod 统一 JSON。
2. **全链路可串联**：每条日志/审计/DB 记录带 `trace_id`，智能体能一键拉出一个请求/操作的完整链路（前端 → 后端 → agent-runtime → 外部调用）。
3. **错误带可操作 context**：错误记录附 stack、关键入参、资源标识（`app_id`/`instance_id`/节点），让智能体拿到就能行动，而非"failed"。
4. **审计支持 `agent` 作为 actor**：智能体将来执行运维操作时，同样被审计记录（不只审计人）。
5. **提供查询 API**：智能体可按 `trace_id`/`module`/`level`/时间窗/关键词查日志，作为它的"感知器官"。

---

## 3. 现状基线（调研结论）

| 维度           | 现状                                                       | 缺口                                                 |
| -------------- | ---------------------------------------------------------- | ---------------------------------------------------- |
| 日志库         | zap（`internal/log/log.go:10` `New(level)`）               | 仅 level，无 format/output/file；业务 handler 用不到 |
| 分级           | zap 支持 5 级，调用规范                                    | 业务 handler **0 处**用 logger                       |
| 落盘           | 仅 stdout                                                  | 无文件、无滚动、无按级分文件                         |
| 结构化         | console 文本（key=value）                                  | 无 JSON 选项                                         |
| 业务报错入库   | `DualLogger` 仅 5xx 入库（`logsvc/middleware.go:21`）      | 4xx 全不入；handler 主动写 0 处                      |
| `httpx.Err`    | 273 处散落 28 个 handler                                   | 旁无日志；改一处即全局生效的改造点                   |
| 吞错           | 212 处 `_ =`                                               | deploy/route/收敛类失败被静默吞                      |
| 操作审计       | 仅 `security_audit`（scan）、`db_action_log`（SQL）        | 无通用 `operation_log`；CRUD/部署/发布/审批不可追溯  |
| 请求中间件     | Trace + RequestLogger + Recovery（`server/middleware.go`） | 不记 body；4xx 不入；无慢请求                        |
| 跨层日志       | `platform_log` 表 + `/logs` API + 前端管理页               | INFO/WARN 不入库；前端不展示构建 build_log           |
| 前端上报       | `error-report.ts` + `/api/v1/logs` 公开端点                | 无统一 logger；无埋点                                |
| Python runtime | ERROR 自动回传                                             | INFO/WARN 不入库                                     |
| 配置项         | 仅 `LOG_LEVEL`（`config.go:16`）                           | 无 format/output/rotation                            |

**关键洞察**：地基和管线已铺好，缺口集中在"业务层接入"。M1 工程量因此很小。

---

## 4. 总体架构

### 4.1 三层日志

```
                     ┌─────────────────────────────────────┐
   业务代码           │  log.L()  /  DualLogger.Log()        │  统一入口
  (handler/          │  (zap 字段 + 可选 DB 入库)            │
   service/store)    └──────────────┬──────────────────────┘
                                    │
            ┌───────────────────────┼───────────────────────┐
            ▼                       ▼                       ▼
    ① stdout 层              ② 文件层                 ③ DB 层
    zap encoder               lumberjack 滚动          platform_log 表
    console(M1) /             app.log + error.log      ERROR/FATAL(M1)
    json(M2)                  (M2)                     + WARN(M1) + INFO(M5)
                                                      智能体查询 API(M5)
```

- **stdout 层**：开发调试 + 容器日志收集（Docker stdout）。
- **文件层**（M2）：生产落盘兜底、事后取证、error.log 独立便于告警。
- **DB 屓**（`platform_log`，已有表）：业务事件 + 错误入库，智能体可查。

### 4.2 trace-id 全链路（M4 完整）

```
浏览器(生成 trace_id) ──X-Trace-Id──▶ 后端(透传+注入日志) ──▶ agent-runtime ──▶ 外部调用
                                          │
                                          ▼
                                   每条日志/审计/DB 记录都带 trace_id
```

已有 `server/middleware.go` 的 Trace 中间件生成/透传 `X-Trace-Id`。M4 把起点前移到前端生成，并向后延伸到 agent-runtime。

---

## 5. M1 · 排障优先（MVP，最先落地）

### 5.1 目标

所有业务报错分级落库、可查；构建/部署失败原因前端直接可见。**直接解决"hello-service 编译错误看不到"类痛点**。工程量小、风险低。

### 5.2 改动清单

#### 5.2.1 logger 全局化（`internal/log/log.go` + `cmd/server/main.go`）

- `log.go`：`New()` 末尾调 `zap.ReplaceGlobals(logger)`，使全局 `zap.L()` 可用。
- `main.go:67` 现有 `logger := zhlog.New(cfg.LogLevel)` 自然生效全局。
- **业务 handler 零改动**即可 `zap.L().Info/Warn/Error(...)`。
- 决策依据：zap 原生全局，比"逐个 handler 加 logger 字段"改动小一个数量级。

#### 5.2.2 `httpx.Err` 自动打日志（`internal/httpx/response.go`，**一处改全局生效**）

- 在 `httpx.Err`（`response.go:25`）内部加：
  ```go
  fields := []zap.Field{
      zap.String("trace_id", c.GetString("trace_id")),
      zap.String("user_id", c.GetString("user_id")),
      zap.Int("biz_code", bizCode),
      zap.Int("http_status", status),
      zap.String("path", c.Request.URL.Path),
  }
  if status >= 500 { zap.L().Error(msg, fields...) } else { zap.L().Warn(msg, fields...) }
  ```
- **收益**：273 处 `httpx.Err` 调用全部自动有结构化日志，无需逐个改 handler。这是 DRY 关键点。

#### 5.2.3 `DualLogger` 扩到 4xx 入库（`logsvc/middleware.go` + `logsvc/dual.go`）

- `middleware.go:21` `if status >= 500` 改为：
  ```go
  switch {
  case status >= 500: dl.Log(... Level:"ERROR" ...)
  case status >= 400: dl.Log(... Level:"WARN" ...)
  }
  ```
- `dual.go:58` 入库门槛从 `ERROR/FATAL` 放宽到 `WARN+`（`if level=="INFO" { return }`）。
- **收益**：4xx 业务失败（参数错、配额超限、变更闸门拦截）也进 `platform_log`，可查可统计。

#### 5.2.4 构建失败前端可见（`platform/frontend/app/applications/page.tsx`）

- `page.tsx:540` 失败横幅 `{a.status === "failed"}` 块内，把 `a.last_error?.slice(0,100)` 改为可展开：
  - 显示 `a.last_error` 摘要 + 一个「详情」开关，展开显示 `a.build_log`（完整构建日志，`<pre>` 等宽）。
- 后端 `store.go:20` `appCols` 已含 `build_log`，前端 `App` 类型已有 `build_log` 字段（`page.tsx:41`），**无需后端改动**。
- **收益**：用户/智能体直接在前端看到构建失败的具体编译错误，不用查库。

#### 5.2.5 关键路径吞错清理（`internal/appdeploy/handler.go`）

- 不动全部 212 处 `_ =`（多数非致命），只清理**部署相关关键失败**：
  - `handler.go:1011` `RemoveByPrefix`（清理容器失败）：失败时 `zap.L().Warn` 记录。
  - `handler.go:1063` `UpsertRoute` 失败：已写 `ins.LastError`，补一条 `zap.L().Warn`。
  - `handler.go` 其他 deploy 收敛分支的 `_ =` 评估补 WARN。
- **收益**：部署链路的失败不再静默，排障有据。

### 5.3 数据结构

复用 `platform_log`（migration `000012`），**M1 不新建表**。WARN 入库后，现有 `idx_log_level_time`、`idx_log_trace` 索引直接支持查询。

### 5.4 测试（TDD，每个改动先写失败测试）

- `internal/httpx/response_test.go`（新建）：`httpx.Err` 触发后，全局 logger 收到对应 level + trace_id 字段。
- `internal/logsvc/middleware_test.go`（补充）：4xx 响应 → `platform_log` 有一条 WARN；5xx → ERROR。
- `internal/logsvc/dual_test.go`（补充）：`Log(WARN)` 入库；`Log(INFO)` 不入库。
- 前端 `applications/page.tsx`：人工验证失败横幅展开 build_log（项目前端用 vitest，可补组件测试）。

### 5.5 验收

- 任意 handler 返回 4xx/5xx → `platform_log` 有对应 WARN/ERROR 记录，含 trace_id/path。
- 应用构建失败 → 前端失败横幅可展开看到完整 build_log。
- `GET /api/v1/logs?level=WARN` 能查到 4xx 记录。

---

## 6. M2 · 落盘 + JSON 格式

### 6.1 目标

生产环境日志落盘 + 滚动 + 按级分文件 + JSON 结构化输出。事后取证、外部采集、智能体解析。

### 6.2 改动清单

#### 6.2.1 `internal/log/log.go` 扩展

- `New(level string)` → `New(cfg Config)`，`Config` 含：`Level / Format(console|json) / Output(stdout|file) / File / ErrorFile / MaxSizeMB / MaxBackups / MaxAgeDays`。
- 用 `zapcore.NewTee` 组合多个 Core：
  - Core A：全级别 → stdout 或 app.log（按 `Format` 选 encoder）。
  - Core B：`ErrorLevel+` → error.log（独立，便于告警/快查）。
- 文件输出经 `lumberjack.Sinker`（`gopkg.in/natefinch/lumberjack.v2`）做滚动。
- 保留 `New` 旧签名做兼容包装（或一次性改调用点 `main.go:67`）。

#### 6.2.2 `internal/config/config.go` 加配置项

`Config` struct（`config.go:14`）新增字段，`Load()`（`config.go:30`）加默认值与环境变量绑定：

```go
LogFormat   string  // LOG_FORMAT  console|json，prod 默认 json
LogOutput   string  // LOG_OUTPUT  stdout|file，prod 默认 file
LogFile     string  // LOG_FILE    默认 /data/logs/app.log
LogErrorFile string // LOG_ERROR_FILE 默认 /data/logs/error.log
LogMaxSizeMB int    // LOG_MAX_SIZE_MB 默认 100
LogMaxBackups int   // LOG_MAX_BACKUPS 默认 7
LogMaxAgeDays int   // LOG_MAX_AGE_DAYS 默认 30
```

#### 6.2.3 `deploy/Dockerfile.backend` + `docker-compose.prod.yml`

- 镜像内建 `/data/logs` 目录（或挂载 `../data/logs:/data/logs`，`docker-compose.prod.yml` backend volumes 加一行）。
- `deploy/.env.prod.example` 加 `LOG_FORMAT=json` `LOG_OUTPUT=file` 等示例。

### 6.3 依赖

- 新增 `gopkg.in/natefinch/lumberjack.v2`（`go.mod`）。

### 6.4 测试

- `internal/log/log_test.go`（新建）：`Format=json` 输出含 `"level":"error"` JSON 行；`Output=file` 写到临时文件；error.log 只含 ERROR+；滚动触发新文件。
- 配置测试 `internal/config/config_test.go`（已有）补 log 配置默认值。

### 6.5 验收

- prod 启动后 `/data/logs/app.log` 持续写入 JSON 行，`error.log` 只含 ERROR/FATAL。
- 日志按 MaxSizeMB 滚动，旧文件按 MaxBackups/MaxAgeDays 清理。

---

## 7. M3 · 操作审计

### 7.1 目标

关键操作（谁在何时对什么做了什么）可追溯；为智能体运维铺路（agent 作为 actor 也被审计）。

### 7.2 新表 `operation_log`（migration `000015_operation_log.up.sql`）

```sql
CREATE TABLE IF NOT EXISTS operation_log (
  id              BIGSERIAL PRIMARY KEY,
  timestamp       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  actor_type      TEXT NOT NULL,    -- user / agent / system
  actor_id        TEXT NOT NULL,    -- 用户 id / agent id / "system"
  action          TEXT NOT NULL,    -- app.deploy / app.delete / change.approve / release.create / quota.update / config.set / auth.login ...
  resource_type   TEXT,             -- app / change / release / requirement / project_space ...
  resource_id     TEXT,
  project_space_id TEXT,
  trace_id        TEXT,
  detail          JSONB,            -- 入参摘要、结果、版本号等
  status          TEXT NOT NULL,    -- success / failed
  error           TEXT
);
CREATE INDEX idx_oplog_time ON operation_log(timestamp DESC);
CREATE INDEX idx_oplog_actor ON operation_log(actor_type, actor_id, timestamp DESC);
CREATE INDEX idx_oplog_action ON operation_log(action, timestamp DESC);
CREATE INDEX idx_oplog_resource ON operation_log(resource_type, resource_id);
```

### 7.3 审计中间件（`internal/audit/middleware.go` 新包）

- 路由**白名单**标注需审计的路由（按 `method+path` 模式或 handler 显式注册）。
- 中间件在 `c.Next()` 后写一条 `operation_log`，从 context 取 `actor_id`（=user_id 或 agent 标识）、`trace_id`、`project_space_id`。
- 白名单初版覆盖：
  - `POST /apps/:aid/deploy` / `/promote` / `/deploy-commit`（部署）
  - `DELETE /apps/:aid`（删除应用）
  - `POST /changes/:id/approve|reject`（变更审批）
  - `POST /releases`（发布上线）
  - `PUT /quota` / `PUT /config/:key`（配额/配置变更）
  - `POST /auth/login`（登录）

### 7.4 agent 作为 actor

- agent-runtime 或未来智能体经认证调用 API 时，token 携带 `actor_type=agent`，中间件据此填 `actor_type`。
- 现有 auth 中间件（`internal/auth`）扩展：识别 agent token 类型，注入 `c.Set("actor_type","agent")`。

### 7.5 查询 API

- `GET /api/v1/operation-logs?actor=&action=&resource=&since=&limit=`（`internal/audit/handler.go`）。
- 前端管理页可后置（M5 统一可观测面板时做）。

### 7.6 测试

- `internal/audit/middleware_test.go`：白名单路由触发 → `operation_log` 一条，字段正确；非白名单不记。
- `internal/audit/store_test.go`：Query 各筛选维度。

### 7.7 验收

- admin 部署一个应用 → `operation_log` 有 `actor_type=user, action=app.deploy, resource_id=<app>` 一条。
- 智能体调用部署 → `actor_type=agent`。

---

## 8. M4 · 全链路 trace + JSON 贯穿

### 8.1 前端生成 trace_id（`platform/frontend/lib/api.ts`）

- `api.ts` 的 fetch 拦截（已有 `reportApiFailure`）里，请求头注入 `X-Trace-Id`（页面首次加载生成一个，存 sessionStorage，每次请求带）。
- 后端 `server/middleware.go` Trace 中间件已能透传客户端带来的 trace_id（需确认优先用请求头而非新生成）。

### 8.2 JSON encoder 贯穿

- 依赖 M2 的 `LogFormat=json`，prod 统一 JSON 输出。
- `DualLogger.Log`（`dual.go`）入库的 `context` JSONB 字段与 zap 字段保持一致 schema，确保 stdout/file/DB 三层可对齐。

### 8.3 agent-runtime 透传 trace_id（`platform/agent-runtime/agent_runtime/main.py`）

- FastAPI 中间件读请求头 `X-Trace-Id`，注入日志上下文，回传后端日志时带 `trace_id`。
- 后端调 agent-runtime 时（`AGENT_RUNTIME_URL`）请求头带 trace_id（`internal/dev` 或相关调用处补 header）。

### 8.4 测试

- 前端：请求头含 `X-Trace-Id`，同一会话多次请求 trace_id 一致。
- 后端：带 `X-Trace-Id` 的请求，日志/DB 记录用客户端的 trace_id。
- agent-runtime：回传日志含来源 trace_id。

### 8.5 验收

- 一个前端操作 → 后端日志 + agent-runtime 日志 + `platform_log` + `operation_log` **同一个 trace_id**，可一键串联全链路。

---

## 9. M5 · 智能体查询 API + 前端统一

### 9.1 查询 API 增强（`internal/logsvc/handler.go` + `store.go`）

- 新增 `GET /api/v1/logs/query`，参数：`trace_id / module / level / source / since / until / q（message 关键词）/ limit / offset`。
- `store.go:89` `Query` 扩展支持上述筛选（动态拼 WHERE，复用现有 `itoo` 模式）。
- 该端点对 agent 开放（智能体的"日志感知器官"）。

### 9.2 前端统一 logger（`platform/frontend/lib/logger.ts` 新建）

- 封装 `error-report.ts`，提供 `logger.info/warn/error(event, fields)`，统一带 trace_id、批量缓冲、`keepalive` 上报。
- 替换散落的 `reportError/reportWarn/reportApiError` 直调。

### 9.3 关键行为埋点

- 前端关键交互（提交需求、触发部署、审批）记 INFO 级事件，回传后端入 `platform_log`（M1 放宽后 INFO 仍不入库，M5 选择性入或单独埋点表——**决策点：复用 platform_log 存 INFO 还是新建 events 表，倾向复用，加 `source=frontend` 区分**）。

### 9.4 可观测面板（可选，前端 `app/admin/`）

- 整合现有 `/admin/logs` + 新增 `/admin/operations`，统一看错误趋势、操作审计、trace 链路展开。

### 9.5 测试

- `internal/logsvc/handler_test.go`（补充）：`/logs/query?trace_id=x` 返回该 trace 全部记录；`q=keyword` 做 message 模糊匹配。
- 前端 `logger.ts` 单测：批量缓冲、trace_id 注入。

### 9.6 验收

- 智能体调 `/logs/query?trace_id=<某失败部署>` → 返回该部署的完整日志链（HTTP 请求 + build 失败 ERROR + 审计记录），据此可定位"hello-service response.go 重复声明"。

---

## 10. 关键决策（已与用户确认，按推荐）

| 决策点           | 选定                                   | 理由                                                    |
| ---------------- | -------------------------------------- | ------------------------------------------------------- |
| logger 注入方式  | **全局 `zap.L()`**（`ReplaceGlobals`） | 业务 handler 零改动即可用；比逐个加字段省一个数量级     |
| 审计触发         | **中间件 + 路由白名单自动**            | 业务 handler 无感；显式调用易漏                         |
| 交付节奏         | **M1 先单独落地，再续 M2–M5**          | M1 改动小、立即可用（含构建失败可见）；后续按里程碑迭代 |
| `httpx.Err` 改造 | **改一处内部打日志，全局 273 处生效**  | DRY；避免逐 handler 改                                  |
| 4xx 入库         | **放宽 DualLogger 入库门槛到 WARN+**   | 业务失败可查可统计                                      |
| 前端 trace_id    | **sessionStorage 存，请求头带**        | 同一会话可串联；无需后端改动                            |

---

## 11. 里程碑交付计划

| 里程碑                 | 范围        | 主要产出                                                                  | 可独立验证                        |
| ---------------------- | ----------- | ------------------------------------------------------------------------- | --------------------------------- |
| **M1** 排障优先        | 5.2.1–5.2.5 | logger 全局 + httpx.Err 自动日志 + 4xx 入库 + 构建失败前端可见 + 吞错清理 | ✅ 所有报错可查、构建错误前端可见 |
| **M2** 落盘+JSON       | 6.2         | log.go 扩展 + config 配置项 + lumberjack + Dockerfile 卷                  | ✅ prod 日志落盘 JSON             |
| **M3** 操作审计        | 7           | operation_log 表 + 审计中间件 + agent actor                               | ✅ 关键操作可追溯                 |
| **M4** 全链路 trace    | 8           | 前端 trace_id + JSON 贯穿 + agent-runtime 透传                            | ✅ 一键串联全链路                 |
| **M5** 智能体查询+前端 | 9           | /logs/query + 前端 logger + 埋点                                          | ✅ 智能体可消费                   |

**顺序**：M1 → M2 → M3 → M4 → M5（M1 必须最先；M2/M3 可并行；M4 依赖 M2 的 JSON；M5 依赖 M1/M3/M4）。

每个里程碑完成后：scp 推送 .28 + `docker-compose up --build -d backend`（或 frontend）重建 + 验证。

---

## 12. 风险与回滚

| 风险                                           | 缓解                                                                                                                 |
| ---------------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| M1 放宽 4xx 入库 → `platform_log` 膨胀         | 已有 `idx_log_level_time`；M2 后补定期清理（保留 N 天 WARN）；高基数路径（如探活）排除                               |
| 全局 logger 改动影响启动                       | `New()` 失败兜底 `zap.NewNop()`（现有行为），不 panic                                                                |
| `httpx.Err` 自动打日志的性能（每请求一次 zap） | zap 结构化日志极快（μs 级）；可异步                                                                                  |
| 审计白名单遗漏关键操作                         | 代码评审 + M3 验收对照"关键操作清单"                                                                                 |
| lumberjack 滚动与容器日志采集冲突              | stdout 仍保留（Tee 双写），文件仅兜底                                                                                |
| 回滚                                           | 每个里程碑独立 commit；.28 部署有 `handler.go.bak` 式备份机制；代码层 `git checkout`（仓库转 git 后）或重新 scp 旧版 |

---

## 13. 验收标准（整体）

1. **排障**：任意业务失败，前端或 `/logs` 能直接看到带 trace_id 的结构化原因（不再"只看到 failed / exit status 1"）。
2. **落盘**：prod `/data/logs/app.log`（JSON）+ `error.log` 持续滚动。
3. **审计**：部署/删除/审批/发布/配额/登录等操作在 `operation_log` 可追溯，含 actor（含 agent）。
4. **全链路**：一次前端操作的 trace_id 贯穿前端→后端→runtime→DB 全部记录。
5. **智能体可消费**：智能体调 `/logs/query` + `/operation-logs` 能拿到定位问题所需的全部结构化信息，据此可自动定位（如"hello-service response.go 重复声明"）。

---

## 附录 A：M1 涉及文件清单（实现计划输入）

| 文件                                         | 改动类型                             |
| -------------------------------------------- | ------------------------------------ |
| `backend/internal/log/log.go`                | 改：`New` 末尾 `ReplaceGlobals`      |
| `backend/cmd/server/main.go`                 | 无需改（`New` 已调用，全局自动生效） |
| `backend/internal/httpx/response.go`         | 改：`Err` 内打日志                   |
| `backend/internal/httpx/response_test.go`    | 新建：测试 `Err` 打日志              |
| `backend/internal/logsvc/middleware.go`      | 改：4xx 入库                         |
| `backend/internal/logsvc/dual.go`            | 改：入库门槛 WARN+                   |
| `backend/internal/logsvc/middleware_test.go` | 补：4xx/5xx 入库测试                 |
| `backend/internal/logsvc/dual_test.go`       | 补：WARN 入库测试                    |
| `backend/internal/appdeploy/handler.go`      | 改：关键 `_ =` 补 WARN（5.2.5）      |
| `frontend/app/applications/page.tsx`         | 改：失败横幅展开 build_log（5.2.4）  |

## 附录 B：不做什么（YAGNI）

- 不引入 ELK/Loki 等外部日志系统（当前规模 stdout+文件+DB 三层够用；后续量大再加）。
- 不接入 Sentry/Datadog（自建上报已覆盖核心需求）。
- 不做实时日志流推送（SSE/WebSocket）——M5 的查询 API 已满足智能体需求；流式留待真实需求出现。
- 不重构所有 212 处 `_ =`（只清关键路径，其余渐进）。
- 不新建 events 表（前端埋点复用 `platform_log`，`source=frontend` 区分）。

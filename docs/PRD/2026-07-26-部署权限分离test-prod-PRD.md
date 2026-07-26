# PRD/设计：部署权限分离（dev 可部署 test 不可 prod）

| 版本 | 日期       | 作者     | 状态   |
| ---- | ---------- | -------- | ------ |
| v1.0 | 2026-07-26 | ANP 团队 | 待评审 |

> 现状摸底见本轮对话三份调查报告。本 spec 是「工作台三需求」分三份中的**第 1 份**（顺序 ①部署权限 → ③多AI工具 → ②绩效记录）。

## 1. 背景与目标

**现状安全漏洞**：appdeploy 部署路由（`/deploy` `/promote` `/deploy-commit` `/stop` `/start` `DELETE /apps`）在 `auth/guard.go` 的 `routeOps` **完全未登记**，`AutoRequire` 对未登记操作默认放行（`permission.go:24-40` `Allowed` 未命中返回 true）→ **任何登录用户（含 dev、business）都能 `POST /deploy env=prod` 直上线生产**。前端 `applications/page.tsx:289-295` 的 🚀上线按钮还绕过 `/promote` 的变更闸门直调 `/deploy env=prod`。

**目标**：

- dev 角色可自主部署/管理 **test** 环境（deploy / deploy-commit / stop / start / 查看日志）
- **prod** 上线仅 gatekeeper/admin，且必须经过变更审批闸门（`HasApproved`）
- 堵住前端绕过 `/promote` 的漏洞

## 2. 现状（已取证）

- ✅ **数据层 test/prod 隔离已就绪**：`appdeploy_instance.env` + `UNIQUE(app_id, env)`（`migrations/pg/000001_init.up.sql:390-405`）+ 端口段分离（test 9100-9199 / prod 9200-9300，`deployer.go:14-20`）。一个应用可同时有 1 个 test 实例 + 1 个 prod 实例。
- ❌ **RBAC 层完全空白**：`guard.go:14-58` 的 `routeOps` 无任何部署路由；`permission.go:24-40` `Allowed` 对未登记操作默认 true。
- ❌ **前端绕过**：`applications/page.tsx:289-295` 🚀上线调 `/deploy env=prod` 而非 `/promote`，绕过 `Promote` 的变更闸门（`handler.go:951-960`）。
- ✅ `release.create` 已排除 dev（gatekeeper/admin），方向一致。
- ⚠️ `Stop`/`Start`/`Logs` 硬编码 `EnvProd`（`handler.go:1151,1179,1250`），不接 env 参数。

## 3. 设计

### 3.1 RBAC 操作拆分（拆操作名方案）

扩展 `permission.go` 的 `OpRoles` 矩阵，新增 env 感知操作：

| 操作                               | 允许角色                              |
| ---------------------------------- | ------------------------------------- |
| `app.deploy.test`                  | dev, gatekeeper, admin                |
| `app.deploy.prod`                  | gatekeeper, admin                     |
| `app.deploy-commit.test`           | dev, gatekeeper, admin                |
| `app.deploy-commit.prod`           | gatekeeper, admin                     |
| `app.stop.test` / `app.start.test` | dev, gatekeeper, admin                |
| `app.stop.prod` / `app.start.prod` | gatekeeper, admin                     |
| `app.delete`                       | admin（收紧；原任何人可删）           |
| `app.promote`                      | gatekeeper, admin（不变，带变更闸门） |

> business 角色不在任何部署操作内（业务方不参与部署）。

### 3.2 鉴权执行（handler 内判 env）

- `guard.go` `AutoRequire`：把查到的 roles 注入 gin context（`c.Set("roles", strings.Join(roles, ","))`），供 handler 读取。
- 部署类路由（`deploy`/`promote`/`deploy-commit`/`stop`/`start`/`DELETE /apps`）登记到 `routeOps` 为 **env 敏感占位操作**（如 `"app.deploy"`）。`AutoRequire` 见到占位操作时不直接 `Allowed` 判定，仅注入 roles 后放行。
- 各 handler 内读 body `env` + context `roles` → `Allowed("app.deploy."+env, roles)`。
- `Stop`/`Start` 改为接受 `env` 参数（当前硬编码 `EnvProd`），按 env 分发到 `.test`/`.prod` 操作。

### 3.3 prod 通道收敛（堵绕过漏洞）

- **后端**：`Deploy` handler 对 `env==EnvProd` 强制 `app.deploy.prod` 权限 **+ 变更闸门 `HasApproved`**（复用 `Promote:951-960` 逻辑，未审批的变更不允许上 prod）。即使前端或 curl 直调 `/deploy env=prod` 也被拦。
- **前端**：`applications/page.tsx` 的 🚀上线按钮从直调 `/deploy env=prod`（`:289-295`）改为走 `/promote`（带变更闸门）。

### 3.4 dev 的 test 权限边界

dev 可：`deploy` test、`deploy-commit` test、`stop`/`start` test、查看 test 日志（`Stats env=test`）。
dev 不可：任何 prod 操作、`DELETE /apps`、`/promote`。

## 4. 验收标准

| 编号 | 验收点                                                                          |
| ---- | ------------------------------------------------------------------------------- |
| AC1  | dev 调 `POST /deploy env=test` → 200；`env=prod` → 403                          |
| AC2  | dev 调 `stop`/`start` 带 `env=test` → 200；`env=prod` → 403                     |
| AC3  | dev 调 `DELETE /apps/:aid` → 403（仅 admin 200）                                |
| AC4  | gatekeeper/admin 调 `deploy env=prod` → 变更未审批则拒（403/412），已审批则 200 |
| AC5  | 前端 🚀上线按钮走 `/promote`（抓包确认不再调 `/deploy env=prod`）               |
| AC6  | business 角色调任何部署操作 → 403                                               |

## 5. 改动清单（file:line）

**后端**

- `internal/auth/permission.go:13-21` — `OpRoles` 扩展（加 `app.deploy.{test,prod}` / `app.deploy-commit.{test,prod}` / `app.stop.{test,prod}` / `app.start.{test,prod}` / `app.delete`）。
- `internal/auth/guard.go:14-58` — `routeOps` 登记 deploy/promote/deploy-commit/stop/start/DELETE 为 env 敏感占位；`:79-107` `AutoRequire` 注入 roles + 占位操作放行（不直接判）。
- `internal/appdeploy/handler.go:910-929` `Deploy`（按 env 鉴权 + prod 变更闸门）；`:943-964` `Promote`（补鉴权）；`DeployCommit`（按 env 鉴权）；`:1151,1179,1250` `Stop`/`Start`（接受 env 参数 + 按 env 鉴权）；`Delete`（admin 鉴权）。
- 单测：`internal/auth/permission_test.go`（矩阵：dev/gatekeeper/admin/business × 各操作）、`internal/appdeploy/handler_test.go`（env 鉴权 + prod 闸门）。

**前端**

- `platform/frontend/app/applications/page.tsx:277-300` — `promoteWithNode` 改走 `/promote`；`:636-646` — 按钮/参数配套；stop/start 调用带 `env`。

## 6. 风险与边界

- `AutoRequire` 改动（注入 roles）影响所有鉴权路由 → 充分回归现有 RBAC 单测。
- `Stop`/`Start` 加 `env` 参数是接口变更 → 前端配套，旧调用默认按 prod（保持向后兼容）。
- 现有"任何登录用户能部署"被收紧，可能影响已习惯的用户 → 上线前通知；admin 仍全权。
- `deploy_node`（多机）与 env 维度正交，不影响本设计。

## 7. 关联

- 摸底报告：本轮对话「部署权限与 test/prod 现状」。
- 与 ③（多AI工具）/ ②（绩效记录）独立；但 ② 的「身份统一 user.id」利好本需求的部署审计 actor 字段。

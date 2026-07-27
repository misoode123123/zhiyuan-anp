# Deploy env=prod delivered 对称前置 实现记录

- 日期:2026-07-27
- 模块:`platform/backend/internal/appdeploy`(Deploy handler)
- 上游设计:`docs/superpowers/specs/2026-07-27-promote-delivered前置-design.md` §3(选 A,原标「本次不做、留作后续」)
- 上游 PRD:`docs/PRD/2026-07-26-主线闭环收敛-PRD.md`(AC7)
- 关联已闭环:`docs/superpowers/plans/2026-07-27-promote-delivered前置.md`(Promote AC7,已合并 a811091)

## 1. 为什么现在做

AC7(Promote delivered 前置)已闭环,堵住了「跳过 release 直接 `/promote` 上线 prod」的绕过。但 spec §3 当时明确留了一个对称缺口:

> `Deploy`(`handler.go:958-966`)的 prod 分支同样有变更闸门(防 `/deploy env=prod` 绕过 `/promote`),但**无 delivered 检查**。`Deploy env=prod` 若不同步,理论上仍存在「approved 未 delivered 直接部署 prod」的绕过面。**spec review 已确认本次不补(选 A),留作后续**;若补,改法与 Promote 完全对称(同方法、同位置插入)。

即:即便 `/promote` 被拦,用户仍可走 `/deploy env=prod`(若持有 `app.deploy.prod` 权限)绕过 delivered 检查直接部署 prod。本记录补这道对称前置,消除该绕过面。

## 2. 改动(与 Promote AC7 完全对称)

### 2.1 `appdeploy/handler.go` Deploy prod 闸门块(line 957-966)

现状:

```go
if env == EnvProd && h.changes != nil {
    if hasAny, _ := h.changes.HasAny(c.Request.Context(), aid); hasAny {
        if ok, _ := h.changes.HasApproved(c.Request.Context(), aid); !ok {
            httpx.Err(c, 409, 40920, "需先登记变更并审批通过才能上线 prod（变更闸门）")
            return
        }
        _ = h.changes.MarkReleased(c.Request.Context(), aid)
    }
}
```

改为(在 `HasApproved` 通过分支内、`MarkReleased` 前插入 AC7 检查):

```go
if env == EnvProd && h.changes != nil {
    if hasAny, _ := h.changes.HasAny(c.Request.Context(), aid); hasAny {
        if ok, _ := h.changes.HasApproved(c.Request.Context(), aid); !ok {
            httpx.Err(c, 409, 40920, "需先登记变更并审批通过才能上线 prod（变更闸门）")
            return
        }
        // 🚪 AC7 delivered 前置（对称 Promote，防 /deploy env=prod 绕过 /promote）：
        // approved 变更关联的需求须已 delivered（即已走 release/merge 发布）。
        // 查不到需求时放行（grandfather，对称 release 回写）。
        if h.reqRepo != nil {
            if undelivered, _ := h.reqRepo.HasUnDeliveredApprovedByApp(c.Request.Context(), aid); undelivered {
                httpx.Err(c, 409, 40921, "来源需求未交付，请先在发布中心发布上线后再部署 prod")
                return
            }
        }
        _ = h.changes.MarkReleased(c.Request.Context(), aid)
    }
}
```

- 复用 `requirement.HasUnDeliveredApprovedByApp`(AC7 已实现,无需新方法)。
- 文案「再部署 prod」对应 Deploy 语义(Promote 是「再 promote」)。
- 错误码同 `409 / 40921`(与 Promote 一致,前端走同一文案通道)。
- `h.reqRepo != nil` 守卫:装配确认 `main.go` 已注入 reqRepo(与 Promote 同一 Handler 实例),生产生效;nil 时保守跳过(grandfather)。
- 无 schema 迁移、无前端改动、无新方法。

### 2.2 测试(`handler_http_test.go`)

复用 AC7 的 `newHTTPHandlerWithGates` fixture(已注入 changes+reqRepo)。新增 3 个 Deploy env=prod 拒绝路径测试,请求 `/deploy` 带 `{"env":"prod"}`:

| 用例                            | seed                                 | 期望                           |
| ------------------------------- | ------------------------------------ | ------------------------------ |
| 非 admin                        | roles=[] inline                      | `403`(`app.deploy.prod` 拒绝)  |
| 登记变更未审批                  | app+change(pending)                  | `409/40920`(变更闸门)          |
| **approved 但需求未 delivered** | app+req(developing)+change(approved) | **`409/40921`(对称 AC7 核心)** |

HTTP 层只测拒绝路径(早返回,不触发 `go buildAndDeploy`);通过语义由 Promote AC7 的 repository 数据层 `false` 场景已覆盖,Deploy 复用同一方法。

## 3. 验证

- 单测:`go test ./internal/appdeploy/ -run TestHandler_Deploy -v` 全过 + 全量 `go test -p 1 ./...` 回归。
- .28 dogfood:approved 未 delivered → `/deploy env=prod` 拦 409/40921;走完 release(delivered)后 → 通过。

## 4. 收尾

合并后回填 spec §3 状态:「选 A 已补,Deploy env=prod delivered 对称前置闭环」。

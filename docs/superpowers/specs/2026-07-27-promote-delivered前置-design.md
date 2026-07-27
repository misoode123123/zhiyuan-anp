# 设计:Promote 上线前的「来源需求 delivered」前置（AC7）

- 日期:2026-07-27
- 模块:`platform/backend/internal/appdeploy`(Promote handler)、`platform/backend/internal/requirement`(Repository 新方法)
- 状态:待审核
- 上游 PRD:`docs/PRD/2026-07-26-主线闭环收敛-PRD.md`(AC7)

## 1. 背景与目标

主线闭环收敛后,发布链路为:

```
dispatch-code → 编码 → 测试 → 审批(变更 approved) → release 发布(需求 delivered + 部署 test) → promote 上线 prod
```

`release.Create`(`release/handler.go:120`)会追溯 `change.source_id` 把来源需求标记 `delivered`——`delivered` 是「已走完发布」的落点。

**现状 `Promote`(`appdeploy/handler.go:988-1018`)的前置**:

1. `404` 应用不存在
2. `403` 部署权限分离(`app.deploy.prod`,需 gatekeeper/admin)
3. `409` 变更闸门:`HasAny` + `HasApproved`(查「变更是否 approved」)
4. 通过后 `MarkReleased` → 异步部署

**漏洞**:变更 `approved` 是审批环节的状态,`delivered` 是发布之后才设的需求状态,二者不同档。现有变更闸门只验 approved,**不验 delivered**——意味着变更一审批通过就可直接 `/promote`,跳过 `release` 发布环节即上线 prod。

**AC7**(`/promote` 对未 delivered 的来源需求 → 拒绝并提示先发布)正是要堵这个绕过。

**目标**:在 `Promote` 变更闸门之后、`MarkReleased` 之前,加一道「approved 变更关联的需求须已 delivered」前置;并对全路径补单测(变更闸门 409 / AC7 409 等此前零覆盖)。

## 2. 范围与验收标准(AC)

- **AC1(实现)**:`Promote` 在变更闸门通过后、`MarkReleased` 前,调 `requirement.Repository.HasUnDeliveredApprovedByApp(appID)`;返回 `true`(有 approved 变更且其来源需求 `status≠delivered`)→ `409 / 40921`,提示「来源需求未交付,请先在发布中心发布上线后再 promote」。
- **AC2(grandfather)**:未登记过变更的 app(`HasAny=false`)不受约束,直接放行——与变更闸门 grandfather 一致。
- **AC3(查不到需求放行)**:approved 变更的 `source_id` 解析不到需求(旧 `source_id=appID` 路径)时放行,对称于 `release.Create` 回写的诚实原则(解析 0 行不报错)。SQL 层由 `JOIN requirement r ON r.id=c.source_id` 自然实现:JOIN 不到 → `r.status` 为 NULL → 不命中 `<>delivered`。
- **AC4(单测-HTTP)**:`Promote` 拒绝路径全覆盖:`403`(非 admin)、`409/40920`(登记变更未审批)、`409/40921`(approved 但需求未 delivered)、`404`(已有)。
- **AC5(单测-Repository)**:`HasUnDeliveredApprovedByApp` 数据层全覆盖:approved+未delivered→`true`;approved+delivered→`false`;无 approved→`false`;`source_id=appID` 旧路径→`false`(grandfather 对称);双路径(`source_id=reqID` 经 `requirement.application_id`)命中。
- **AC6(端到端)**:`.28` dogfood:一条 approved 但未 released 的变更 → `/promote` 被拦 409;走完 release(需求 delivered)后 → `/promote` 通过。留 .28 验(本机不跑功能测试,见 memory `deploy-28-no-local-test`)。

## 3. 非目标(YAGNI)

- **`Deploy env=prod` 的 delivered 对称前置**(✅ **2026-07-27 已补**,见 `docs/superpowers/plans/2026-07-27-deploy-prod-delivered前置.md`):`Deploy`(`handler.go:958-966`)的 prod 分支同样有变更闸门(防 `/deploy env=prod` 绕过 `/promote`),原**无 delivered 检查**。AC7 当时只覆盖 `/promote`(spec review 选 A,留作后续);现已对称补上——`Deploy env=prod` 在 `HasApproved` 通过后、`MarkReleased` 前同样调 `HasUnDeliveredApprovedByApp`,命中 `409/40921`,堵 `/deploy env=prod` 绕过 `/promote` 的面。改法与 Promote 完全对称(同方法、同位置插入)。.28 dogfood(app_f60c1add,approved+未 delivered)已验 `/deploy env=prod` → `409/40921`;通过路径(delivered→放行)复用同一方法,由 repository 层 `delivered→false` 单测覆盖,不做生产端到端(与 AC7 遗留项 2 同处理)。
- 不改 `release` / `merge` 的 delivered 写入路径(已是 AC7 的对偶正确实现)。
- 不改前端(`/promote` 按钮的 409 文案展示走通用错误码通道,无需改)。
- 不重构 `Promote` 异步部署段(`go buildAndDeploy`)——测试回避该路径(见 §6 取舍)。

## 4. 数据模型与反查路径(复用,不改表)

AC7 是 `release.Create` delivered 回写的**读取端对偶**,复用同一条反查链:

```
change_request.source_id  ──(AC1 后 = reqID;旧路径 = appID)──→  requirement.status
requirement.application_id = appID
```

`change/store.go:40` 的 `appSourceCond` 已是「从 appID 找它所有变更」的双路径条件:

```sql
(source_id = $1  -- source_id 直接是 appID
 OR source_id IN (SELECT id FROM requirement WHERE application_id = $1))  -- source_id 是该 app 的需求
```

无 schema 迁移、无新列、无新表。

## 5. 关键决策

| #   | 决策       | 取值                                                                                                                                                | 依据                                      |
| --- | ---------- | --------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------- |
| 1   | 检查范围   | 只约束 `HasAny=true` 的 app                                                                                                                         | 与变更闸门 grandfather 一致               |
| 2   | 拒绝条件   | 有 approved 变更 + 其 source_id 解析到的需求 `status≠delivered`;**多个 approved 变更取并集,任一来源需求未 delivered 即拒绝**(SQL `EXISTS` 天然语义) | AC7 字面                                  |
| 3   | 叠加位置   | 变更闸门(HasApproved)通过**后**、`MarkReleased` **前**                                                                                              | approved 是前提,delivered 是 release 结果 |
| 4   | 查不到需求 | 放行                                                                                                                                                | 对称 release 回写诚实原则;保护老数据      |
| 5   | 错误码     | `409 / 40921`(区别变更闸门 `40920`)                                                                                                                 | 前端可区分文案                            |
| 6   | 方法归属   | `requirement.Repository`(非 `change.Store`)                                                                                                         | delivered 是需求属性,放 repository 更内聚 |

## 6. 实现方案

### 6.1 `requirement/repository.go` 新增方法

```go
// HasUnDeliveredApprovedByApp 该 app 是否存在「approved 变更但来源需求未 delivered」的情形。
// promote 闸门(AC7)据此拒绝「跳过 release 直接上线」。
//
// SQL 要点:
//   - JOIN requirement r ON r.id=c.source_id:source_id 为旧 appID 路径时 JOIN 不到(r=NULL),
//     r.status<>delivered 不命中 → 天然 grandfather(对称 release 回写)。
//   - appSourceCond 双路径:source_id=appID 或 source_id=该 app 的需求。
func (r *Repository) HasUnDeliveredApprovedByApp(ctx context.Context, appID string) (bool, error) {
    var exists bool
    const q = `SELECT EXISTS (
        SELECT 1 FROM change_request c
        JOIN requirement r ON r.id = c.source_id
        WHERE (c.source_id = $1 OR c.source_id IN (SELECT id FROM requirement WHERE application_id = $1))
          AND c.status = 'approved'
          AND r.status <> 'delivered')`
    err := r.db.GetContext(ctx, &exists, q, appID)
    return exists, err
}
```

### 6.2 `appdeploy/handler.go` Promote 插入 AC7 前置

在现有变更闸门块内、`MarkReleased` 之前插入(`handler.go:1005-1014`):

```go
if h.changes != nil {
    if hasAny, _ := h.changes.HasAny(c.Request.Context(), aid); hasAny {
        if ok, _ := h.changes.HasApproved(c.Request.Context(), aid); !ok {
            httpx.Err(c, 409, 40920, "需先登记变更并审批通过才能上线 prod（变更闸门）")
            return
        }
        // 🚪 AC7 delivered 前置（PRD 2026-07-26 主线闭环收敛 AC7）：
        // approved 变更关联的需求须已 delivered（即已走 release/merge 发布），
        // 堵「变更一审批就 promote、跳过发布」的绕过。查不到需求时放行（grandfather，对称 release 回写）。
        if h.reqRepo != nil {
            if undelivered, _ := h.reqRepo.HasUnDeliveredApprovedByApp(c.Request.Context(), aid); undelivered {
                httpx.Err(c, 409, 40921, "来源需求未交付，请先在发布中心发布上线后再 promote")
                return
            }
        }
        _ = h.changes.MarkReleased(c.Request.Context(), aid)
    }
}
```

> 位置不变动现有 404/403/40920/MarkReleased 任何分支,仅在 HasApproved 通过的分支内追加一道检查。`HasAny=false` 的 grandfather app 天然跳过整块。

### 6.3 测试 fixture 注入 reqRepo

`appdeploy/handler_http_test.go` 的 `newHTTPHandler` 现把 reqRepo 传 nil(第 6 个 nil):

```go
// 现状
h := NewHandler(store, NewDeployer("test"), nil, nil, nil, nil, nil, nil, nil, nil)
```

改为注入真实 Repository(db 已在手):

```go
reqRepo := requirement.NewRepository(db)
h := NewHandler(store, NewDeployer("test"), nil, nil, nil, reqRepo, nil, nil, nil, nil)
```

(同步处理 `newHTTPHandlerWithExtRoute` 内同款构造。)

## 7. 测试矩阵

### 7.1 `requirement/repository_test.go`(新方法数据层)

| 用例                         | seed                                                                | 期望                      |
| ---------------------------- | ------------------------------------------------------------------- | ------------------------- |
| approved + 需求 developing   | app+req(application_id=app)+change(source_id=req,approved)          | `true`                    |
| approved + 需求 delivered    | 同上但 req.status=delivered                                         | `false`                   |
| 无 approved(仅 pending)      | change(source_id=req,pending)                                       | `false`                   |
| source_id=旧 appID(approved) | change(source_id=app,approved),无 req                               | `false`(grandfather 对称) |
| 双路径命中                   | req.application_id=app + change(source_id=req,approved,未delivered) | `true`                    |
| 跨 app 隔离                  | change 属 app_b,查 app_a                                            | `false`                   |

### 7.2 `appdeploy/handler_http_test.go`(HTTP 拒绝路径)

| 用例                            | seed                                 | 期望                                          |
| ------------------------------- | ------------------------------------ | --------------------------------------------- |
| 非 admin                        | roles=[] 注入                        | `403`                                         |
| 登记变更未审批                  | app+change(pending)                  | `409/40920`                                   |
| **approved 但需求未 delivered** | app+req(developing)+change(approved) | **`409/40921`(AC7 核心)**                     |
| app 不存在                      | —                                    | `404`(已有 `TestHandler_Promote_appNotFound`) |

**测试取舍**:HTTP 层只测拒绝路径(早返回,不触发 `go buildAndDeploy` 异步 docker,干净可跑,与现有 `TestHandler_Promote_appNotFound` 同模式);「通过」语义由 repository 层 `false` 场景覆盖。200 通过路径的端到端留 .28 dogfood(AC6)。

## 8. 影响面

- 后端:`internal/requirement/repository.go`(+1 方法)、`internal/appdeploy/handler.go`(Promote +~6 行)、`internal/appdeploy/handler_http_test.go`(fixture 注入 + 3 用例)、`internal/requirement/repository_test.go`(+1 方法用例)。
- 装配确认:`main.go:216` `appdeploy.Register(..., reqRepo, ...)` 已注入 reqRepo → 线上 `h.reqRepo!=nil`,AC7 生产生效。`reqRepo==nil` 时(仅测试遗漏注入)保守跳过,等同 grandfather。
- 无 schema 迁移、无前端改动、无配置开关。
- 现网影响:已走完 release(需求 delivered)的应用 promote 不受影响;「approved 未 delivered」的应用 promote 将被新拦——这正是预期,提示先发布。

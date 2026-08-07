# 需求派发：从「AI 全自动编码」改为「指派开发人员 + 人工工作台协同开发」

- 日期：2026-08-07
- 状态：待评审
- 作者：方案设计（brainstorming 产出）
- 关联代码：`platform/backend/internal/requirement`、`internal/dev`、`internal/codews`；`platform/frontend/app/requirements`、`app/workspace`

## 1. 背景

当前系统存在**两条相互独立的编码路径**，但前端入口只默认接了「自动」那条：

| 路径                                                                 | 入口                          | 谁编码              | 过程                                  |
| -------------------------------------------------------------------- | ----------------------------- | ------------------- | ------------------------------------- |
| ① 自动编码（codetask + `opencode --auto`）                           | `/requirements`「⚡派发编码」 | AI 全自动一锤子写完 | 人不参与，事后在 `/approvals` 看 diff |
| ② 交互编码（codews：opencode/claude web UI，iframe 嵌 `/workspace`） | 应用卡片「编码」              | 人在工具 UI 里主导  | 实时可见、可介入、可纠偏              |

**根因**：`/requirements` 的「⚡派发编码」调用 `POST /dispatch-code` → `service.Dispatch` → `UpdateStatus("developing")` + `coder.Submit(...)` 立即起 goroutine 跑 `opencode run --auto`（`requirement/service.go:194-195`、`dev/coding.go:172`）。它和人工工作台 `/workspace`（已具备「人主导 + 子任务级按需调 AI + 实时可介入」能力，见 `app/workspace/workspace-frame.tsx` 的 `dispatchReq`/`breakdownReq`/`submitReq`）**没有接通**；认领后也只丢一句「去编码工作台」的文字、不给入口。

> 结论：**能力已就位，断在入口和默认路径。** 用户诉求 = 把「派发」从启动路径①扭成导向路径②，并铺好未来自动化。

## 2. 目标 / 非目标

**目标**

- `/requirements` 的「派发」语义从「启动 AI 全自动编码」改为「指派给开发人员 + 进入人工工作台」。
- 派发时**指定一名开发人员**；该人员在工作台从「派给我的」需求里自己选着开始、协同 AI 开发，过程可见可控。
- AI 全自动编码能力（`coder.Submit`）**完整保留**，作为「未来自动化」的底座。

**非目标（本期不做）**

- 不新增/不细分需求状态枚举（不引入 `awaiting_dev` 等），保持最小改动。
- 不下线 `/dev` 手动编码、`adapt` 系统适配等 `coder.Submit` 的既有调用方。
- 不做「子任务自动连跑」等自动化（留作未来）。

## 3. 方案概述（方案 B + 派发指定人员）

重定义 `service.Dispatch`：保留「确定应用」逻辑、**去掉 `coder.Submit`**、**新增「指派开发人员」**、返回 `appID` 供前端跳转人工工作台。自动路径保留备用。

**新流程**：

```
发起需求(AI生成规格) → specified
   ── 派发(指定开发人员 + EnsureApp) → developing（assignee=被指派人）
       ── 开发人员在 /workspace 从「派给我的」选需求 → 协同 AI 开发（按需调 AI、实时可介入）
           ── 提交核对 → 合并 → delivered
```

## 4. 详细设计

### 4.1 状态机（最小改动，不新增枚举）

| 状态         | 含义                                           | 进入条件                            |
| ------------ | ---------------------------------------------- | ----------------------------------- |
| `specified`  | AI 已生成规格，未派发                          | 创建需求                            |
| `developing` | 已派发进入开发通道（含「待开始」与「开发中」） | 派发（设 `assignee` + `EnsureApp`） |
| `delivered`  | 已交付                                         | 工作台合并                          |

- **派发动作 = `repo.Assign(assignee)` + `EnsureApp`（确定/兜底创建托管应用并绑定）+ `UpdateStatus("developing")`**，**不再 `coder.Submit`**。
- `developing` 语义扩展为「已派发进入开发通道」；用 `assignee` 区分负责人，用工作台会话状态区分是否已开始。本期不细分「待开始/开发中」（YAGNI）。
- 看板聚合 `MyTasks.myDev`（`assignee==user && status=="developing"`）/ `TeamTasks.inDev`（`assignee!="" && developing`）逻辑不变，自然展示「派给我的 / 全队开发中」。

### 4.2 后端：重定义 `service.Dispatch`（`requirement/service.go:164`）

- **保留**：读需求、`EnsureAppForRequirement`（未归属应用则兜底创建托管应用 + 绑定，`service.go:177-190`）以拿到 `appID`；**不再需要 `repo_dir`**（不再编码，应用位置仅供未来自动化时确定）。
- **新增入参**：`assignee string`（派发指定的开发人员 user_id）。
- **去掉入参**：`userID`、`repoDir`、`model`（不再 `coder.Submit`，无需绩效归属传参 / 外部仓库 / 模型）。
- **新增动作**：`repo.Assign(ctx, reqID, assignee)`（复用现有认领写入；指派语义）。
- **去掉**：`s.coder.Submit(...)`（`service.go:195`）。
- **保留**：`UpdateStatus(ctx, reqID, "developing")`（`service.go:194`）。
- **改签名**：`(*codetask.Task, error)` → `(appID string, err error)`。

> `service.Dispatch` 仅被 `requirement/handler.go:301`（`dispatch-code`）一处调用（已 grep 确认），改它不影响 `/dev`、`adapt` 等其他 `coder.Submit` 调用方。

### 4.3 后端：`handler.DispatchCode`（`requirement/handler.go:293`）

- `dispatchRequest` 改为 `{ assignee }`（required）：移除已无用的 `RepoDir`、`Model`（不再编码、应用内部确定）。
- 调 `svc.Dispatch(ctx, psID, rid, assignee)`，不再透传 `model`。
- 去掉 `ErrActiveTaskConflict` 分支（不再 `Submit`，无并发冲突）。
- 返回体：`{requirement_id, app_id, workspace_url}`，其中 `workspace_url = /workspace?app={appID}&ps={psID}&req={rid}`。
- swagger 注释（`@Router /dispatch-code`、`@Param body`）同步更新；改 handler 须 `swag init` regen。

### 4.4 前端：`/requirements` 派发（`app/requirements/page.tsx:136` `dispatch()`）

- 派发前**选开发人员**：新增成员选择控件（数据源见 §6 开放点 ②）。
- `dispatch()` body 改为 `{ assignee }`；拿到 `workspace_url` 后：
  - 若被指派人 == 当前用户 → 直接跳转 `/workspace?app&ps&req=rid`；
  - 否则 → 提示「已派发给 {assignee}，其可在「研发工作台/我的任务」认领开发」，不强制跳转。
- 按钮文案：「⚡ 派发编码」→「👤 派发给开发」；提示语去掉「AI 后台实现→去审批」，改为「已派发，待开发人员在工作台协同 AI 开发」。

### 4.5 前端：`/workspace` 支持 `req` 参数直达（`app/workspace/workspace-frame.tsx`）

- 从 URL 读 `req`；若有 → 自动 `setSelectedReq(req)` + 触发认领与 boot（复用现有 `onStartReq` 逻辑，约 `workspace-frame.tsx:495`）。
- 效果：派发跳转后直达该需求工作台，自动起 opencode，无需手选。
- 幂等处理（开放点 ④）：被指派人首次进入时 `/assign` 对「已 assignee=本人」应幂等放行，不报 409。

### 4.6 前端：「派给我的需求」入口

- 复用 `MyTasks.myDev`（`assignee==我 && developing`）。在 `/requirements` 列表或 `/dev` 为这些需求加「💻 进编码工作台」按钮 → 跳 `/workspace?app=该需求应用&ps&req=rid`。
- 让被指派的开发人员能一键从「派给我的」进入对应应用的工作台。

## 5. 保留项（未来自动化底座，本期不动）

- `dev.CodingAgent.Submit` / `run` / `opencodeRun`（`dev/coding.go`）：`/dev` 手动起编码、`adapt` 仍用。
- `codetask/store`、`/dev` 研发工作台、`/approvals` 变更审批：原样保留。
- `/requirements` 的「👤 认领」按钮（自助占坑场景）：保留。

## 6. 开放设计点（建议，待评审定）

1. **已指派需求再次派发**：建议允许覆盖指派（派发是 lead/管理员动作）；或返回 409 提示「已派发给 X，先释放再派」。倾向覆盖。
2. **选人 UI 数据源**：需「项目空间可指派成员」列表。确认是否已有接口可复用（`auth.Store` 有用户/角色数据）；若无，加一个轻量列表接口或在前端复用已有用户数据。
3. **被指派人未归属应用的展示**：派发时 `EnsureApp` 已兜底创建应用，故需求必有 `application_id`，「进工作台」URL 的 `app` 参数恒可用。
4. **`/assign` 幂等**：`workspace-frame` 进入已指派给本人的需求时，`/assign` 应幂等放行，不触发 409。需确认 `repo.Assign` 对「已 assignee=同人」的行为并按需放宽。

## 7. 错误处理

| 场景                              | 处理                                      |
| --------------------------------- | ----------------------------------------- |
| `assignee` 为空                   | 400「请指派开发人员」                     |
| 需求不存在                        | 400（保留）                               |
| `EnsureAppForRequirement` 失败    | 500「为需求兜底创建托管应用失败」（保留） |
| 被指派人进入工作台 `/assign` 冲突 | 幂等放行（开放点 ④）                      |

## 8. 测试计划

**后端**

- `requirement/service_test.go:168` 改：`Dispatch` 不再返回 task、不再调 `coder.Submit`；断言返回 `appID`、`repo.Assign` 被调用、状态转 `developing`。
- 新增：`assignee` 为空报错；需求不存在报错；`EnsureApp` 兜底创建路径。
- 新增 `handler` 测试：返回 `workspace_url` 含正确 `app/ps/req`；`assignee` 透传。

**前端**

- `/requirements`：选人 → 派发 → 跳转/提示分支。
- `/workspace`：带 `req` 参数自动选中 + 认领 + boot。
- 「派给我的需求」入口跳转。

**回归**

- `MyTasks`/`TeamTasks` 看板：`developing + assignee` 聚合仍正确。
- `/dev` 手动编码、`/approvals` 审批链不受影响。

## 9. 未来自动化（展望，本期不做）

落地后，「自动化」有两条渐进路径，都以本人工流程为底座：

- **工作台内自动化**：把 `dispatchReq` 的「按子任务派 AI、做完停」改为自动连跑（去掉「停」），人从主导者退为监督者。
- **接回自动路径**：把保留的 `coder.Submit`（`opencode --auto`）以新入口（如工作台内「一键 AI 自动实现整个需求」）接回，作为人工模式的加速档。

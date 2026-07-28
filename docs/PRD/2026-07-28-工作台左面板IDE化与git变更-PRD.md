# PRD：编码工作台左面板 IDE 化与 git 变更可见

| 版本 | 日期       | 作者     | 状态        |
| ---- | ---------- | -------- | ----------- |
| v0.1 | 2026-07-28 | ANP 团队 | 草案·待评审 |

> 所有根因均在源码取证（文件:行号），非推断。设计决策经与用户 brainstorming 确认：布局方案 A（活动栏+视图）、diff 用右侧浮层抽屉、面板内只提交不部署、活动栏 4 图标。

## 1. 背景

用户反馈编码工作台（`/workspace`）两个问题：

1. **看不到项目需求列表及 git 变更列表** —— 希望在工作台一眼看到「这个项目要做什么需求、git 改了什么」。
2. **列表显示太难看** —— 希望模拟主流 IDE（VSCode/JetBrains）的显示风格。

经取证：需求列表其实已渲染但被文件结构压在下方、样式拥挤；**git 变更列表则根本没接**（后端有数据，前端 `WorkspaceDetail` 类型未含 `commits` 字段，从未展示）。

## 2. 现状与根因（取证）

### 2.1 git 变更「看不到」—— 前端类型缺字段，后端数据被丢弃

- **后端已返回 commits**：`detail.go:15-16` `AppDetail.Commits []CommitInfo`，`detail.go:86` `d.Commits, _ = Log(ctx, a.RepoDir, 10)` 取最近 10 条 git log。`handler.go:152-159` `Detail` 直接 `httpx.OK(c, d)` 返回。
- **前端没接**：`context-drawer.tsx:31-40` `WorkspaceDetail` 类型只有 `application/requirements/changes/releases`，**无 `commits`**；`workspace-frame.tsx:77-102` `fetchDetail` 也只 set 这四项。→ 后端返回的 `commits` 被前端整体丢弃，这就是「看不到 git 变更」根因。
- **变更记录 ≠ git 变更**：现有「变更(N)」section（`context-drawer.tsx:164`）渲染的是 `change_request` 审批记录（`detail.go:70-76`），不是 git 文件改动/提交历史，易混淆。

### 2.2 git 变更数据能力不足——只有计数和 log，无文件级列表/单文件 diff

- `repo.go:90` `CountUncommitted` 只数未提交文件**个数**（`git status --porcelain` 行数），不返回文件路径/状态。
- `repo.go:133` `Log` 返回 `{SHA, Message, Date}`（`CommitInfo`，`repo.go:172-176`），**无作者、无该次提交改了哪些文件**。
- `repo.go:160` `Diff` 返回 `git log -p` 原始文本（多次提交混在一起），无法按单文件/单提交查看。
- **无「工作区单文件 diff」能力**：没有 `git diff HEAD -- <path>` 的等价函数，点某个改动文件看不到行级差异。

### 2.3 编码在 worktree，工作区改动须查 worktree 而非主仓

- `handler.go:1277` `wt := filepath.Join(a.RepoDir, ".worktrees", sanitizeID(user))` —— 编码发生在开发者 `dev-<user>` worktree。
- `handler.go:1279` 部署时已用 `CountUncommitted(ctx, wt)` 检测该 worktree 未提交数。
- `detail.go:86` 的 `Log(a.RepoDir, 10)` 查的是**主仓 main 分支**历史，不含 `dev-<user>` 未合并的提交 → 工作台该看 `dev-<user>` 分支历史，须改查 worktree 目录。
- 用户来源：后端 `c.GetString(auth.CtxUserID)`（同 `Workspace`/`Deploy`），前端 `api.ts` `currentUser()`。

### 2.4 需求列表「看不到」—— 被文件结构压在下方 + 样式拥挤

- `context-drawer.tsx:89-91` 顶部先渲染「📁 文件结构」（`ProjectDocs`），需求 section 在其后（`context-drawer.tsx:94`），抽屉宽仅 `w-64`（256px），emoji 圆点 + 挤文字 → 需求被挤、不显眼。
- `context-drawer.tsx:226-233` `Section`/`Empty` 用 `text-xs`、emoji 状态点（`STATUS_DOT`，`context-drawer.tsx:43-52`），无 IDE 视觉语言（状态色条、文件状态字母、行高、hover）。

## 3. 设计方案

### 3.1 总体布局：活动栏 + 单视图（方案 A，已确认）

左面板分两层：

```
┌──┬───────────────────────┐
│活│  侧边栏（当前视图）      │
│动│  标题栏  [操作按钮]      │
│栏│  ─────────────────     │
│  │  树/列表内容            │
│46│                        │
│px│                        │
└──┴───────────────────────┘
```

- **活动栏**（46px，深色 `#2c2c2c`）：4 个图标竖排，选中项左侧蓝色竖条 `#007acc`，右上角红点角标（badge）显示未读数。
- **侧边栏**（`#f3f3f3` 浅灰，宽度可拖拽，默认 280px）：只显示当前选中视图。
- **4 个视图**（不单独加「审批」图标，待审批变更收在源代码管理视图底部）：

| 图标 | 视图       | 数据来源                                                          |
| ---- | ---------- | ----------------------------------------------------------------- |
| 📋   | 需求       | `/detail` requirements                                            |
| 🔀   | 源代码管理 | `/git-status`（工作区改动+提交历史）+ `/detail` changes（待审批） |
| 🚀   | 发布       | `/detail` releases                                                |
| 📁   | 文件       | `/repo-docs` + `/repo-file`（复用 `ProjectDocs`）                 |

### 3.2 源代码管理视图（核心，解决 git 变更可见）

三个折叠 section + 底部提交框：

1. **工作区改动**（来自 `git-status` 接口的 `changes[]`）：文件行 `状态字母 + 路径 + 增删行数`。状态字母配色：`M`黄`#d29922` / `A`绿`#2da44e` / `D`红`#cf222e` / `U`紫`#8250df`。**点文件 → 右侧 diff 抽屉**（3.3）。
2. **提交历史**（`commits[]`）：`SHA(等宽灰) + 说明 + 相对时间 + 作者`。点 SHA 展开「该次提交改了哪些文件」，再点文件看 diff。
3. **待审批变更**（复用 `changes[]` 中 `status=pending`）：通过/拒绝按钮（复用现有 `onApprove/onReject`）。
4. **提交框**：输入框（留空走 AI 总结）+「提交」按钮 → `POST /commit`。**只提交不部署**，部署仍走顶部工具栏（`workspace-toolbar.tsx:52` 构建部署，已含 `need_commit`/`auto_commit` 处理）。

**worktree 不存在兜底**：未认领需求时 `git-status` 返回 `worktree_exists=false`，源代码管理视图显示空态「请先认领需求以创建 `dev-<user>` 工作区」+ 跳需求视图。

### 3.3 diff 浮层抽屉（已确认，替代内联展开）

- 从侧边栏右缘滑出，覆盖 opencode iframe 左部，**默认 480px、可拖拽调宽**，看完可关。
- 内容：文件名标题栏 + 行级 diff（红删 `#ffebe9` / 绿增 `#dafbe1` / 上下文灰），等宽字体，行号。
- 不用内联展开的理由：侧边栏仅 280px，行级代码 diff 塞进去横向滚动严重、读不清，反而重陷「太难看」。

### 3.4 需求视图（解决需求被挤看不到）

独占面板：状态色条（delivered 绿/pending 黄/developing 蓝/draft 灰）+ 优先级标签（P0 红/P1 蓝/P2 灰）+ 标题。选中需求展开用户故事/验收标准 + 操作按钮行（🤖AI编码 / 🧪自动测试 / 📋拆子任务 / 🔒提交核对），复用现有 `dispatchReq/runAutoTest/breakdownReq/submitReq`（`workspace-frame.tsx:265-427`）。

### 3.5 IDE 视觉语言规范

- 充足行高（`leading-relaxed`）、hover 浅灰 `#e8e8e8`、选中蓝底 `#d6ebff`。
- 状态用左侧 3px 竖色条 + 右侧字母/标签，**弃用 emoji 圆点**。
- 等宽字体用于 SHA、文件路径、diff。
- 角标 badge：需求视图显示「待认领数」、源代码管理显示「工作区改动文件数」。

## 4. 后端改动

| 改动                 | 文件                                             | 内容                                                                                                                                                                                                                                                                                                                                |
| -------------------- | ------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 工作区文件级改动     | `repo.go`（新增 `StatusFiles`）                  | `func StatusFiles(ctx, repoDir string) ([]FileChange, error)`：`git -c core.quotepath=false status --porcelain`，解析每行 `XY 路径`，返回 `{Path, Status}`（Status 映射 M/A/D/U/R）。复用 `CountUncommitted` 的 porcelain 思路但返回文件级。                                                                                        |
| 单文件 diff          | `repo.go`（新增 `FileDiff`）                     | `func FileDiff(ctx, repoDir, path, sha string) (string, error)`：`sha==""` → `git diff HEAD -- <path>`（工作区 vs HEAD）；`sha!=""` → `git diff <sha>^..<sha> -- <path>`（首提交无父时降级 `git show <sha> -- <path>`）。返回 unified diff 原始文本，空则 ""。                                                                      |
| 某次提交改了哪些文件 | `repo.go`（新增 `CommitFiles`）                  | `func CommitFiles(ctx, repoDir, sha string) ([]FileChange, error)`：`git diff-tree --no-commit-id --name-status -r <sha>`，解析 `XY 路径` 复用 `FileChange{Path,Status}`。                                                                                                                                                          |
| 提交历史加作者       | `repo.go:133` `Log` / `repo.go:172` `CommitInfo` | `CommitInfo` 加 `Author string`；`--pretty` 改 `%h                                                                                                                                                                                                                                                                                  | %an | %s  | %ci`，`SplitN` 改 4 段。向后兼容（Detail 调用不变）。 |
| 仅提交接口           | `handler.go`（新增 `Commit` handler）            | `POST /project-spaces/:id/apps/:aid/commit`，body `{message?}`：worktree=`a.RepoDir/.worktrees/sanitizeID(user)`（同 `handler.go:1277`）；message 空 → `commitMessageFor(ctx, wt, apiKey)`（复用 `handler.go:364`）；调 `Commit(ctx, wt, message)`（`repo.go:76`）。返回新 `Log(wt,1)[0]`。                                         |
| git 聚合查询接口     | `handler.go`（新增 `GitStatus` handler）         | `GET /project-spaces/:id/apps/:aid/git-status`：user=`c.GetString(auth.CtxUserID)`；wt=`a.RepoDir/.worktrees/sanitizeID(user)`；`os.Stat(wt)` 不存在 → `{worktree_exists:false, branch:"", changes:[], commits:[]}`；存在 → `{worktree_exists:true, branch:"dev-"+sanitizeID(user), changes:StatusFiles(wt), commits:Log(wt,20)}`。 |
| 单文件 diff 接口     | `handler.go`（新增 `FileDiff` handler）          | `GET /project-spaces/:id/apps/:aid/file-diff?path=&sha=`：wt 同上；`FileDiff(wt, path, c.Query("sha"))`；限返回前 2000 行（超长截断 + 标注）。返回 `{path, sha, diff, truncated}`。sha 省略查工作区 diff，给 sha 查该提交对该文件的 diff。                                                                                          |
| 某次提交文件列表接口 | `handler.go`（新增 `CommitFiles` handler）       | `GET /project-spaces/:id/apps/:aid/commit-files?sha=`：wt 同上；`CommitFiles(wt, sha)`。返回 `{sha, files:[]FileChange}`。供提交历史点 SHA 展开用。                                                                                                                                                                                 |
| 路由注册             | `handler.go:87` `Register`                       | 加 `r.GET(".../git-status", h.GitStatus)`、`r.GET(".../file-diff", h.FileDiff)`、`r.GET(".../commit-files", h.CommitFiles)`、`r.POST(".../commit", h.Commit)`。                                                                                                                                                                     |

**不改 `Detail`/`AppDetail`**：git 变更走独立 `git-status` 接口（编码时轮询），需求/变更/发布仍走 `/detail`，职责分离，避免 detail 每次都跑 git。

## 5. 前端改动

| 改动           | 文件                                                           | 内容                                                                                                                                                                                                                 |
| -------------- | -------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 侧边栏重构     | `workspace/sidebar.tsx`（新，替代 `context-drawer.tsx`）       | 活动栏 + 视图切换 state（持久化到 `localStorage` key `anp.workspace.view`）+ 渲染当前视图。`workspace-frame.tsx:572` 改渲染 `<Sidebar>`，传 detail/git 数据与回调。                                                  |
| 活动栏         | `workspace/activity-bar.tsx`（新）                             | 4 图标 + badge，active 高亮。                                                                                                                                                                                        |
| 需求视图       | `workspace/views/requirements-view.tsx`（新）                  | IDE 风格需求列表，复用现有展开/认领/操作按钮逻辑（从 `context-drawer.tsx:94-163` 迁移）。                                                                                                                            |
| 源代码管理视图 | `workspace/views/source-control-view.tsx`（新）                | 工作区改动 + 提交历史 + 待审批 + 提交框。调 `git-status`；点工作区文件调 `file-diff`（无 sha）打开 DiffDrawer；点提交 SHA 调 `commit-files?sha=` 展开该提交改了哪些文件，再点文件调 `file-diff?path=&sha=` 看 diff。 |
| 发布视图       | `workspace/views/releases-view.tsx`（新）                      | 复用 `detail.releases`，IDE 风格重排。                                                                                                                                                                               |
| 文件视图       | `workspace/views/files-view.tsx`（新）                         | 包 `ProjectDocs`（`project-docs.tsx`，不改）。                                                                                                                                                                       |
| diff 抽屉      | `workspace/diff-drawer.tsx`（新）                              | 右侧浮层，480px 可拖拽；按行着色 unified diff（首字符 `+`绿/`-`红/` `上下文/`@@` 标记灰）。                                                                                                                          |
| git 数据轮询   | `workspace-frame.tsx`                                          | 新增 `fetchGitStatus`（调 `git-status`），进工作台拉一次 + 每 10s 轮询；提交后/部署轮询命中 running 时刷新一次。                                                                                                     |
| 类型定义       | `workspace/types.ts`（新，从 `context-drawer.tsx:10-40` 抽出） | `WorkspaceDetail`（加可选 `commits` 备用）+ `FileChange{path,status}` + `GitStatus{worktree_exists,branch,changes,commits}` + `CommitInfo{sha,message,date,author}`。                                                |
| 旧组件清理     | `context-drawer.tsx`                                           | 删除或保留为薄壳转发到新 `Sidebar`（避免外部引用断裂）。                                                                                                                                                             |

## 6. 数据流

```
detail 接口 ──→ 需求/变更/发布（静态，进工作台拉一次 + 部署轮询刷新）
git-status 接口 ──→ 工作区改动 + 提交历史（10s 轮询）
file-diff 接口 ──→ 点工作区文件 / 展开后点 commit 文件时按需拉（带可选 sha）
commit-files 接口 ──→ 点提交 SHA 展开该提交改了哪些文件时按需拉
commit 接口 ──→ 提交按钮（提交后刷新 git-status）
```

## 7. 验证计划

按记忆规范，本机可跑前端 UI 静态效果，功能端到端验在 **.28 生产**（scp 重建 backend+frontend）：

1. **本地 UI**：`pnpm dev` 打开 `/workspace?app=&ps=`，确认活动栏 4 图标、视图切换、需求/发布/文件视图 IDE 风格、diff 抽屉滑出。
2. **.28 后端**：admin 认领一个需求 → worktree 生成 → 调 `git-status` 返回 `worktree_exists:true` + `branch=dev-admin` + `changes`/`commits` 真实数据；在 opencode 改两文件 → 10s 后 `changes` 出现 2 条；点文件 → `file-diff` 返回行级 diff。
3. **.28 提交**：源代码管理视图留空说明点「提交」→ AI 总结 → worktree `git log` 多一条 → `changes` 清空。
4. **.28 兜底**：未认领需求的应用进工作台 → 源代码管理视图显示空态引导。
5. **.28 中文路径**：含中文文件名的 repo → `changes` 路径正常显示（`core.quotepath=false` 生效）。

## 8. 风险与边界

- **worktree 不存在**：`git-status` 返回 `worktree_exists:false`，源代码管理视图空态引导认领，不报错。
- **大 diff**：`file-diff` 截断前 2000 行 + `truncated` 标注，避免长 diff 拖垮前端。
- **`--porcelain` 解析**：处理重命名（`R`）、未跟踪（`??`→`U`）、二进制（diff 标注 `Binary files`）；`core.quotepath=false` 防中文路径被引号转义。
- **轮询频率**：10s 一次 `git status`，编码时无感；离开工作台或抽屉关 → 不停轮询（worktree 不变，无害）。
- **提交 vs 部署职责**：面板「提交」只 `git commit`；部署仍走顶部工具栏（其 `need_commit`/`auto_commit` 逻辑保留，`workspace-frame.tsx:179-189` 不动）。
- **dev-<user> 历史 vs 主仓**：提交历史查 worktree（`dev-<user>` 分支，含未合并提交）；发布视图仍用 `detail.releases`（上线记录），不混。
- **向后兼容**：`CommitInfo` 加 `Author` 字段不破坏现有调用；`Detail`/`AppDetail` 不改。

## 9. 关联文档

- 现有 PRD：`docs/PRD/2026-07-26-工作台四问题修复-PRD.md`、`docs/PRD/2026-07-26-主线闭环收敛-PRD.md`、`docs/PRD/2026-07-18-变更上线关联显示-PRD.md`
- 实现记录（实现后补）：`docs/bugs/2026-07-28-工作台左面板IDE化.md`

# 开发向导（应用卡片内进度条）设计

- 日期: 2026-07-16
- 状态: 已批准（用户确认形态：卡片内嵌进度条）

## 背景
用户反馈：opencode 编码工作台打开后只看到一个聊天界面、看不到项目上下文；且「编码→测试→上线」业务流程不明确，不知现在该做哪步、上一步结果如何。

## 目标
让开发者在「应用部署」页每个应用卡片上一眼看到：当前开发到哪步、下一步该做什么、项目上下文（仓库/最近提交/test·prod 状态）。把割裂的编码(opencode)→测试→上线串成连贯引导。

## 方案：卡片内嵌进度条
应用卡片内、现有按钮上方，加「✏编码 → 🧪测试 → 🚀上线」横向进度条 + 上下文行 + 引导文案。整合现有按钮，不加页/路由/API/新按钮。

### 步骤状态判定（复用 detail API 已有数据）
- 编码 ✅：`detail.commits` 非空（仓库有提交）
- 测试 ✅：`detail.instances` 存在 env=test 且 status=running
- 上线 ✅：`detail.instances` 存在 env=prod 且 status=running

### 当前步 + 引导文案（纯函数 `devStep(commits, instances)`）
| 条件 | 当前步 | 引导文案 |
|---|---|---|
| 无 commit | 编码 | 先打开编码工作台写代码 |
| 有 commit、test 未 running | 测试 | 代码已就绪，构建部署 test 验证 |
| test running、prod 未 running | 上线 | test 验证 OK 就上线 |
| prod running | （全绿） | 已上线 ✅，可继续编码迭代 |

### 上下文行
仓库路径 `/data/repos/<名>` · 最近 commit `commits[0].message` · test URL/状态 · prod URL/状态

## 改动范围
- **前端** `applications/page.tsx`：新增 `DevWizard` 子组件 + `devStep(commits, instances)` 纯函数（带单测）。复用已加载的 `detail` 数据。
- **后端**：不动（`detail` 已返回 commits + instances）。
- 不加页/路由/API/按钮。

## 测试
- `devStep` 纯函数单测（4 种状态组合 → 正确当前步 + 文案）。
- 前端 tsc 通过。

## YAGNI（先不做）
- 不做文件树预览（需新增后端 API，重）。
- 不把「变更审批/发布」塞进步骤条（那是发布中心的事）。

# PRD/设计：增加 claude / codex 编码环境（ttyd web 终端）

| 版本 | 日期       | 作者     | 状态   |
| ---- | ---------- | -------- | ------ |
| v1.0 | 2026-07-26 | ANP 团队 | 待评审 |

> 现状摸底见本轮对话「opencode 集成现状」报告。本 spec 是「工作台三需求」分三份中的**第 3 份**（顺序 ①部署权限 → ③多AI工具 → ②绩效记录）。

## 1. 背景与目标

**现状**：开发者交互编码工作台只接入了 opencode（自带 web UI，iframe 嵌入）。`codews/manager.go:58-64` 已预注册 opencode/claude/codex 三个工具，但 `codews/tool.go:36-49` 的 `ClaudeTool`/`CodexTool.Start` 是 stub（返回"尚未接入"）。前端 `applications/page.tsx:648-663` 下拉框已有 claude\*/codex\* 选项（带 `*` 预留标记）。

**目标**：开发者工作台可用 **claude（Claude Code）**、**codex（OpenAI Codex）** 编码，体验与 opencode 对称（浏览器 iframe 交互工作台）；平台开发规范对三工具都生效；重开工作台可恢复上次会话。

**关键约束（决策已定）**：claude/codex 没有 opencode 那样的 self-hosted web UI，采用 **ttyd web 终端 + iframe** 方案（用户已选）。

## 2. 现状（已取证）

- ✅ **架构已就位**：`Tool` 接口（`codews/tool.go:13-17`）、`Manager`（端口分配 9400-9450、worktree 隔离 `dev-<user>`、进程生命周期、`Ensure` 主流程）都工具无关；claude/codex 已 Register。
- ✅ **opencode 集成模式**：`OpenCodeTool.Start`（`tool.go:23-33`）起 `opencode serve --port`；前端 `workspace-frame.tsx` iframe 嵌入；`POST /workspace`（`handler.go:164-195`）body `{tool}`。
- ✅ **规范注入已 tool 无关**：`handler.go:178-180` 在 `POST /workspace` 启动前（tool 解析前）调 `RefreshAgentsMD` 写 repo 根 `AGENTS.md`；opencode/claude/codex 都原生读 AGENTS.md（公共约定）。**仅注释 line 175 写"opencode"过时**。
- ✅ **配置动态生成能力**：`compute/opencode_gen.go` 可从 DB 动态生成 opencode.json（可借鉴给 claude/codex 配置）。
- ❌ **ClaudeTool/CodexTool 是 stub**（`tool.go:36-49`）。
- ❌ **Tool.Start 只继承 os.Environ()**（`tool.go:30`），无法注入 API key。
- ❌ **会话管理是 opencode 专属**：`ensureSession`/`SessionMessages`/`SendPrompt`（`manager.go:171-300`）硬编码调 opencode `/api/session*`；claude/codex（ttyd）无对等 API。
- ❌ **Dockerfile 未装 claude/codex/ttyd**（`Dockerfile.backend:15` 只装 opencode-ai）。

## 3. 设计

### 3.1 工具启动（Tool.Start 实现 + 接口扩展）

- `ClaudeTool.Start`：`ttyd -p <port> --writable -W claude --continue`（ttyd 把 claude CLI 暴露为 web 终端；`--continue` 恢复该目录最近会话，见 3.5）。
- `CodexTool.Start`：`ttyd -p <port> --writable -W codex <resume-args>`（codex 的 resume 命令以官方文档为准；若无则不带 resume，该工具暂不支持自动恢复）。
- **Tool 接口扩展**：给 Tool 加配置访问能力——`Start` 签名加 `env []string` 参数（或 Tool 持有 `*config.Store`）。`Manager.Ensure` 从 system_config 读智谱兼容 env（`*_base_url` + `zhipuai_api_key` + 模型）注入子进程。
- 复用 `Manager` 端口分配（`allocPortLocked`）、worktree 隔离（`ensureWorktree`）、进程回收（`cmd.Wait`）。

### 3.2 配置（复用智谱 key + 兼容端点 + Dockerfile + 持久化）

- **复用智谱 key**（用户既定，不新增 anthropic/openai key）：claude/codex 都走智谱 GLM。system_config 加：
  - `claude_base_url`（智谱 Anthropic 兼容端点，如 `https://open.bigmodel.cn/api/anthropic`）
  - `codex_base_url`（智谱 OpenAI 兼容端点，如 `https://open.bigmodel.cn/api/paas/v4`）
  - `claude_model`（如 `glm-4.6`）、`codex_model`
  - API key 复用现有 `zhipuai_api_key`
  - 具体 env 名（`ANTHROPIC_AUTH_TOKEN`/`ANTHROPIC_BASE_URL`/`OPENAI_BASE_URL` 等）以智谱官方 claude code/codex 接入文档为准，实现时确认。
- **Dockerfile.backend**：`apk add ttyd`（alpine 仓库或有，否则从 GitHub releases 下二进制）+ `npm i -g @anthropic-ai/claude-code @openai/codex`。
- **healthcheck**（`server/healthcheck.go`）：`exec.LookPath` 加 claude/codex/ttyd 探测。
- **会话持久化**：`docker-compose.prod.yml` 加 volume 挂载 `~/.claude`、`~/.codex`（类似现有 `../data/opencode`），工具会话历史不丢。

### 3.3 规范注入（零成本，已就绪）

- `RefreshAgentsMD` 在 `POST /workspace` 启动前 tool 无关地写 AGENTS.md（`handler.go:178-180`）→ claude/codex 启动后自读 → **平台规范对三工具都生效**。
- 仅改 `handler.go:175-176` 注释："启动 opencode 前" → "启动工作台前（opencode/claude/codex 都读 AGENTS.md）"。

### 3.4 会话管理分流（opencode 专属 API 降级）

- `ensureSession`/`SessionMessages`/`SendPrompt`（`manager.go:171-300`）：对非 opencode 工具**跳过**（不调 `/api/session*`）。
- `appdeploy/handler.go` `InjectRequirement`（:353-380）/ `RegisterChange`（:197+）：按 `session.Tool` 分流——仅 opencode 调 `/api/session*`；claude/codex 返回"该工具不支持平台侧注入/自动总结，请手动操作"。
- 前端：claude/codex 工作台隐藏"注入需求"按钮（或提示手动）。

### 3.5 会话恢复（ttyd + 工具自身 resume + 持久化）

- worktree 已持久（`dev-<user>` 固定）+ `~/.claude`/`~/.codex` volume 持久 → 工具会话历史跨重启保留。
- ttyd 启动带 resume：`claude --continue` / `codex resume` → 重开工作台恢复该目录最近会话。
- 取舍：靠工具自身 resume（按"目录最近会话"），不如 opencode 的"按 repo 精确匹配 + 平台 session API"，但能恢复；开发者可在终端 `/clear` 开新会话。

### 3.6 前端

- `workspace-frame.tsx`：opencode/claude/codex 都是 iframe（ttyd URL 与 opencode URL 都嵌 iframe），统一，无需按 tool 分流渲染。
- `applications/page.tsx:648-663`：去掉 claude/codex 的 `*` 预留标记和 `(预留)` title。

## 4. 验收标准

| 编号 | 验收点                                                                                                   |
| ---- | -------------------------------------------------------------------------------------------------------- |
| AC1  | 应用详情选 claude 打开工作台 → ttyd 终端加载、`claude` CLI 可交互输入                                    |
| AC2  | 选 codex 同理（依赖 codex CLI 在容器可跑）                                                               |
| AC3  | opencode 工作台不受影响（回归：iframe + session 注入/总结正常）                                          |
| AC4  | 多开发者隔离：dev-A 和 dev-B 同应用各开 claude 工作台，各自独立 worktree/端口                            |
| AC5  | 规范生效：claude/codex 工作台启动前 AGENTS.md 已刷新（含平台规范）                                       |
| AC6  | 会话恢复：claude 工作台关闭后重开，`--continue` 恢复上次对话                                             |
| AC7  | 智谱兼容配置从 system_config 注入：`/admin/config` 改 `claude_base_url`/`zhipuai_api_key` 后新开会话生效 |

## 5. 改动清单（file:line）

**后端**

- `internal/codews/tool.go:13-17`（Tool 接口加 env/config）、`:36-49`（ClaudeTool/CodexTool.Start 实现 ttyd 启动）、`:23-33`（OpenCodeTool.Start 适配新签名）。
- `internal/codews/manager.go:86-151`（Ensure 注入 API key env 给 Tool）、`:171-300`（ensureSession/SessionMessages/SendPrompt 非 opencode 跳过）。
- `internal/appdeploy/handler.go:175-176`（注释修正）、`:197+` RegisterChange（按 tool 分流）、`:353-380` InjectRequirement（按 tool 分流）。
- `internal/config/sysconfig.go` + `cmd/server/main.go:121-129`（anthropic_api_key/openai_api_key 种子）。
- `cmd/server/healthcheck.go`（LookPath 加 claude/codex/ttyd）。

**部署**

- `deploy/Dockerfile.backend:14-15`（apk add ttyd + npm i -g claude-code codex）。
- `deploy/docker-compose.prod.yml`（volume 挂载 ~~/.claude、~~/.codex）。

**前端**

- `platform/frontend/app/applications/page.tsx:648-663`（去 `*` 预留标记）。
- `platform/frontend/app/workspace/workspace-frame.tsx`（claude/codex 隐藏注入按钮，若该按钮对 opencode 显示）。

**测试**

- `internal/codews/tool_test.go`（ClaudeTool/CodexTool.Start 构造的命令含 ttyd + resume + env）。
- `internal/codews/manager_test.go`（Ensure 对 claude/codex 跳过 ensureSession）。

## 6. 风险与边界

- **CLI 在 alpine 可跑性**：claude-code/codex 是 npm 包，alpine 需 node（已有）+ 可能的 native 依赖；ttyd 需确认 alpine 仓库或有二进制 → 实现时先在 .28 容器内 `docker exec` 验证 `claude --version`/`codex --version`/`ttyd --version` 能跑。
- **智谱兼容端点可用性（关键前提）**：claude code/codex 连智谱 Anthropic/OpenAI 兼容端点的实际可用性——实现前先在 .28 容器内 `docker exec` 跑 `claude --version`/`codex --version`/`ttyd --version`，并实际调通智谱端点（端点 URL + env 名以智谱官方 claude code/codex 接入文档为准）。若智谱对某工具兼容不全，该工具标注限制。
- **codex resume 命令**：codex CLI 较新，resume 命令以官方文档为准；若无则 codex 暂不支持自动恢复（AC6 仅 claude）。
- **端口段 9400-9450 共享**：opencode/claude/codex 共用 51 个端口，按开发者×应用分配，不按工具预留。
- **会话恢复精度**：`--continue` 按目录最近会话，若开发者在该 worktree 多次开新会话可能恢复到非预期会话；可接受（开发者可 /clear）。

## 7. 关联

- 摸底报告：本轮对话「opencode 集成现状」。
- 与 ②（绩效记录）重叠：③的 codews 改造（会话/Tool 持久化）利好 ② 的"工作台交互记录持久化"。建议 ③ 实现时给 codews session 加最小 DB 记录（谁/何时/哪个工具/worktree），为 ② 铺路（可选）。
- 与 ①（部署权限）独立。

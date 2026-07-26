# 多 AI 工具 - claude MVP 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans。Steps use checkbox (`- [ ]`).

**Goal:** 开发者工作台可用 claude（Claude Code via ttyd web 终端，走智谱 GLM）编码，验证 ttyd 工作台模式跑通。codex 对称复制留下阶段。

**Architecture:** `ClaudeTool.Start` 起 `ttyd -p <port> --writable claude --continue`，注入智谱兼容 env（`ANTHROPIC_BASE_URL` + `ANTHROPIC_AUTH_TOKEN`=智谱key + `ANTHROPIC_MODEL`）；Tool 接口加 env 参数；Manager 按 tool 构造 env + ensureSession 仅 opencode；规范经 AGENTS.md 零成本生效；会话经 `--continue` + ~/.claude volume 恢复。

**Tech Stack:** Go(gin) + ttyd + claude-code CLI + 智谱 anthropic 兼容端点 + Next.js + PG

**Spec:** `docs/PRD/2026-07-26-多AI工具claude-codex-PRD.md`（claude MVP 子集）

**前提已验证（.28）:** ttyd 1.7.4 可装、claude-code v2.1.220 可装、`claude -p` 配智谱端点调通 GLM 返回响应。

## File Structure

| 文件                                          | 责任                                       | 动作                                                                              |
| --------------------------------------------- | ------------------------------------------ | --------------------------------------------------------------------------------- |
| `internal/codews/tool.go`                     | Tool 接口 + 三工具 Start                   | 改：Start 加 env 参数；ClaudeTool.Start 实现 ttyd；OpenCodeTool 适配              |
| `internal/codews/manager.go`                  | Manager 进程/端口/worktree                 | 改：注入 config.Store；Ensure 按 tool 构造 env；ensureSession/DeepURL 仅 opencode |
| `cmd/server/main.go`                          | NewManager 调用                            | 改：传 config.Store                                                               |
| `internal/config/sysconfig.go`+`main.go` 种子 | system_config                              | 加 `claude_base_url`/`claude_model`                                               |
| `deploy/Dockerfile.backend`                   | 镜像                                       | 加 `apk add ttyd` + `npm i -g @anthropic-ai/claude-code`                          |
| `deploy/docker-compose.prod.yml`              | 编排                                       | 加 `~/.claude` volume                                                             |
| `internal/appdeploy/handler.go`               | Workspace/RegisterChange/InjectRequirement | 改：注释修正；RegisterChange/InjectRequirement 按 tool 分流（claude 友好提示）    |
| `platform/frontend/app/applications/page.tsx` | 工具下拉                                   | 去 claude `*` 预留标记                                                            |

---

### Task 1: Tool 接口加 env + ClaudeTool.Start + OpenCodeTool 适配

- [ ] tool.go：`Start(repoDir string, port int, env []string)`；OpenCodeTool.Start 适配（`cmd.Env = append(os.Environ(), env...)`）；ClaudeTool.Start 实现 `ttyd -p <port> --writable claude --continue` + env 注入；CodexTool.Start 签名适配（仍 stub，返回"codex 留下阶段"）。
- [ ] 编译：`go build ./internal/codews/`

### Task 2: Manager 注入 config + Ensure 按 tool 构造 env + ensureSession 仅 opencode

- [ ] manager.go：Manager 加 `cfg *config.Store`；NewManager(host, cfg)；Ensure 内按 toolName 构造 env（claude: ANTHROPIC_BASE_URL/AUTH_TOKEN=zhipuai key/MODEL；opencode/codex: nil）；`tool.Start(workDir, port, env)`；`ensureSession`/DeepURL 仅 `toolName=="opencode"` 时调。
- [ ] main.go：NewManager 调用传 configStore。
- [ ] 编译：`go build ./cmd/server`

### Task 3: system_config 加 claude 配置种子

- [ ] main.go 种子 + sysconfig：`claude_base_url=https://open.bigmodel.cn/api/anthropic`、`claude_model=glm-4.6`（key 复用 zhipuai_api_key）。

### Task 4: Dockerfile 装 ttyd + claude-code + volume

- [ ] Dockerfile.backend：`apk add --no-cache ttyd` + `npm i -g @anthropic-ai/claude-code`。
- [ ] docker-compose.prod.yml：加 `../data/claude:/root/.claude` volume。
- [ ] healthcheck：LookPath 加 claude/ttyd（可选）。

### Task 5: handler 注释 + RegisterChange/InjectRequirement 按 tool 分流

- [ ] handler.go:175 注释"opencode"→"三工具"；RegisterChange/InjectRequirement：session.Tool != "opencode" 时返回友好提示（"该工具不支持平台侧自动总结/注入，请手动操作"）。

### Task 6: 前端去 claude 预留标记

- [ ] applications/page.tsx：claude 选项去 `*` 和 `(预留)` title。

### Task 7: .28 部署 + 验证

- [ ] scp + 重建 backend 镜像（装 claude/ttyd）+ frontend。
- [ ] 验证：选 claude 打开工作台 → ttyd 终端加载 → claude 交互调智谱；规范 AGENTS.md 生效；opencode 回归正常。

## Self-Review

- Spec 覆盖：3.1（Tool.Start ttyd）→Task1-2；3.2（配置/Dockerfile）→Task3-4；3.3（规范 AGENTS.md 零成本，仅改注释）→Task5；3.4（分流）→Task2/5；3.5（会话恢复 --continue + volume）→Task1/4；3.6（前端）→Task6。✓
- codex 留下阶段（spec 注明），MVP 不实现 CodexTool.Start 业务（签名适配保持 stub）。

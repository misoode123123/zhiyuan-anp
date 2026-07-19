# 贡献指南 · 智源 ANP

> 8 人团队协作约定。对齐《详细设计/开发标准与规范.md》。新人 onboard 先读本文档 + `README.md`。

---

## 1. 环境准备

- Go ≥ 1.22、Node ≥ 20（pnpm 10）、Python 3.10
- `platform/agent-runtime/.env` 填 `ZHIPUAI_API_KEY`
- 安装 opencode（编码引擎）

```bash
pnpm install            # 前端依赖
bash scripts/dev.sh     # 一键起三服务（Go:8080 / Python:8001 / 前端:3000）
```

默认账号：`admin / admin123`。

---

## 2. 统一命令（Makefile / pnpm scripts）

Windows 本地用 pnpm 脚本（无 make）；Linux/macOS/CI 两者皆可。

```bash
pnpm lint          # go vet + 前端 eslint（本地兜底，可跑）
pnpm test          # 后端 go test + 前端 test
pnpm check         # build + vet + lint（提交前跑）
pnpm be:lint       # golangci-lint（需先装）
pnpm py:lint       # ruff check（需先 pip install ruff）
pnpm fe:fmt        # 前端 prettier 格式化
```

各工具安装（可选，本地完整 lint）：

```bash
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest   # Go
pip install ruff                                                         # Python（含 format）
```

---

## 3. 分支与提交（Trunk-Based + Conventional Commits）

- 主干 `main` 始终可发布；短分支（≤ 2 天）+ 频繁合入。
- 分支命名：`feat/<scope>-<short>` / `fix/<scope>-<short>`。
- 提交信息强制 Conventional Commits（husky + commitlint 已配置，不合规会被拒）：

```
type(scope): subject

type ∈ feat | fix | docs | style | refactor | perf | test | build | ci | chore | revert
scope ∈ 模块名（rule / requirement / workspace / ci ...）
```

示例：`feat(rule): 新增 block 规则阻断` / `fix(workspace): 修应用名不显示`

---

## 4. PR 流程

1. 本地 `pnpm check` 全绿。
2. 推分支，开 PR，填 PR 模板 checklist。
3. CI 必须全绿（lint + typecheck + test + 安全扫描）。
4. 至少 1 人 review；高风险/核心模块 2 人（见规范 §4）。
5. Squash merge 到 main。

---

## 5. 代码边界（重要）

- 后端：跨模块只调 Service 接口，不直连他域 schema（规范 §2.2）。
- 前端：API 调用经 `lib/api.ts`（后续接 OpenAPI 生成客户端）；共享逻辑抽 `lib/hooks`。
- Python 运行时：只做 AI 编排，业务事务在 Go 后端。
- 数据库：迁移经工具（P0-3 进行中），禁止手改库。

---

## 6. 文档与变更记录

- 架构决策写 ADR（`docs/adr/NNNN-标题.md`）。
- 需求/方案/bug 修复都写文档到 `docs/`（PRD / 详细设计 / bugs）。
- 文档与代码同生命周期；过时文档视为缺陷。

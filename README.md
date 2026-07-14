# 智源 / ANP — 企业 AI 原生研发平台

> AI 驱动 **需求→研发→测试→审批→发布** 全流程；业务全程对话参与；规则治理约束 AI；关键节点人工决策。
> **v0.2.0**：主线闭环 + 10 真功能 + **9 大板块全部落地（M1 切片）** + RBAC 权限强制 + 多标签页 + 异步编码 + 多模态。

## 主线闭环（已端到端打通）

```
业务描述(文字+截图) → GLM 生成需求规格 → 派发编码
  → opencode+GLM-5.1 异步编码(规则校验) → 登记 🚪G3 待审批
  → 人工批准 → 🚀 发布 → 需求标记 ✅已交付
```

## 10 大真功能

| 板块 | 能力 |
|---|---|
| 💬 需求工作台 | 业务描述 + 图片 → GLM-4V 多模态生成规格 → 入库 |
| 💻 研发工作台 | opencode + GLM-5.1 异步编码，编码前过规则引擎 |
| 🧪 测试中心 | GLM 把需求验收标准转为测试用例 |
| 🚀 发布中心 | approved 变更 → 发布版本（需求标记已交付，闭环） |
| ⭐ 规则治理 | RaC 规则引擎，block 规则阻断 AI 操作 |
| 🚪 变更审批 | 🚪G3 人工审批流 |
| ⚙️ 系统配置 | 业务配置入库，热生效（不依赖文件） |
| ⚡ 算力资源 | 用量 / Token / 成本看板 |
| 🔐 用户权限 | RBAC + ABAC，角色×操作矩阵 |
| 🔗 需求→编码闭环 | 需求规格一键派发 opencode |

## 快速启动

```bash
bash scripts/dev.sh   # Go:8080 + Python:8001 + 前端:3000
# 浏览器打开 http://localhost:3000
```

**配置**：`platform/agent-runtime/.env` 填 `ZHIPUAI_API_KEY`；安装 opencode（编码引擎）。
详见 [`docs/部署指南.md`](docs/部署指南.md)。

## 技术栈

| 层 | 技术 |
|---|---|
| 平台后端 | Go (Gin 模块化单体 + sqlx + SQLite) |
| AI/Agent 运行时 | Python (FastAPI + zhipuai SDK + 智谱 GLM) |
| 编码引擎 | opencode + GLM-5.1 |
| 前端 | Next.js + TS + Tailwind（多标签页） |
| 模型 | 智谱 GLM-4-Flash/4V/5.1（可换 Claude 等） |

## 文档

- [`docs/企业AI原生研发平台方案.md`](docs/企业AI原生研发平台方案.md) — 总体方案 V1.0（六篇）
- [`docs/开发展开计划.md`](docs/开发展开计划.md) — MVP / 技术栈 / Monorepo / 路线
- [`docs/详细设计/`](docs/详细设计/) — 14 份（基座 2 + 横切核心 3 + 9 板块）
- [`docs/部署指南.md`](docs/部署指南.md) — 环境与部署

## 9 大板块完成度（v0.2.0）

| 板块 | 状态 | M1 实现 |
|---|---|---|
| 01 需求工作台 | ✅ | 对话式梳理（SSE 流式 + 语音 ASR）+ 一次性生成规格 |
| 02 研发工作台 | ✅ | opencode+GLM 异步编码 + 规则校验 + 编码规范注入 |
| 03 测试与质量中心 | ✅ | AI 验收标准转测试用例 |
| 04 规则治理中心 | ✅ | RaC 规则引擎（block 阻断）+ 编码规范（全局/项目级） |
| 05 安全与合规中心 | ✅ **新** | Go 原生扫描（密钥/SAST/提示注入）+ 安全门 + 数据分级 + 审计 |
| 06 发布中心 | ✅ | approved→发布→需求标记已交付 |
| 07 运维中心 | ✅ **新** | 健康检查（DB/agent-runtime/opencode）+ 看板 + 告警 + SOP 预案 |
| 08 算力与资源中心 | ✅ | Token 用量/成本看板 |
| 09 AI 能力市场 | ✅ **新** | 技能注册/上架 + APIKey + 调用网关 + 用量 + 领域 Agent |

> 横切：**RBAC 权限强制**（板块05 配套）—— 集中式中间件按路由强制 7 类写/危险操作鉴权，默认 admin/dev1/biz1 演示成员。

> M1 切片说明：各板块按当前技术栈（Go+Gin+SQLite+HTTP→Python FastAPI）落地精简可用版本；愿景中的 gRPC/LangGraph/LiteLLM/外部工具（Semgrep 等）/Copilot SDK npm 包列为后续阶段（详见各板块设计文档「落地优先级」）。

## 项目结构

```
智源-ANP平台/
├── platform/
│   ├── backend/         # Go 后端（workspace/requirement/dev/rule/change/qa/release/
│   │                    #   compute/auth/config/conversation/standard/
│   │                    #   ops[07]/security[05]/capability[09]）
│   ├── agent-runtime/   # Python AI 运行时（GLM 网关：/v1/chat 流式 + /v1/asr）
│   ├── frontend/        # Next.js（工作台 + 多标签页 + 用户/空间切换器）
│   ├── infra/           # proto / 迁移
│   └── opencode.json    # 编码引擎配置（智谱 provider）
├── pilots/              # 试点项目（AI 编码产出）
├── scripts/dev.sh       # 一键启动三服务
└── docs/                # 方案 + 详细设计 + 部署
```

## 设计理念

- **规则即代码（RaC）**：制度/红线结构化，约束所有 AI 行为
- **分级自治**：低风险自动，高风险 🚪 人工闸门
- **配置中心化**：业务配置入库，热生效（不依赖文件）
- **模型无关**：智谱 GLM（可换 Claude/其他）
- **多租户**：项目空间隔离数据/权限/用量

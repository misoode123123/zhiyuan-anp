# 智源 ANP 平台 · AI 内核与 Agent 编排（详细设计）

> **文档定位**：Python 侧 `agent-runtime` 微服务的核心技术设计——Orchestrator 总控、Agent 生命周期、多 Agent 接力、工具市场、三级记忆、模型路由、LangGraph 状态机、五约束运行时强制，以及与 Go 后端经 gRPC 的契约。是九大板块共用 AI 能力的**引擎骨架**。
> **语言/栈**：Python + LangGraph + LiteLLM；与 Go 后端经 gRPC（IDL 契约）通信。
> **配套**：软件开发架构设计（ADR-005）、闸门与决策引擎、规则治理中心、项目空间与多项目管理。

---

## 1. 设计目标与约束

| 目标 | 说明 |
|---|---|
| 引擎化 | Orchestrator + 可插拔 Agent + 可插拔工具，新增能力=注册而非改引擎 |
| 确定性可控 | Agent 不"自由发挥"——编排路径、工具、权限均由 Go 侧配置/规则约束 |
| 五约束内嵌 | 可校验/可追溯/可回滚/守边界/守权限 在运行时强制，非事后检查 |
| 多租户隔离 | `project_space_id` 贯穿所有 Agent 调用、记忆、工具执行 |
| 模型无关 | 经 LiteLLM 路由，按 任务难度×成本×延迟 选模，可热切换 |
| 可观测 | 每步产出可追溯（traceId 跨 Go/Python 透传），可重放 |
| 降级安全 | 模型故障/超时/越权 → 自动降级人工，绝不静默失败 |

**硬约束**：agent-runtime 是**无状态计算服务**（状态在 PG/Redis）；不承载业务事务（事务在 Go 后端）；不直连供应商模型（经 LiteLLM）；不绕过 Go 侧规则/闸门（产出回流 Go 校验）。

---

## 2. 整体架构

```
┌─────────────────────────────────────────────────────────────────┐
│  Go 后端（Gin 模块化单体）                                        │
│  requirement / dev / rule / gate / pipeline / knowledge / audit  │
└───────────────────────┬─────────────────────────────────────────┘
                        │ gRPC（IDL 契约）+ traceId 透传
┌───────────────────────▼─────────────────────────────────────────┐
│  Python agent-runtime（独立微服务）                               │
│                                                                  │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │  Orchestrator（总控 Agent）                                  │  │
│  │  规划 Planner / 拆解 Decomposer / 调度 Scheduler             │  │
│  │  汇总 Aggregator / 记忆协调 MemoryCoord / 安全围栏 Guard      │  │
│  └──────┬─────────────────────────────────────────────────────┘  │
│         │ LangGraph StateGraph 编排                              │
│  ┌──────▼──────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐          │
│  │ 需求澄清Agent│ │ 编码Agent │ │ 评审Agent │ │ 运维Agent │ ...     │
│  └──────┬──────┘ └─────┬────┘ └─────┬────┘ └─────┬────┘          │
│         │              │           │            │                │
│  ┌──────▼──────────────▼───────────▼────────────▼──────────┐    │
│  │  工具市场 Tools Market（注册/鉴权/调用/留痕）              │    │
│  └──────┬─────────────────────────────────────────────────┘    │
│  ┌──────▼──────┐  ┌──────────────────┐  ┌──────────────────┐    │
│  │ 三级记忆系统 │  │ 模型路由 LiteLLM  │  │ 安全围栏 Guard    │    │
│  │ 项目/短期/企业│  │ 难度×成本×延迟   │  │ 五约束强制       │    │
│  └─────────────┘  └──────────────────┘  └──────────────────┘    │
└─────────┬───────────────┬────────────────────┬──────────────────┘
          │               │                    │
     PostgreSQL       Redis(上下文)        MinIO(产物)
   (多schema+向量)    (短期记忆/队列)       (代码/文档/日志)
```

### 2.1 与 Go 后端的职责边界

| 职责 | 归属 | 说明 |
|---|---|---|
| 业务事务（创建需求、提交 PR、审批） | Go 后端 | agent-runtime 不写业务库 |
| 规则定义/校验 | Go `rule` 模块 | agent-runtime 调用，不内置规则库 |
| 闸门审批流 | Go `gate` 模块 | agent-runtime 触发闸门、不自己放行 |
| Agent 编排/推理/工具调用 | Python agent-runtime | 核心计算 |
| 模型调用 | Python LiteLLM | 统一出站 |
| 记忆检索/RAG | Python | 向量检索在 Python |
| 记忆元数据持久化 | Go `knowledge` 模块 | Python 经 gRPC 读写 |
| 审计留痕 | Go `audit` 模块 | agent-runtime 上报事件 |

⭐ **关键原则**：agent-runtime 是"大脑"，Go 后端是"手脚与记忆"。Agent 决定做什么，Go 后端负责以事务安全的方式做掉并留痕。

---

## 3. Orchestrator 总控 Agent

Orchestrator 是所有任务的入口与总控，本身也是一个 Agent，但职责是**元认知**：不直接产出业务内容，而是规划、调度、监督其他 Agent。

### 3.1 六大职责

| 职责 | 组件 | 输入 | 输出 |
|---|---|---|---|
| 规划 Planning | Planner | 任务目标 + 项目上下文 + 规则约束 | 执行计划（DAG） |
| 拆解 Decomposition | Decomposer | 计划节点 | 子任务列表（分配给具体 Agent） |
| 调度 Scheduling | Scheduler | 子任务 + Agent 注册表 + 依赖 | 执行顺序/并行度/模型选择 |
| 汇总 Aggregation | Aggregator | 各 Agent 产出 | 合并结果 + 冲突消解 |
| 记忆协调 | MemoryCoord | 任务上下文 | 三级记忆的读写编排 |
| 安全围栏 | Guard | 每步产出 | 五约束校验/拦截/升级 |

### 3.2 Orchestrator 执行主循环

```
接收任务(Go gRPC)
  │
  ▼
①加载上下文：project_space_id + 规则集 + 记忆召回
  │
  ▼
②Planner 规划 → 生成 Plan DAG（含每节点 Agent/工具/模型/权限/可回滚点）
  │
  ▼
③Guard 预检：Plan 是否违反规则/越权/越界？  ──是──▶ 拒绝并回流 Go（带原因）
  │否
  ▼
④Scheduler 拓扑排序 → 确定可并行节点
  │
  ▼
⑤循环执行节点：
    ├─ 取下个节点 → 实例化 Agent → 注入受限上下文+权限令牌
    ├─ Agent 经 LangGraph 运行（可调工具、调模型、读写短期记忆）
    ├─ 每步产出 → Guard 校验（五约束）→ 不合规即拦截+留痕
    ├─ 工具调用经 Tools Market（鉴权/限额/留痕）
    └─ 产出回流 Go（业务落库 + audit）
  │
  ▼
⑥Aggregator 汇总 → 冲突消解 → 最终产出
  │
  ▼
⑦MemoryCoord 写记忆（本次经验沉淀到项目记忆/企业记忆）
  │
  ▼
⑧回流 Go：结果 + traceId + 依据规则清单 + 置信度
```

### 3.3 Plan DAG 数据结构（核心）

```python
# Python 侧运行时结构（与 gRPC 消息对应）
@dataclass
class PlanNode:
    node_id: str
    agent_type: str            # 映射 Agent 注册表
    objective: str             # 本节点目标
    inputs: list[str]          # 依赖上游 node_id
    tools_allowed: list[str]   # ⭐ 白名单，超出即拦截
    model_tier: str            # low/mid/high，供 LiteLLM 选模
    autonomy_level: int        # L0-L3，决定是否需闸门
    rollback_point: bool       # 是否可回滚点
    guard_rules: list[str]     # 本节点强制校验的规则 ID
    expected_output_schema: str # 产出 schema，可校验性保证
    max_steps: int             # 防 Agent 失控循环
    max_tokens: int            # 成本上限
    timeout_sec: int

@dataclass
class Plan:
    plan_id: str
    project_space_id: str      # ⭐ 多租户路由键
    root_objective: str
    nodes: dict[str, PlanNode] # DAG 节点
    edges: list[tuple[str, str]]  # 依赖边
    created_by_trace: str      # traceId 透传
    rule_context: list[str]    # 全局适用规则 ID
```

⭐ Plan DAG 是 agent-runtime 的"施工图"，每个节点都带**白名单工具、模型档位、自治等级、可回滚标记**——这是五约束能被强制的前提。

---

## 4. Agent 生命周期与运行时

### 4.1 Agent 注册表

Agent 不是硬编码的，而是通过**注册表**动态管理。新增一个 Agent = 写一个 LangGraph 子图 + 注册元数据。

```python
@dataclass
class AgentSpec:
    agent_type: str            # 唯一标识，如 "coder"、"reviewer"、"requirement_analyst"
    display_name: str
    description: str
    system_prompt_template: str  # 模板，含变量插槽
    default_tools: list[str]     # 默认可用工具
    allowed_tool_scopes: list[str]  # 工具作用域白名单
    default_model_tier: str      # 默认模型档位
    supported_autonomy: list[int] # 支持的自治等级 L0-L3
    max_concurrent: int          # 同类 Agent 并发上限
    input_schema: dict           # Pydantic schema
    output_schema: dict          # 产出 schema（可校验性）
    version: str                 # 语义化版本
    enabled: bool
```

### 4.2 生命周期状态机

```
            register                enable
  [DRAFT] ─────────▶ [REGISTERED] ─────────▶ [ACTIVE]
                                              │
                                    disable   │   instantiate
                                  ◀──────────┤──────────────▶
                                  [DISABLED]                   │
                                                               ▼
                                                        [RUNNING] ──▶ [SUCCEEDED]
                                                            │    ╲
                                                  timeout/  │     ╲ error
                                                  cancel    │      ▼
                                                            ▼   [FAILED]
                                                       [CANCELLED]    │
                                                                       │
                                                              retry?   │
                                                          ◀────────────┘
```

### 4.3 Agent 运行时实例

每次执行实例化一个 Agent 运行时对象，**注入受限上下文与权限令牌**，运行结束即销毁（无状态服务原则）。

```python
@dataclass
class AgentRuntime:
    instance_id: str           # 运行实例 ID（用于 trace）
    agent_type: str
    project_space_id: str      # ⭐ 租户隔离
    plan_node: PlanNode        # 本次的节点约束
    permission_token: str      # Go 签发的短期权限令牌（含 scope + expiry）
    short_term_memory: ContextWindow  # 短期上下文窗口
    trace_id: str
    step_count: int = 0        # 已执行步数（防失控）
    token_used: int = 0        # 已用 token（防成本失控）
```

⭐ `permission_token` 是 Go 侧签发的**短期、受限、可吊销**令牌，携带本次允许的数据访问 scope、工具 scope、过期时间。Agent 调工具/读数据必须出示令牌，越权即被工具层拒绝。这是"守权限"约束的技术实现核心。

---

## 5. 多 Agent 接力机制

真实任务往往需要多个 Agent 协作（如：需求澄清 → 架构设计 → 编码 → 自测 → 评审）。接力机制保证上下文与约束的传递。

### 5.1 接力模式

| 模式 | 说明 | 示例 |
|---|---|---|
| 串行接力 | A 产出作为 B 输入，按 DAG 边传递 | 需求Agent→架构Agent→编码Agent |
| 并行扇出 | 一个节点产出分发给多个 Agent 并行 | 编码Agent→(单测Agent + 安全Agent + 规则Agent) |
| 汇聚合并 | 多个 Agent 产出汇入 Aggregator | 单测+安全+规则 → 汇总评审结论 |
| 循环精炼 | Agent 产出→评审Agent否决→回退重做（带反馈） | 编码→评审不通过→带意见重写 |
| 人机接力 | Agent 到达闸门节点→暂停等 Go 侧人工审批→恢复 | G2 架构评审点 |

### 5.2 接力时的上下文传递契约

接力不是简单"把上文全塞给下个 Agent"，而是**受控剪裁**，避免上下文爆炸与越权。

```
Agent A 产出
    │
    ├─ 结构化产出（按 output_schema）──▶ 作为 Agent B 的结构化输入
    ├─ 关键依据（规则/知识引用）──▶ 透传（可追溯性）
    ├─ 原始上下文 ──▶ MemoryCoord 按 B 的 need-to-know 剪裁
    └─ 敏感数据 ──▶ 按权限令牌过滤，不传递（守权限）
```

⭐ **need-to-know 剪裁**：MemoryCoord 根据下游 Agent 的 `permission_token` scope，只传递其有权可见的上下文片段。例如运维 Agent 不应看到需求 Agent 中的客户隐私字段。

### 5.3 循环精炼的边界保护

```python
# 防止 Agent 无限循环重做
MAX_RETRY_PER_NODE = 3          # 单节点最多重做 3 次
MAX_TOTAL_STEPS = 50            # 单任务总步数上限
MAX_TOKENS_PER_TASK = 500_000   # 单任务 token 上限

# 任一超限 → Guard 拦截 → 升级人工（降级机制）
```

---

## 6. 工具市场（Tools Market）

Agent 通过工具与外部世界交互（查 GitLab、读知识库、写文件、调流水线、查监控）。工具市场是统一注册、鉴权、调用、留痕的中间层。

### 6.1 工具注册元数据

```python
@dataclass
class ToolSpec:
    tool_id: str
    name: str
    description: str            # 供 Agent 理解用途
    scope: str                  # 权限作用域，如 "gitlab:read"、"pipeline:trigger"
    handler_type: str           # "grpc"(调Go) / "http" / "python_local"
    handler_ref: str            # 调用地址/函数引用
    input_schema: dict          # Pydantic
    output_schema: dict
    is_destructive: bool        # 是否破坏性（写/删/发布）
    requires_gate: str | None   # 破坏性工具需经过的闸门，如 "G5"
    rate_limit_per_min: int
    enabled: bool
    version: str
```

🟥 **破坏性工具强制经闸门**：`is_destructive=true` 的工具（如发布、删库、改生产配置）调用前，工具市场**自动挂起**并向 Go `gate` 模块发起审批请求，审批通过后才执行。Agent 不能自己绕过。

### 6.2 工具调用链路

```
Agent 决定调工具 X
  │
  ▼
①Tools Market 校验：X 在 Agent 的 tools_allowed 白名单？  ──否──▶ 拒绝+留痕
  │是
  ▼
②校验 permission_token 是否含 X.scope？  ──否──▶ 拒绝+留痕
  │是
  ▼
③限流检查（rate_limit_per_min）  ──超──▶ 限流等待
  │
  ▼
④X.is_destructive？ ──是──▶ 向 Go gate 发起审批 → 等待/阻塞
  │否                         │
  ▼                           ▼
⑤执行 handler（grpc/http/local）── 产出 ──▶ Guard 校验产出 schema
  │
  ▼
⑥留痕：tool_invocation 记录（agent/工具/输入摘要/产出摘要/耗时/traceId）
```

### 6.3 内置工具分类

| 类别 | 工具示例 | scope | 破坏性 |
|---|---|---|---|
| 代码 | git_clone、read_file、search_code | `code:read` | 否 |
| 代码 | create_branch、commit、push | `code:write` | 是（G3） |
| 知识 | rag_search、memory_read | `knowledge:read` | 否 |
| 流水线 | trigger_pipeline、get_status | `pipeline:trigger` | 是（G5） |
| 规则 | fetch_rules、validate_artifact | `rule:read` | 否 |
| 需求 | create_requirement、update_spec | `requirement:write` | 是（G1） |
| 监控 | query_metrics、get_alerts | `obs:read` | 否 |
| 运维 | restart_service、scale_replica | `ops:write` | 是（G6） |

⭐ 所有"写"类工具默认破坏性，需经对应闸门——这是 Agent 不能直发生产/直改数据的技术保证。

---

## 7. 记忆系统（三级）

记忆是 Agent 智能的基石。三级记忆各有不同生命周期、粒度、写入策略。

### 7.1 三级记忆模型

| 级别 | 名称 | 生命周期 | 粒度 | 存储 | 写入方 |
|---|---|---|---|---|---|
| L1 | 短期上下文 Short-term Context | 单次任务/会话 | 对话窗口、中间产物 | Redis | Agent 运行时 |
| L2 | 项目记忆 Project Memory | 项目长期 | 项目事实、决策、约定、教训 | PG + pgvector | MemoryCoord 任务结束沉淀 |
| L3 | 企业记忆 Enterprise Memory | 跨项目长期 | 通用规范、最佳实践、知识资产 | PG + pgvector（独立 schema） | 人工/治理流程发布 |

```
┌──────────────────────────────────────────────────────┐
│ 任务执行中                                            │
│  ┌────────────────────────────────────────────────┐  │
│  │ L1 短期上下文（Redis，ContextWindow）            │  │
│  │  · 当前对话/当前 Plan 的中间状态                 │  │
│  │  · 任务结束即清除（精华沉淀到 L2）               │  │
│  └────────────────────────────────────────────────┘  │
│              │ 召回                        │ 沉淀      │
│  ┌───────────▼────────────┐    ┌──────────▼────────┐ │
│  │ L2 项目记忆（PG+向量）  │    │ L3 企业记忆        │ │
│  │ · 本项目的技术栈/架构   │    │ · 跨项目规范       │ │
│  │ · 历史决策与理由        │◀──▶│ · 最佳实践         │ │
│  │ · 本项目踩过的坑        │ 上 │ · 通用知识资产     │ │
│  │ · 命名/分层约定         │ 升 │ · Rule/Process/Knowledge │ │
│  └────────────────────────┘    └───────────────────┘ │
└──────────────────────────────────────────────────────┘
```

### 7.2 记忆召回策略（RAG）

```python
def recall_context(query: str, project_space_id: str, agent_scope: str) -> RecallResult:
    # 1. L2 项目记忆：向量检索 + 关键词混合，限本项目
    project_hits = vector_search(
        schema=f"memory_proj_{project_space_id}",
        query=query, top_k=8, filter={"scope": agent_scope}
    )
    # 2. L3 企业记忆：跨项目通用知识
    enterprise_hits = vector_search(
        schema="memory_enterprise",
        query=query, top_k=5, filter={"asset_type": ["Process", "Knowledge"]}
    )
    # 3. 权限过滤：只返回 permission_token 允许可见的条目
    filtered = apply_permission_filter(project_hits + enterprise_hits, token)
    # 4. 相关性 + 时效性加权排序
    return rank_and_dedupe(filtered)
```

🟥 **租户隔离**：L2 项目记忆按 `project_space_id` 物理隔离（独立 schema 或行级过滤 + 硬性 project_space_id 校验）。Agent A 永远检索不到项目 B 的记忆，除非有显式跨项目授权。

### 7.3 记忆写入与遗忘

- **写入触发**：任务成功完成 → MemoryCoord 提取"事实/决策/教训" → 写 L2。
- **质量门**：记忆写入需经 Guard 校验（不写臆测、不写敏感数据、可校验）。
- **遗忘机制**：过时记忆（如已废弃的决策）由项目成员标记失效；企业记忆下线走治理流程。记忆条目带 `valid_until` 与 `superseded_by` 字段。
- **不写敏感**：记忆写入前经脱敏过滤，客户隐私/密钥/凭证不进记忆库。

---

## 8. 模型路由（LiteLLM）

所有模型调用经 LiteLLM 网关，按**任务难度 × 成本 × 延迟**三维选模，支持故障切换与成本控制。

### 8.1 模型档位定义

| 档位 | 适用任务 | 典型模型（信创可选） | 相对成本 |
|---|---|---|---|
| low | 分类、抽取、简单校验 | Qwen-Turbo / DeepSeek-Lite / 文心 Lite | ×1 |
| mid | 常规编码、需求澄清、评审 | Qwen-Plus / DeepSeek / 通义千问 | ×3 |
| high | 架构设计、复杂推理、跨域规划 | Qwen-Max / DeepSeek-R1 / 国产大参数 | ×10 |
| embedding | 向量化 | bge-m3 / text-embedding | 极低 |

### 8.2 选模决策矩阵

```
选模输入：
  task_difficulty   ← Planner 评估（low/mid/high）
  latency_budget    ← PlanNode.timeout_sec 推算
  cost_budget       ← PlanNode.max_tokens × 单价上限
  project_quota     ← Go 后端查该项目剩余配额

选模输出：具体 model_id + fallback 链

示例策略：
  difficulty=low  → low 档模型
  difficulty=high + 有配额 → high 档
  difficulty=high + 无配额 → 降级 mid 档 + 标记"降级"回流 Go
  latency 敏感 → 选低延迟档（即使成本略高）
```

### 8.3 故障切换与成本熔断

```python
# LiteLLM 配置：主备链 + 熔断
model_router:
  coder_model:
    primary: "qwen-plus"
    fallback: ["deepseek-coder", "wenxin-coder"]
    retry: 2
    timeout: 60s
  # 成本熔断：单任务超 max_tokens 即停 + 升级人工
  circuit_breaker:
    per_task_token_cap: 500_000
    per_space_daily_cap: 50_000_000  # project_space 维度日上限
```

⭐ **配额联动**：Go 后端 `workspace` 模块管理每个 project_space 的 token/算力配额；agent-runtime 每次调用前经 gRPC 查配额，超额即拒绝（L0 降级为人工任务）。配额消耗实时回写 Go 后端。

---

## 9. 五约束的运行时强制（⭐ 核心）

五约束不是文档口号，而是在 agent-runtime 运行时**每一步强制校验**的技术机制。

### 9.1 约束到机制的映射

| 约束 | 运行时强制机制 | 实现位置 |
|---|---|---|
| 可校验 | 每个产出必须符合 `output_schema`；无 schema 的产出被拒 | Guard + 工具市场 |
| 可追溯 | 每步产出绑定 traceId/规则ID/知识引用/模型版本；不可篡改 | 全链路 |
| 可回滚 | PlanNode 标记 rollback_point；破坏性操作生成 revert 指令 | Orchestrator + 工具 |
| 守边界 | Agent 的 tools_allowed 白名单 + 模块边界规则（来自 Go rule） | Guard + 工具市场 |
| 宥权限 | permission_token（短期/受限/可吊销）校验每次数据/工具访问 | 工具市场 + 数据层 |

### 9.2 Guard 围栏工作流

```
Agent 产出 artifact
  │
  ▼
①可校验：artifact 是否符合 output_schema？  ──否──▶ 拒绝，要求重做
  │是
  ▼
②可追溯：是否标注了依据规则/知识/模型版本？  ──否──▶ 拒绝，要求补标
  │是
  ▼
③守边界：是否触及非本 Agent 边界？  ──是──▶ 拦截 + 留痕 + 升级
  │否
  ▼
④守权限：所用工具/数据是否在 permission_token scope 内？  ──否──▶ 拒绝 + 安全告警
  │是
  ▼
⑤可回滚：若是破坏性产出，是否附 revert 指令？  ──否──▶ 拒绝（破坏性必可回滚）
  │是
  ▼
⑥规则合规：调 Go rule 模块校验是否违反规则库？  ──违──▶ 拦截 + 留痕
  │合
  ▼
放行产出 → 回流 Go 后端
```

🟥 **拦截即留痕**：任何一次 Guard 拦截都产生 `audit` 事件（含完整上下文），供安全与合规中心回溯。多次拦截同类型 → 自动触发告警。

### 9.3 禁止事项的技术兜底

| 禁止 | 技术兜底 |
|---|---|
| Agent 直发生产 | 发布工具 is_destructive + requires_gate=G5，工具层强制挂起审批 |
| Agent 跳闸门 | Orchestrator 到达 gate 节点必暂停，Plan 引擎不允许跳过 |
| Agent 越权数据 | permission_token scope 校验，数据访问经 Go 受控 API |
| Agent 删审计 | 审计在 Go audit 模块，agent-runtime 仅有只读/追加权限 |
| Agent 无限循环 | max_steps/max_tokens 超限即终止 + 升级人工 |

---

## 10. LangGraph 状态机设计

LangGraph 是 agent-runtime 的编排引擎核心，用 StateGraph 表达 Agent 内部的状态流转。

### 10.1 通用 Agent 子图模板

每个业务 Agent 内部是一个 LangGraph 子图，遵循统一模板：

```
                    ┌──────────────┐
            ┌──────▶│   START      │
            │       └──────┬───────┘
            │              ▼
            │       ┌──────────────┐
            │       │ load_context │ 载入短期记忆+召回
            │       └──────┬───────┘
            │              ▼
            │       ┌──────────────┐
            │       │   think      │ 模型推理（经 LiteLLM）
            │       └──────┬───────┘
            │              ▼
            │       ┌──────────────┐
            │       │ decide_action│ 调工具 or 产出 or 结束
            │       └──┬─────┬─┬───┘
            │          │     │ │
            │     工具 │  产出│ │结束
            │          ▼     ▼ ▼
            │   ┌─────────┐ ┌────────┐ ┌────────┐
            │   │call_tool│ │ guard  │ │ END    │
            │   └────┬────┘ │ check  │ └────────┘
            │        │      └───┬────┘
            │   工具产出         │ 合规？
            └────────┘     否 ───┘ 重做(带反馈)
```

### 10.2 State 定义

```python
from typing import TypedDict, Annotated
from langgraph.graph import StateGraph

class AgentState(TypedDict):
    # 输入
    project_space_id: str
    plan_node: dict                 # PlanNode 序列化
    objective: str
    permission_token: str
    trace_id: str
    # 运行时
    messages: Annotated[list, add_messages]  # 对话历史
    tool_calls_made: list[dict]     # 已调工具（留痕+防重复）
    artifacts_produced: list[dict]  # 已产出
    guard_violations: list[dict]    # 违规记录
    step_count: int
    token_used: int
    # 输出
    final_output: dict | None
    status: str                     # running/succeeded/failed/blocked
    citations: dict                 # 规则/知识/模型版本引用（可追溯）
    revert_plan: dict | None        # 回滚指令（破坏性产出时）
```

### 10.3 条件路由（guard 决定走向）

```python
def route_after_guard(state: AgentState) -> str:
    if state["guard_violations"] and len(state["guard_violations"]) >= MAX_RETRY:
        return "block"            # 多次违规 → 阻塞 + 升级人工
    if state["guard_violations"]:
        return "retry"            # 带反馈重做
    if state["final_output"]:
        return "end"              # 成功结束
    return "think"                # 继续

graph = StateGraph(AgentState)
graph.add_node("load_context", load_context_node)
graph.add_node("think", think_node)
graph.add_node("decide_action", decide_node)
graph.add_node("call_tool", tool_node)
graph.add_node("guard_check", guard_node)
graph.add_edge("load_context", "think")
graph.add_edge("think", "decide_action")
graph.add_conditional_edges("decide_action", route_decision, {...})
graph.add_conditional_edges("guard_check", route_after_guard, {
    "retry": "think", "block": "block_node", "end": END
})
```

### 10.4 持久化检查点（中断与恢复）

LangGraph 的 checkpointer 让 Agent 可在闸门节点**暂停**，待人工审批后**恢复**：

```python
# 使用 PostgreSQL checkpointer
from langgraph.checkpoint.postgres import PostgresSaver

checkpointer = PostgresSaver(conn_string=...)
graph = graph.compile(
    checkpointer=checkpointer,
    interrupt_before=["gate_node"]   # 到闸门前暂停
)

# 恢复执行（Go 侧审批通过后回调）
graph.invoke(None, config={"configurable": {"thread_id": task_id}})
```

⭐ 这是"人机接力"与"闸门阻断"的技术实现：Plan 中的 gate 节点用 `interrupt_before` 暂停，Go `gate` 模块审批通过后经 gRPC 回调恢复。

---

## 11. gRPC 接口（Go ↔ agent-runtime）

契约先行：跨语言边界用 gRPC IDL 定义。以下为关键服务。

### 11.1 编排服务 OrchestrationService

```protobuf
// proto/agent_runtime/orchestration.proto
syntax = "proto3";
package anp.agent_runtime.v1;

service OrchestrationService {
  // 启动编排任务（异步，返回 task_id）
  rpc StartTask(StartTaskRequest) returns (StartTaskResponse);

  // 查询任务状态
  rpc GetTaskStatus(TaskStatusRequest) returns (TaskStatusResponse);

  // 流式订阅任务事件（产出/违规/完成）
  rpc StreamEvents(StreamEventsRequest) returns (stream TaskEvent);

  // 取消任务
  rpc CancelTask(CancelTaskRequest) returns (CancelTaskResponse);

  // 闸门审批回调（Go gate 审批通过后恢复暂停的 Agent）
  rpc ResumeAtGate(ResumeAtGateRequest) returns (ResumeAtGateResponse);
}

message StartTaskRequest {
  string project_space_id = 1;     // ⭐ 多租户路由键
  string objective = 2;            // 任务目标
  string task_type = 3;            // requirement_analysis/code/review/ops...
  string triggered_by = 4;         // 用户/系统
  string permission_token = 5;     // Go 签发的权限令牌
  string trace_id = 6;             // 链路追踪
  map<string, string> params = 7;  // 任务参数
  int32  autonomy_level = 8;       // L0-L3
}

message StartTaskResponse {
  string task_id = 1;
  string plan_id = 2;              // 生成的 Plan DAG ID
  TaskStatus status = 3;
}

message TaskStatusResponse {
  string task_id = 1;
  TaskStatus status = 2;           // PENDING/RUNNING/BLOCKED_AT_GATE/SUCCEEDED/FAILED/CANCELLED
  string current_node = 3;
  repeated ArtifactRef artifacts = 4;
  repeated ViolationRecord violations = 5;
  string trace_id = 6;
  TokenUsage usage = 7;
}

message TaskEvent {
  string task_id = 1;
  EventType type = 2;              // NODE_START/NODE_END/TOOL_CALL/GUARD_VIOLATION/GATE_REACHED/DONE/ERROR
  string node_id = 3;
  google.protobuf.Any payload = 4;
  string trace_id = 5;
  int64  timestamp = 6;
}
```

### 11.2 记忆服务 MemoryService

```protobuf
service MemoryService {
  rpc Recall(RecallRequest) returns (RecallResponse);
  rpc Write(WriteMemoryRequest) returns (WriteMemoryResponse);
  rpc Invalidate(InvalidateRequest) returns (InvalidateResponse);
}

message RecallRequest {
  string project_space_id = 1;
  string query = 2;
  string scope = 3;                // 调用方 Agent 类型
  string permission_token = 4;
  int32  top_k = 5;
  bool   include_enterprise = 6;   // 是否检索企业记忆
}
```

### 11.3 工具调用回流（agent-runtime → Go）

agent-runtime 调用"写"类工具时，实际通过 gRPC 调 Go 后端执行业务事务：

```protobuf
// Go 侧实现，agent-runtime 调用
service ToolExecutionService {
  // 执行业务工具（在 Go 侧事务中执行）
  rpc ExecuteTool(ToolExecutionRequest) returns (ToolExecutionResponse);
  // 查询配额
  rpc CheckQuota(QuotaRequest) returns (QuotaResponse);
  // 上报审计事件
  rpc EmitAudit(AuditEvent) returns (Ack);
}
```

### 11.4 Go 后端调用示例（概念）

```go
// Go 侧 dev 模块触发编码 Agent
resp, err := agentRuntimeClient.StartTask(ctx, &arv1.StartTaskRequest{
    ProjectSpaceId: req.ProjectSpaceID,
    Objective:      fmt.Sprintf("实现需求 %s 的编码", req.RequirementID),
    TaskType:       "code",
    TriggeredBy:    userID,
    PermissionToken: token,        // workspace 模块签发
    TraceId:        traceID,
    AutonomyLevel:  2,             // L2 需审批
})
// 异步监听事件流
stream, _ := agentRuntimeClient.StreamEvents(ctx, &arv1.StreamEventsRequest{TaskId: resp.TaskId})
for {
    ev, err := stream.Recv()
    // 处理 GATE_REACHED → 通知 gate 模块；DONE → 落库 + audit
}
```

---

## 12. 与规则引擎、闸门的集成

### 12.1 与 Go `rule` 模块的集成

```
Plan 生成阶段
  │
  ▼
Orchestrator 调 Go rule.FetchRules(project_space_id, task_type)
  │ 返回适用的 Rule/Process/Knowledge ID 清单
  ▼
注入 PlanNode.guard_rules + AgentState.citations
  │
  ▼
产出阶段：Orchestrator 调 Go rule.Validate(artifact, rule_ids)
  │ 返回合规/不合规 + 原因
  ▼
不合规 → Guard 拦截 + 留痕
```

⭐ 规则库的**唯一真源在 Go `rule` 模块**，agent-runtime 不内置规则副本，避免双写不一致。每次校验实时调 Go。

### 12.2 与 Go `gate` 模块的集成

```
Agent 执行到 Plan 中的 gate 节点
  │
  ▼
LangGraph interrupt_before（暂停）
  │
  ▼
agent-runtime 发 TaskEvent(GATE_REACHED, gate_id=G3)
  │
  ▼
Go 后端收到 → gate 模块创建审批实例 → 通知人工
  │
  ▼
人工审批（Go gate 完成）→ Go 调 agent-runtime.ResumeAtGate(task_id, decision)
  │
  ▼
LangGraph 恢复：approved → 继续；rejected → 终止 + 回滚
```

### 12.3 集成矩阵

| 场景 | agent-runtime 行为 | Go 模块 |
|---|---|---|
| 取适用规则 | gRPC FetchRules | rule |
| 校验产出合规 | gRPC Validate | rule |
| 到达闸门 | 暂停 + 发事件 | gate（创建审批） |
| 闸门审批回调 | ResumeAtGate | gate |
| 破坏性工具调用 | 挂起 | gate + 业务模块 |
| 写业务数据 | gRPC ExecuteTool | 对应业务模块 |
| 上报审计 | gRPC EmitAudit | audit |
| 查配额 | gRPC CheckQuota | workspace |
| 写记忆元数据 | gRPC Memory（Go 侧落库） | knowledge |

---

## 13. 数据模型（PostgreSQL）

记忆与 Agent 相关持久化在 PG。agent-runtime 经 gRPC 读写，Go 侧 `knowledge`/`dev` 模块管理元数据。

### 13.1 Agent 注册表 `agent_runtime.agents`

```sql
CREATE TABLE agent_runtime.agents (
    agent_id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_type        VARCHAR(64) NOT NULL UNIQUE,
    display_name      VARCHAR(128) NOT NULL,
    description       TEXT,
    system_prompt_tpl TEXT NOT NULL,            -- 模板（含变量插槽）
    default_tools     JSONB NOT NULL DEFAULT '[]',
    allowed_scopes    JSONB NOT NULL DEFAULT '[]',
    default_model_tier VARCHAR(16) NOT NULL,    -- low/mid/high
    supported_autonomy JSONB NOT NULL,          -- [0,1,2,3]
    input_schema      JSONB NOT NULL,
    output_schema     JSONB NOT NULL,
    max_concurrent    INT NOT NULL DEFAULT 5,
    version           VARCHAR(32) NOT NULL,
    enabled           BOOL NOT NULL DEFAULT TRUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by        UUID NOT NULL
);
```

### 13.2 编排任务 `agent_runtime.tasks`

```sql
CREATE TABLE agent_runtime.tasks (
    task_id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_space_id  UUID NOT NULL,            -- ⭐ 多租户路由键
    objective         TEXT NOT NULL,
    task_type         VARCHAR(64) NOT NULL,
    plan_id           UUID NOT NULL,
    plan_dag          JSONB NOT NULL,           -- Plan DAG 完整结构
    status            VARCHAR(24) NOT NULL,     -- PENDING/RUNNING/BLOCKED/SUCCEEDED/FAILED/CANCELLED
    triggered_by      UUID NOT NULL,
    permission_token  VARCHAR(256) NOT NULL,
    autonomy_level    SMALLINT NOT NULL,
    trace_id          VARCHAR(128) NOT NULL,
    token_used        BIGINT NOT NULL DEFAULT 0,
    cost_cents        BIGINT NOT NULL DEFAULT 0,
    started_at        TIMESTAMPTZ,
    finished_at       TIMESTAMPTZ,
    error_detail      JSONB,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON agent_runtime.tasks (project_space_id, status);
CREATE INDEX ON agent_runtime.tasks (trace_id);
```

### 13.3 工具调用留痕 `agent_runtime.tool_invocations`

```sql
CREATE TABLE agent_runtime.tool_invocations (
    invocation_id     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id           UUID NOT NULL REFERENCES agent_runtime.tasks(task_id),
    project_space_id  UUID NOT NULL,
    node_id           VARCHAR(64) NOT NULL,
    tool_id           VARCHAR(64) NOT NULL,
    agent_type        VARCHAR(64) NOT NULL,
    input_summary     JSONB NOT NULL,           -- 输入摘要（脱敏）
    output_summary    JSONB,                    -- 产出摘要
    status            VARCHAR(16) NOT NULL,     -- OK/DENIED/RATE_LIMITED/GATE_BLOCKED/ERROR
    is_destructive    BOOL NOT NULL,
    gate_id           VARCHAR(8),               -- 若经闸门，记录 G1-G6
    duration_ms       INT,
    trace_id          VARCHAR(128) NOT NULL,
    invoked_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON agent_runtime.tool_invocations (task_id);
CREATE INDEX ON agent_runtime.tool_invocations (project_space_id, invoked_at);
```

### 13.4 记忆库

```sql
-- L2 项目记忆（按 project_space_id 隔离）
CREATE TABLE knowledge.project_memories (
    memory_id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_space_id  UUID NOT NULL,
    memory_type       VARCHAR(32) NOT NULL,     -- fact/decision/convention/lesson
    title             VARCHAR(256) NOT NULL,
    content           TEXT NOT NULL,
    embedding         vector(1024),             -- pgvector
    scope             VARCHAR(64),              -- 适用 Agent 类型
    source_task_id    UUID,                     -- 来源任务（可追溯）
    citations         JSONB,                    -- 引用的规则/知识
    valid_until       TIMESTAMPTZ,              -- 失效时间
    superseded_by     UUID,                     -- 被哪条替代
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by        UUID NOT NULL
);
CREATE INDEX ON knowledge.project_memories USING ivfflat (embedding vector_cosine_ops);
CREATE INDEX ON knowledge.project_memories (project_space_id, memory_type);

-- L3 企业记忆（跨项目）
CREATE TABLE knowledge.enterprise_memories (
    memory_id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_type        VARCHAR(16) NOT NULL,     -- Rule/Process/Knowledge
    title             VARCHAR(256) NOT NULL,
    content           TEXT NOT NULL,
    embedding         vector(1024),
    scope_tags        JSONB,
    published_at      TIMESTAMPTZ,
    valid_until       TIMESTAMPTZ,
    superseded_by     UUID,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON knowledge.enterprise_memories USING ivfflat (embedding vector_cosine_ops);
```

🟥 `project_memories` 强制带 `project_space_id`，且向量检索 SQL 硬性 `WHERE project_space_id = $1`——多租户隔离在数据层兜底。

---

## 14. 可观测与降级

### 14.1 指标（Prometheus）

| 指标 | 说明 |
|---|---|
| `agent_task_total{status,project_space}` | 任务计数 |
| `agent_task_duration_seconds` | 任务耗时分布 |
| `agent_guard_violations_total{type}` | 五约束违规计数 |
| `agent_tool_calls_total{tool,status}` | 工具调用 |
| `agent_token_usage_total{model,tier}` | token 消耗 |
| `agent_model_fallback_total` | 模型故障切换次数 |

### 14.2 降级策略

| 故障 | 降级动作 |
|---|---|
| LiteLLM 主模型不可用 | 切 fallback 模型 |
| 所有模型不可用 | 任务标记 FAILED → Go 后端转人工任务 |
| agent-runtime 整体不可用 | Go 后端直接转人工流程（平台仍可用，仅失 AI 能力） |
| 任务超 max_steps | 终止 + 升级人工 + 留痕 |
| Guard 频繁拦截 | 暂停该任务 + 安全告警 |

⭐ **AI 故障不等于平台故障**：agent-runtime 挂掉时，Go 后端降级为"纯人工模式"，业务连续性不受影响。这是 Go 主业务、Python 辅 AI 架构的韧性优势。

---

## 15. 关键设计取舍（ADR 摘要）

| 决策 | 选择 | 理由 | 后果 |
|---|---|---|---|
| 编排引擎 | LangGraph | 状态机/检查点/中断恢复成熟，适配闸门暂停 | 需团队熟悉 |
| 模型路由 | LiteLLM | 多供应商统一接口、故障切换、成本统计 | 多一层抽象 |
| 状态位置 | agent-runtime 无状态，状态在 PG/Redis | 水平扩缩、故障恢复 | 需良好 checkpointer |
| 规则真源 | 唯一在 Go rule，不内置副本 | 避免双写不一致 | 每次校验多一跳 gRPC（可接受） |
| 五约束强制 | 运行时 Guard 每步校验，非事后 | 真正可控 | 轻微性能开销 |
| 权限模型 | 短期令牌（scope+expiry） | 最小权限、可吊销 | 令牌签发/校验开销 |

---

*本文档定义 agent-runtime 的核心机制；具体 Agent（需求/编码/评审/运维）的 prompt 与子图细节见各板块详细设计。*

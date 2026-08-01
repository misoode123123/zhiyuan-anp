# opencode 导入适配 设计（spec）

| 日期     | 2026-08-01                                                                                                                                          |
| -------- | --------------------------------------------------------------------------------------------------------------------------------------------------- |
| 类型     | 设计 / spec                                                                                                                                         |
| 状态     | **待审核**（方案先成文审核，通过后 plan→实现）                                                                                                      |
| 路线位置 | 中期①（**替代已弃的规则 Analyzer**——规则中间层扫错会误导 opencode，错上加错）                                                                       |
| 上游     | [企业商业 OS 方案](../../企业AI原生研发平台方案.md) 板块 2/7/8、[多形态应用研发 PRD](../../PRD/2026-08-01-多形态应用治理与开发运维统筹-PRD.md) §4.4 |

---

## 1. 目标

导入项目后，由 **opencode/claude（经 `CodingAgent`）适配应用**，使其能在 ANP 部署运行——**改应用代码**（config 走 env、修 Dockerfile、调端口）**+ 必要时经 ANP 控制面受控供给缺失服务**（部署机没有的 Redis/Milvus 等）。opencode 读 repo + `AGENTS.md`（ANP 上下文）**自己分析+改**，不依赖易错的规则中间层。

## 2. 为什么是 opencode（不是规则引擎）

- ANP 已往每个仓库写 `AGENTS.md`（`RefreshAgentsMD`）= **ANP 上下文**；opencode 读它就知道 ANP 的部署模型与适配规范，按规范改即可。
- 规则中间层（曾实现的 `Analyzer`，扫 `DeployAnalysis`）**已弃**：它扫错一条会误导 opencode **错上加错**；而若 opencode 还要自己复核扫描，则该层多余。→ **纯 opencode**。
- opencode 是 agentic + 有 shell 能力：能改代码，也能（**经 ANP 控制面**）操作部署节点装服务。
- ANP 本身**已管理所有部署服务器**（deploy-node 子系统 + `ProvisionNode` + `SSHExecutor` + `ServerMonitor`）→ opencode 操作节点 = 经 ANP 既有能力，**不裸连、不新造通道**。

## 3. ANP 上下文：`AGENTS.md` 加「ANP 部署适配规范」段

`RefreshAgentsMD` 在适配前刷新，把下面规范写进 `AGENTS.md`（opencode 据此适配，不靠猜）：

- **env-over-config**：应用 config **必须优先读环境变量**（ANP 注入 `DATABASE_URL`/`REDIS_ADDR`/`MILVUS_ADDR`/`PG_*` 等）；**禁止硬编码 `127.0.0.1`/`localhost` 访问中间件**（容器内够不到）。
- **构建**：仓库根须有 Dockerfile（多阶段）、`EXPOSE` 应用监听端口、构建上下文 = 仓库根（源码在根，已 flatten）。
- **依赖**：声明依赖 → ANP 供给/绑定 → 注入连接 env；应用读 env 连接。
- **网络**：默认 bridge（隔离）；`host` 网络需审批、仅特定场景；优先改 config 走 env 而非开 host。
- **形态**：`web`（HTTP+端口+URL）/ `headless`（bot/worker，无 URL，健康=进程/外连）。
- **部署环境适配**：若部署机缺某依赖服务，**先报缺什么**；**经审批后**调 ANP 受控执行在部署机装标准中间件（白名单内）；连接信息回填注入应用 env；全程留痕、可回滚。

## 4. 适配流程

```
导入完成
  → 刷新 AGENTS.md（含「ANP 部署适配规范」）
  → 触发 opencode 适配（CodingAgent.Submit，prompt = 适配规范 + "把这个应用适配成能在 ANP 部署"）
       │  opencode 读 repo + AGENTS.md → 改 config 走 env / 修 Dockerfile / 调端口
       │  若部署机缺服务 → 经 ANP 控制面（ProvisionNode/SSHExecutor）受控装（白名单+审批）
       │  连接信息回填 → 注入应用 env
       ▼
  产出 commit + 变更请求（app 改动 L2 / 节点改动 L3）
  → 审批 → 构建 → 部署 → 健康检查（按形态）→ 运行
```

## 5. 实现件（其余复用现有）

### 5.1 `AGENTS.md` 适配规范写入

扩 `RefreshAgentsMD`（`internal/standard`）：追加「ANP 部署适配规范」段（§3 内容）。全局 + 项目级均可。

### 5.2 导入后触发 opencode 适配

导入完成处（`ImportFromZip/Dir/Git` 之后）异步调 `CodingAgent.Submit(ctx, psID, userID, "adapt", appID, repoDir, 适配prompt, "")`（`internal/dev/coding.go`，`opencode run --auto --dir <repo>`）。CodingAgent 跑完自动 `gitCommit` + 登记变更请求 → 进审批。HTTP 立即返回，不阻塞导入。

### 5.3 经 ANP 控制面供给缺失服务

opencode 在适配中发现部署机缺服务时，**经 ANP 受控执行**（复用 `ProvisionNode`/`SSHExecutor` 思路）：在目标节点装标准中间件容器 → 连接信息回填 → 注入应用 env。

- **v1（务实）**：opencode 在变更请求里**报缺**（结构化：`{kind,node,reason}`）；ANP/人据此经 `ProvisionNode`（既有）审批后供给；连接回填后 opencode 再把应用 config 改为读 env。
- **v2（自动）**：ANP 暴露一个受控"装中间件"MCP 工具/端点（白名单+审批+审计），opencode 直接调；连接自动回填。**经 ANP，不走裸 SSH**。

## 6. 治理（关键——能力有，口子只有一个、且受治）

呼应早先"权限/风险"担忧 + OS 分级自治：

| 项                | 要求                                                                                                                         |
| ----------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| **审批分级**      | app 代码改动 = **L2 审批**；部署节点改动（装服务）= **L3 双确认**；均不自动放行 prod                                         |
| **经 ANP 不裸连** | opencode 永远经 ANP 控制面操作节点，**不直接 SSH 任何服务器**                                                                |
| **白名单**        | 节点上只允许装标准中间件（redis/milvus/pg/mysql/mongo/kafka/es/minio/...）；禁破坏性操作（删数据/改系统关键配置/开高危端口） |
| **审计**          | opencode 经 ANP 执行的每条命令留痕（谁/何时/改了什么），可追溯                                                               |
| **可回滚**        | 装的服务能卸；应用改动走版本/回滚                                                                                            |

## 7. 复用（不新造）

`CodingAgent.Submit`（编码+提交+变更请求）、变更闸门（`HasApproved`）、`RefreshAgentsMD`（AGENTS.md）、`ProvisionNode`+`SSHExecutor`（节点供给）、`DATABASE_URL` 注入范式（扩到 `REDIS_ADDR` 等）、部署权限分离 RBAC、配额。

## 8. 范围与非目标

**做**：AGENTS.md 适配规范；导入后触发 opencode 适配（改应用代码）；缺失服务经 ANP 受控供给（v1 报缺+审批供给，v2 自动）。

**不做**（本期）：规则 Analyzer（已弃）；自动放行 prod；opencode 裸 SSH 节点；非白名单服务安装。

## 9. 验收（客服机器人）

重新导入客服机器人 → 刷新 AGENTS.md → opencode 按"适配规范"把 `config.yaml` 的 redis/milvus/pg `127.0.0.1` 改为读 env（或部署机地址）、补/修 Dockerfile → 变更审批 → ANP 构建部署 → bridge 容器经宿主 IP 连上 redis/milvus/pg → 连上企微、running。可演示"缺服务 → opencode 报缺 → 审批 → ANP 在节点装 → 回填 env → 跑通"。

## 10. 待决议

1. **触发时机**：导入即自动触发 opencode 适配，还是应用详情点"适配"按钮手动触发？（建议：导入后自动触发一次 + 详情可重试）
2. **缺失服务供给 v1 vs v2**：先做"opencode 报缺 + ANP/人审批后 `ProvisionNode` 供给"（稳），还是直接做"受控 MCP 工具 opencode 自动装"？（建议 v1 先行）
3. **白名单范围**：标准中间件清单 + 是否允许装系统包（如 nginx）？
4. **AGENTS.md 适配规范**放全局还是项目级可覆盖？（建议全局默认 + 项目级覆盖）

---

_审核通过后，进 writing-plans 出实现计划（按 5.1/5.2/5.3 分任务，TDD + 客服机器人验收 + .28 验证）。_

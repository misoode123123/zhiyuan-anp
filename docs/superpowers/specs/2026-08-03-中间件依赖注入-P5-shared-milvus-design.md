# 中间件依赖注入 P5 设计 —— shared 共享 Milvus（collection 前缀隔离）

- **类型**：方案 / 详细设计（P1 设计 §10 分期里的 P2 阶段的 milvus 补齐；P2 已完成 redis shared）
- **状态**：待审核（按「方案先成文审核」，审核通过后再开 plan → 实现）
- **日期**：2026-08-03
- **作者**：miscode + Claude
- **关联模块**：`mwsupply`（shared 流程按 kind 分派 + milvus 前缀生成/注入）、`appdeploy`（不改主流程）
- **关联文档**：
  - [中间件依赖供给与注入 设计（P1 总设计）](2026-08-01-中间件依赖供给与注入-design.md) —— 本文件是其 §10「P2：shared 共享实例」的 milvus 细化
  - [P2 shared redis 设计](2026-08-01-中间件依赖注入-P2-shared-redis-design.md) —— 同一 mwsupply 包的前一阶段（共享 redis + db 号隔离），本文件延续其范式并按 milvus 特性扩展
  - [P4 dedicated milvus 设计](2026-08-02-中间件依赖注入-P4-dedicated-milvus-design.md) —— 同一 milvus kind 的 dedicated 形态（专属三容器栈），与本 shared 形态对照
  - [多形态应用治理 PRD §4.4/§7/§8/§9](../../PRD/2026-08-01-多形态应用治理与开发运维统筹-PRD.md)
  - [应用库与 API 统一管理设计](2026-07-21-应用库与API统一管理-设计.md)（pgsupply 范式来源）

---

## 1. 背景与目标

### 1.1 P1–P4 已闭环

- **P1（bind_existing）**：`.anp/deps.yaml` 声明依赖 → 部署时 `mwsupply.Reconcile` 读清单 → 查 `appdeploy_service_instance` 注册表 → 经 `EnvWriter.UpsertEnv(source=platform)` 写 `REDIS_ADDR`/`MILVUS_ADDR` 等 → 现有 `docker run -e` 注入。`.28` live 验证通过。redis 与 milvus 的 bind_existing 均已可用（种子 `svinst-redis-28` / `svinst-milvus-28`）。
- **P2（shared redis）**：共享 redis 实例 + **db 号隔离**（token=db 号，db_range `[1,15]` 池，最小空闲号分配 + 重分配 flush + CASCADE 回收）。`.28` live 验证通过。
- **P3（dedicated redis）**：每 app 一个专属 redis 容器（端口池 9600-9699 + requirepass）+ `mwReconciler.Cleanup`。`.28` live 验证通过。
- **P4（dedicated milvus）**：`supplyDedicated` 重构为 **kind 分派**（launch/ready/env/rm 四处分派），milvus 起专属网络 + milvus/etcd/minio 三容器（1:1 复刻 .28 yxt-milvus）+ alpine 探针就绪 + Cleanup 按 kind rm。`.28` live 验证通过（commit 03c1ab1 / e2e 9249f82）。

### 1.2 P5 要补的缺口

P2 的 shared 流程是 **redis 专写**：`supplyShared`（`supply.go:101`）里 `ParseDBRange`+`pickLowestFree`（db 号池模型）、`claimWithRetry` 内的 `r.flusher.FlushDB`（redis 专属数据卫生）、`writeSharedEnv` 的 `_DB` 键——三处都是 redis db 号语义。**milvus 的 shared 分支不存在**：应用在 `.anp/deps.yaml` 声明 `kind: milvus, strategy: shared` 时，会落到 redis 专写的 shared 流程里（`ParseDBRange` 解析 milvus 实例的 `{"mode":"prefix"}` 失败 → `mkBind(failed, "shared 实例 isolation 缺 db_range")`，行为=不可用）。

注：000028 建表时 `isolation` 注释即写明「shared 用：redis db_range / **milvus prefix**」，`model.go:51` `IsolationToken` 注释亦写「redis db号 / **milvus collection 前缀**」——**框架早就为 milvus shared 预留了语义位**，本设计填实。

本设计把 shared 流程**按 kind 分派**（镜像 P4 对 dedicated 的重构），补齐 **milvus shared**。补完后 milvus 三策略（bind_existing / shared / dedicated）全齐。

### 1.3 目标

应用在 `.anp/deps.yaml` 声明 `kind: milvus, strategy: shared` → 平台从共享 milvus 实例给它分配一个**独占 collection 前缀**（如 `app1a2b3c4d5e6f_`）→ 注入 `MILVUS_ADDR`（共享实例地址）+ `MILVUS_COLLECTION_PREFIX`（该 app 的前缀）→ 多个 app 共用一台 milvus 但靠前缀互不撞名；删 app 自动回收前缀。

**与 redis shared 的本质区别**：redis db 号隔离是**透明**的（app redis 客户端 `SELECT N` 即隔离，零代码配合）；milvus collection 前缀隔离**不透明**——**app 必须读 `MILVUS_COLLECTION_PREFIX` 并给所有 collection 的 create/insert/search/drop 加前缀**。这是「collection 前缀隔离」的固有代价（透明隔离留给后续「milvus 鉴权」项：native database + RBAC）。

**与 P4 dedicated milvus 的对照**：dedicated 每 app 起专属三容器栈（强隔离、重、~2-3GB）；shared 多 app 共用一台 milvus（轻、靠前缀逻辑隔离）。shared **不起任何容器**（复用已运行的共享 milvus），故**无 docker / 无就绪检测 / 无 main.go wiring**——比 P4 轻得多（≈ P4 改动量的 1/3）。

---

## 2. 范围

### 2.1 本期做（in）

- milvus shared 全链路：共享实例种子 + collection 前缀分配 + 双 env 注入（`MILVUS_ADDR` + `MILVUS_COLLECTION_PREFIX`）+ 删 app 回收（CASCADE）
- 把 `supplyShared` 从 redis 专写**重构为按 kind 分派**（分配+claim / 写 env 两处分派），**redis 路径行为逐字保留**（既有 redis shared 单测零回归护栏）

### 2.2 本期不做（out / YAGNI）

- **milvus 鉴权 / native database + RBAC**：透明隔离（每 app 一个 milvus database + scoped user，免 app 加前缀），需 Go milvus 客户端或 REST + 版本/auth 验证，是更大的设计（即 PRD §7 剩余项之一），独立 spec
- **残留 collection 清理（前缀级 flush）**：删 app 后其前缀下的 collection 留在 milvus（无前缀级 FLUSHDB 原语，需 milvus 客户端；pymilvus 在 .28 被 numpy/X86_V2 阻断）。因前缀不复用，残留只占存储不撞名 → v1 接受 + 文档化，清理由鉴权项 / janitor 补
- **dedicated / bind_existing milvus**：P1/P4 已闭环，不动
- **项目级 shared 池**：本期 shared 实例=平台级（`project_space_id IS NULL`）；未来按项目空间分池再扩 `LookupShared` 的 scope（同 P2 redis shared 留的口）
- **UI 勾选 / deps HTTP API**：本期仍由 `.anp/deps.yaml`（opencode 适配回写或手编）驱动；UI 勾选器独立 spec
- **配额模块接入**：前缀天然无限（随机生成），无「池满」概念，不动 `internal/quota`
- **vault/KMS 密钥管理**：v1 无 auth 无 secret；未来鉴权时接（同 pgsupply I1 债）
- **监控/备份/迁移**：shared milvus 的指标与备份不在本期

---

## 3. 关键决策

| 维度       | 选择                                                                                    | 理由                                                                                                                                                                       |
| ---------- | --------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 隔离机制   | **collection 前缀**（共享实例 + 每 app 独占一个前缀串）                                 | milvus 无 redis 那种原生「多库 SELECT」透明隔离；前缀是最轻、零新依赖的隔离手段。000028/model.go 注释早预留「milvus prefix」语义                                           |
| 隔离强度   | **逻辑隔离（需 app 配合加前缀），非透明**                                               | 与 redis shared（透明 SELECT N）的本质差异；透明隔离 = 后续 native database+RBAC（鉴权项）。v1 接受 app 配合契约                                                           |
| token 生成 | **`app<12hex>_` 随机**（复用 `genShortID` 的 crypto/rand）                              | 无穷空间、碰撞概率 ~0；首字符字母、仅 `[a-zA-Z0-9_]`、合 milvus collection 名规则；与 dedicated 容器名 `mwmilvus-<short>` 同款 shortid 范式                                |
| token 复用 | **不复用**（每次新 app 生新随机前缀）                                                   | 删 app 后其残留 collection 永不与未来 app 撞名（即便不清理）；「回收」= binding 行 CASCADE → token 从占用集移除，但物理上前缀不重发                                        |
| 并发控制   | **随机生成 + 部分唯一索引兜底 + 有界重试（≤4）**                                        | 随机 12-hex 碰撞概率 ~0；`uq_svbind_inst_token`（000029）兜底极罕见的并发撞号；无需 redis 那种「最小空闲号 + 池大小重试」（无有限池）                                      |
| 数据卫生   | **不清理残留 collection（v1）**                                                         | 无前缀级 flush 原语（需 milvus 客户端）；pymilvus 在 .28 被 numpy 阻断。前缀不复用 → 残留不撞名，仅占存储。比 redis flush 更弱（redis flush 在 .28 本就 best-effort 跳过） |
| 共享实例   | **复用 `.28:19530 yxt-milvus`**（与 bind_existing 种子 `svinst-milvus-28` 同机）        | 与 P2 redis shared 复用 `.28:6381 yxt-redis`（bind_existing `svinst-redis-28` 同机）**同范式**；不起新容器，零运维                                                         |
| env 注入   | **`MILVUS_ADDR` + `MILVUS_COLLECTION_PREFIX`**，均 `source=platform`                    | `MILVUS_ADDR` 与 bind_existing/dedicated 同 key（app 读法不变）；`MILVUS_COLLECTION_PREFIX` 是 shared 专有键。无 password（v1 无 auth）                                    |
| 回收       | **删 app 靠 `ON DELETE CASCADE` 自动回收**，**Delete handler 零改**                     | 同 P2 redis shared：token 占用集=存活 binding 行，删 app → binding CASCADE → 前缀从占用集移除；env 行同样 CASCADE 清。无容器故无 Cleanup docker rm（与 P4 dedicated 不同） |
| 泛化       | **`supplyShared` 骨架按 kind 分派**（分配+claim / 写 env 两处分派），redis 路径逐字保留 | 单一 shared 入口，避免并行函数重复 + 漂移；现有 redis shared 单测零回归保护（同 P4 dedicated 重构手法）                                                                    |

---

## 4. 数据模型

### 4.1 复用，不改表结构

000028 已建（P2 已写 redis，milvus 列位早就绪）：

```sql
appdeploy_service_instance.isolation JSONB        -- shared 实例存隔离配置：redis {"db_range":[1,15]} / milvus {"mode":"prefix"}
appdeploy_service_instance.supply_mode TEXT       -- 'shared'
appdeploy_service_binding.isolation_token TEXT    -- redis=db号("7") / milvus=前缀("app1a2b3c4d5e6f_")
```

000029 已建部分唯一索引 `uq_svbind_inst_token`（`WHERE isolation_token IS NOT NULL`）——**直接复用**：milvus token=前缀串，两 app 前缀不同即不冲突，**无需新索引**。

P5 只**第一次写 milvus 的 shared 种子**，不改 DDL。

### 4.2 新增迁移 `000033_mwsupply_shared_milvus.up.sql` / `.down.sql`

> 版本号 000033：已确认 `migrations/pg/` 当前最大 000032（`000031_appdeploy_fk_cascade` / `000032_appdeploy_headless_health`），000033 空闲。

```sql
-- 000033_mwsupply_shared_milvus.up.sql
-- P5：shared 共享 milvus（collection 前缀隔离）
-- ① 种子一条平台级 shared milvus 实例（复用 .28 同一台 yxt-milvus，每 app 独占一个 collection 前缀）
-- ② 唯一索引 uq_svbind_inst_token（000029 已建）直接复用——token=前缀串，两 app 前缀不同不冲突，无需新索引

INSERT INTO appdeploy_service_instance
  (id, project_space_id, kind, name, supply_mode, host, port, auth_ref, isolation, status)
VALUES
  ('svinst-milvus-shared-28', NULL, 'milvus', 'yxt-milvus-shared', 'shared',
   '10.10.0.28', 19530, NULL, '{"mode":"prefix"}'::jsonb, 'active')
ON CONFLICT (id) DO NOTHING;
```

```sql
-- 000033_mwsupply_shared_milvus.down.sql
DELETE FROM appdeploy_service_instance WHERE id = 'svinst-milvus-shared-28';
```

> **`auth_ref=NULL`**：v1 无 auth（同 P4 dedicated milvus v1、同 .28 yxt-milvus 现状）。鉴权项接入时补 root 密码 + 更新种子 `auth_ref`。
>
> **`isolation={"mode":"prefix"}`**：仅人读自文档（表名 + 注释已说明是前缀隔离）；v1 代码**不解析**它——前缀总是随机生成（无池配置可读）。留键为未来扩展（如可配前缀模板）。
>
> **`host=10.10.0.28 port=19530`**：与 bind_existing 种子 `svinst-milvus-28` 同机同端口（同 P2 redis shared 复用 yxt-redis 范式）。bind_existing app（无前缀，db 概念上的「默认命名空间」）与 shared app（带前缀）共用同一物理 milvus——理论上 bind_existing app 若手工建了 `app<short>_xxx` 名的 collection 会撞 shared 前缀，但 bind_existing 是导入应用自管理场景、碰撞概率极低，同 P2 redis 的 db 0（bind_existing）与 db 1-15（shared）共用同机的等价情形。

---

## 5. 供给流程：`supplyShared` 重构为 kind 分派

### 5.1 骨架（共享，kind 无关）

把现 `supplyShared`（`supply.go:101`，redis 专写）重构为：`LookupShared` / 复用判定 / binding 落库 **共享**，**分配+claim** 与 **写 env** 两处按 `dep.Kind`（=`inst.Kind`）分派。

```
supplyShared(dep{kind, strategy:shared}):
  inst = store.LookupShared(kind)                         # 已通用（按 kind 过滤），不动
  if inst == nil:
      mkBind(failed, "", "无 shared "+kind+" 实例"); return

  # —— 复用判定（幂等：同 app 重部署不换前缀、不重写 claim、保数据）—— 【共享】
  existing = store.GetBinding(appID, kind)
  if existing != nil && existing.status==bound
     && existing.isolation_token != "" && existing.service_instance_id==inst.ID:
      writeSharedEnv(kind, inst, existing.isolation_token)  # kind-aware
      mkBind(bound, inst.ID, existing.isolation_token, "")
      return

  # —— 新分配 + claim（按 kind 分派）——
  token, err = allocAndClaimShared(kind, appID, psID, inst)
  if err != nil:
      mkBind(failed, inst.ID, "", err.Error()); return

  writeSharedEnv(kind, inst, token)                         # kind-aware
  mkBind(bound, inst.ID, token, "")
```

`allocAndClaimShared` 与 `writeSharedEnv` 即按 kind 的分派函数（内部 `switch kind`）。

### 5.2 `allocAndClaimShared` —— 分派

```go
func (r *Reconciler) allocAndClaimShared(ctx, appID, psID, kind, inst) (token string, err error) {
    switch kind {
    case "milvus":
        return r.allocMilvusPrefix(ctx, appID, psID, kind, inst)
    default: // redis
        return r.allocRedisDB(ctx, appID, psID, kind, inst)
    }
}
```

**redis 分支 `allocRedisDB`（逐字=现状，零回归）**：把现 `supplyShared` 新分配段的 `ParseDBRange` + `pickLowestFree` + `claimWithRetry` 三步原样搬入，错误以 `error` 返回（`supplyShared` 统一 `mkBind(failed, inst.ID, "", err.Error())`，错误串与现状逐字一致）：

```go
func (r *Reconciler) allocRedisDB(ctx, appID, psID, kind, inst) (string, error) {
    lo, hi, ok := ParseDBRange(inst.Isolation)
    if !ok { return "", fmt.Errorf("shared 实例 isolation 缺 db_range") }
    allocated, _ := r.store.AllocatedTokens(ctx, inst.ID)
    first, found := pickLowestFree(lo, hi, allocated)
    if !found { return "", fmt.Errorf("shared redis db 号耗尽（池 %d-%d）", lo, hi) }
    return r.claimWithRetry(ctx, appID, psID, kind, inst, lo, hi, first, allocated)
}
```

> `claimWithRetry`（`supply.go:138`）**不动**——仍只被 redis 分支调（内部 flush + 有界重试，redis 专属）。flush best-effort 语义不变（.28 backend 拨不到 redis 时记 Warn 跳过）。

**milvus 分支 `allocMilvusPrefix`（新）**：生成唯一随机前缀 + 单次 `ClaimSharedToken`（**无 flush、无有限池**）；极罕见的并发撞号（`uq_svbind_inst_token` 抛 23505）换号重生，有界 ≤4 次：

```go
func (r *Reconciler) allocMilvusPrefix(ctx, appID, psID, kind, inst) (string, error) {
    allocated, _ := r.store.AllocatedTokens(ctx, inst.ID)
    taken := make(map[string]bool, len(allocated))
    for _, t := range allocated { taken[t] = true }
    for attempts := 0; attempts < 4; attempts++ {            // 碰撞概率 ~0，4 次冗余
        token := genMilvusPrefix()                            // app<12hex>_
        if taken[token] { continue }                          // 本地撞（极罕见），跳过换号
        err := r.store.ClaimSharedToken(ctx, appID, psID, kind, inst.ID, token, EnvKeyFor(kind))
        if err == nil { return token, nil }
        if !isUniqueViolation(err) { return "", err }         // 非冲突，真错
        taken[token] = true                                   // 并发撞，换号重生
    }
    return "", fmt.Errorf("milvus 前缀分配重试用尽（并发撞号）")
}
```

> 无需 redis 那种「池大小」量级的重试上限——前缀空间 ~16^12，本地+并发碰撞同时发生的概率可忽略；4 次是纯防御冗余。

### 5.3 `writeSharedEnv` —— 分派

```go
func (r *Reconciler) writeSharedEnv(ctx, appID, inst, token) {
    switch inst.Kind {
    case "milvus":
        _ = r.env.UpsertEnv(ctx, appID, "MILVUS_ADDR", ConnStr(inst), false, "platform")
        _ = r.env.UpsertEnv(ctx, appID, "MILVUS_COLLECTION_PREFIX", token, false, "platform")
    default: // redis（逐字保留）
        kindUp := strings.ToUpper(inst.Kind)
        _ = r.env.UpsertEnv(ctx, appID, kindUp+"_ADDR", ConnStr(inst), false, "platform")
        _ = r.env.UpsertEnv(ctx, appID, kindUp+"_DB", token, false, "platform")
        if inst.AuthRef != "" {
            _ = r.env.UpsertEnv(ctx, appID, kindUp+"_PASSWORD", inst.AuthRef, true, "platform")
        }
    }
}
```

> milvus 不写 `MILVUS_PASSWORD`（v1 无 auth）、不写 `MILVUS_DB`（无 db 号语义，前缀是独立 env 行）。
> `ConnStr`（`connstr.go:20`）返回 `host:port`、`EnvKeyFor`（`connstr.go:6`）milvus→`MILVUS_ADDR` 均已就位，**不改**。

### 5.4 状态机（binding.status）

同 P2 redis shared：

```
（无 binding）──新分配──▶ bound          // 生成前缀 + claim 直接落 bound（无 flush 步）
bound ──重部署──▶ bound（复用前缀，不重生、不 claim）   // 幂等
（任意）──无 shared 实例/claim 失败/重试用尽──▶ failed（token 不 claim / 留空）
failed ──重部署──▶ 重新走「新分配」        // 失败 binding 的 token 恒空，重试生新前缀
```

无 `declared`/`allocating` 中间态（同 P2：成功直接 bound，失败直接 failed）。milvus 无就绪检测步骤（共享实例已运行，不起容器），故无 best-effort 降级分支（与 P4 dedicated 不同）。

---

## 6. App 配合契约（与 redis shared 的核心差异，须文档化）

**redis shared 透明**：app redis 客户端连 `REDIS_ADDR` 后 `SELECT $REDIS_DB` 即隔离，**零业务代码改动**。

**milvus shared 不透明**：app 必须主动读 `MILVUS_COLLECTION_PREFIX` 并给**所有** collection 操作加前缀：

```python
# app 侧伪代码（pymilvus 示例）
import os
from pymilvus import MilvusClient
client = MilvusClient(uri=os.environ["MILVUS_ADDR"])          # 10.10.0.28:19530
prefix = os.environ["MILVUS_COLLECTION_PREFIX"]               # app1a2b3c4d5e6f_

client.create_collection(collection_name=prefix+"docs", ...)  # 所有 collection 名都拼前缀
client.insert(collection_name=prefix+"docs", data=[...])
client.search(collection_name=prefix+"docs", ...)
client.drop_collection(collection_name=prefix+"docs")
```

- **契约**：app 对 milvus 的每一次 collection 级操作（create / insert / search / delete / drop / describe）的 collection_name 都必须是 `prefix + 业务名`。
- **隔离效果**：两 shared app 前缀不同（`appAAA_` / `appBBB_`）→ collection 名空间不交 → 互不可见、互不撞名。
- **违约后果**：app 若忘加前缀，其 collection 会落到「裸命名空间」，可能与其他违约 app 或 bind_existing app 撞名——是 app 侧 bug，平台不强制（v1 无 RBAC 约束能力）。
- **透明隔离的出路**：后续「milvus 鉴权」项（native database + RBAC user scoped to database）可让 app 连进来就只看到自己的库，免加前缀——届时本契约可废弃。

> 本契约写入 spec + 应用适配规范（opencode 适配 / 应用 README 模板须提示读 `MILVUS_COLLECTION_PREFIX`）。

---

## 7. 回收：CASCADE 自动回收，Delete handler 零改

同 P2 redis shared（**与 P4 dedicated 不同——shared 无容器，无 Cleanup docker rm**）：

- token 的「占用集合」= `appdeploy_service_binding` 的存活行（`AllocatedTokens` 查非空 `isolation_token`）。删 app 时：
  - `appdeploy_service_binding` 行靠 `app_id REFERENCES appdeploy_application(id) ON DELETE CASCADE`（000028 `up.sql:24`）**自动删** → 前缀从占用集移除
  - `appdeploy_env` 的 `MILVUS_ADDR`/`MILVUS_COLLECTION_PREFIX` 行同样 CASCADE 自动清
- **`handler.go` Delete 零改**：shared 靠 CASCADE，不走 `mwReconciler.Cleanup`（Cleanup 只处理 dedicated 容器，`supply.go:298` 的 `if b.Strategy != ModeDedicated ... continue` 已跳过 shared）。

**残留 collection（已知 gap，v1 不清理）**：删 app 后其前缀下的 collection 物理留在 milvus（CASCADE 只删平台 DB 行，不清 milvus 内对象）。与 redis flush 对照：

|                | redis shared                               | milvus shared                                           |
| -------------- | ------------------------------------------ | ------------------------------------------------------- |
| 重分配数据卫生 | `FLUSHDB`（best-effort，.28 不可达时跳过） | **无原语**（需 milvus 客户端；pymilvus .28 numpy 阻断） |
| 残留后果       | 重分配 db 号可能拿到脏数据（flush 跳过时） | 前缀不复用 → 残留 collection 永不撞名，仅占存储         |
| v1 处理        | best-effort flush                          | 接受残留 + 文档化                                       |

因前缀随机不复用，残留 collection 无撞名风险，仅累积占存储——v1 接受。清理留给「milvus 鉴权」项（届时管 database/user 生命周期，删 app 可 drop database）或独立 janitor。

---

## 8. naming.go 改动

```go
// genMilvusPrefix 生成 milvus shared collection 前缀：app<12hex>_。
// 复用 genShortID 的 crypto/rand 12-hex；'app' 首字符为字母、仅 [a-zA-Z0-9_]，合 milvus collection 名规则
// （首字符字母、仅字母数字下划线、长度 1-255）。app 给 collection 加前缀后仍合法（如 app1a2b..._foo）。
func genMilvusPrefix() string {
    return "app" + genShortID() + "_"
}
```

> 仅新增此函数。`genShortID`/`genPassword`/`allocPort`/`dedicatedContainerName`/`portRange`/milvus 常量均不变（P4 已就位）。
> 放 `naming.go`（与 `genShortID` 同文件，prefix 生成属命名范畴）；亦可单列 `prefix.go`——实现期择一。

---

## 9. 模块 / 文件改动

| 动作     | 文件                                                        | 说明                                                                                                                                                |
| -------- | ----------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- |
| 改       | `internal/mwsupply/supply.go`                               | `supplyShared` 抽 kind 分派骨架；新增 `allocAndClaimShared`/`allocRedisDB`/`allocMilvusPrefix`；`writeSharedEnv` 加 milvus 分支。redis 路径逐字保留 |
| 改       | `internal/mwsupply/naming.go`                               | 新增 `genMilvusPrefix()`                                                                                                                            |
| 新增迁移 | `migrations/pg/000033_mwsupply_shared_milvus.{up,down}.sql` | shared milvus 实例种子（仅 INSERT，无新表/无新索引）                                                                                                |
| 改测试   | `internal/mwsupply/supply_test.go`                          | milvus shared 新分配/复用幂等/无 flush/claim 失败；**redis shared 既有用例零改动须全绿**                                                            |
| 改测试   | `internal/mwsupply/naming_test.go`                          | `genMilvusPrefix` 格式（`^app[0-9a-f]{12}_$`、首字符字母）+ 大量调用唯一性                                                                          |
| **不改** | `internal/mwsupply/connstr.go`                              | `EnvKeyFor`（milvus→MILVUS_ADDR 已在）、`ConnStr`（host:port 通用）均不改                                                                           |
| **不改** | `internal/mwsupply/model.go`                                | `ModeShared`/`StatusBound`/`StatusFailed`/`IsolationToken`（注释已含 milvus 前缀）均已在                                                            |
| **不改** | `internal/mwsupply/store.go`                                | `LookupShared`（按 kind 过滤，已通用）/`GetBinding`/`AllocatedTokens`/`ClaimSharedToken` 均已在                                                     |
| **不改** | `internal/mwsupply/docker.go`                               | shared 不起容器（复用共享 milvus），无 docker 调用                                                                                                  |
| **不改** | `internal/mwsupply/isolation.go`                            | `ParseDBRange`/`pickLowestFree` 仍只服务 redis 分支；milvus 不解析 isolation                                                                        |
| **不改** | `internal/mwsupply/redisflush.go`                           | flush 仍只服务 redis；milvus 无 flush                                                                                                               |
| **不改** | `cmd/server/main.go`                                        | shared milvus 无新依赖（无 docker/无 flush/无 ready），`NewReconciler` 签名不变                                                                     |
| **不改** | `internal/appdeploy/handler.go`                             | `MWReconciler` 接口签名不变；Delete 不变（shared 靠 CASCADE，Cleanup 已跳过 shared）；`buildAndDeploy` 调用点不变                                   |

> 核心改 2 文件（supply/naming）+ 1 迁移，零 main.go、零 handler、零 docker、零新依赖。是 P4（改 supply/docker/naming + 零迁移靠复用列）之后最轻的一档。

---

## 10. 测试计划

### 10.1 PG 单测（`go test -p 1`，跑 `anp_test` 库）

> 遵循记忆 `sqlite-test-pg-type-trap` / `go-test-serial-p1`：真 PG（不 sqlite），全量回归 `-p 1` 串行；`GOPATH=C:/Users/yxt/go` 前缀。

**supply_test.go（fake flusher + 真 store/EnvWriter）**：

1. milvus shared 新分配：`.anp/deps.yaml` `{kind:milvus, strategy:shared}` → binding=bound + `MILVUS_ADDR=10.10.0.28:19530` + `MILVUS_COLLECTION_PREFIX=app<12hex>_` 两行 env（source=platform，**无 MILVUS_PASSWORD / 无 MILVUS_DB**——仅这两个 MILVUS% key，证 milvus 分支）；binding `isolation_token` 形如 `app..._`；**flusher 不被调**（milvus 无 flush）
2. 隔离：两 app 各自 shared milvus → 前缀不同（`appAAA_` ≠ `appBBB_`），`MILVUS_ADDR` 同
3. 复用幂等：同 app 二次 `Reconcile` → 前缀不变、flusher 不被调、env 重写、binding 仍 bound
4. claim 失败：fake `ClaimSharedToken` 返非唯一冲突错 → binding=failed + `last_error` 含错；返唯一冲突 4 次仍撞 → failed 含「重试用尽」
5. 无 shared 实例：`LookupShared('milvus')` nil（删种子）→ binding=failed + `last_error` 含「无 shared milvus 实例」
6. **redis shared 零回归**：既有 redis shared 用例（新分配 db 号/复用不 flush/flush best-effort/池满/无实例）全绿、行为不变；`flusher` 在 redis 分支仍被调用

**naming_test.go**：

7. `genMilvusPrefix` 格式：匹配 `^app[0-9a-f]{12}_$`；首字符 `a`（字母）；1000 次调用全唯一

### 10.2 `.28` 端到端（`deploy-28-no-local-test`）

> 本机不跑功能测试，`.28` 是测试库。commit → push origin main → scp + `.28` 重建。

1. **先验共享 milvus 在跑**：`.28` 上 `yxt-milvus`（`10.10.0.28:19530`）是否运行（`docker ps`）；若 bind_existing 种子指向但未实跑，shared 供给仍成功（只注入 env），但 app 连不上——e2e TCP 探测覆盖
2. 造两个最小 python 应用（`.anp/deps.yaml` 预写 `services:[{kind:milvus, strategy:shared}]`；**.28 无 golang 镜像缓存，用 python:3-alpine**，仿 P2/P4 e2e 范式；app 启动打印 `MILVUS_ADDR` + `MILVUS_COLLECTION_PREFIX` + 对 milvus 端口 TCP 探测）
3. 各自 CREATE（带 repo_dir，不触发 adapt）→ deploy test
4. 容器内验证：app1 `MILVUS_COLLECTION_PREFIX=appAAA_`、app2 `MILVUS_COLLECTION_PREFIX=appBBB_`（**前缀不同**），`MILVUS_ADDR=10.10.0.28:19530` 同
5. `appdeploy_env` 各有 `MILVUS_ADDR` + `MILVUS_COLLECTION_PREFIX` 两行 `source=platform`；`appdeploy_service_binding` 各一行 `strategy=shared, isolation_token=appAAA_/appBBB_, status=bound`
6. **app→milvus 可达**：app 容器内 `nc -z 10.10.0.28 19530` = `APP_TO_MILVUS_TCP_OK`（同 P4 dedicated 范式）
7. **回收**：删 app1 → 其 binding/env CASCADE 删 → 占用集少一前缀 → `appdeploy_service_binding` 不再有 app1 行
8. **平台保护**：手改 `MILVUS_COLLECTION_PREFIX` 返 409（复用 source=platform 保护）
9. **pymilvus 向量 CRUD 不测**：.28 numpy/X86_V2 老 CPU 阻断（P4 已记），e2e 只验平台「注入 + 隔离 + 可达 + 回收」，不验 app 端 CRUD（app 配合契约 §6 由 app 侧负责）

---

## 11. 风险与取舍

| 风险                                               | 对策                                                                                           |
| -------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| 隔离是逻辑前缀、靠 app 自觉加（违约可能撞名）      | 文档化契约（§6）；透明隔离留给鉴权项（native database+RBAC）                                   |
| 残留 collection 不清理、累积占存储                 | v1 接受（前缀不复用不撞名）；清理留给鉴权项 / janitor                                          |
| 随机前缀理论碰撞                                   | crypto/rand 12-hex（~16^12 空间）+ `uq_svbind_inst_token` 兜底 + 有界重试 ≤4                   |
| 共享 milvus 单点（多 app 共用一台）                | 同 P2 redis shared 共用 yxt-redis；强隔离需求走 dedicated（P4）                                |
| 泛化 `supplyShared` 动了 redis 路径                | redis 路径逐字保留（搬入 `allocRedisDB` + `writeSharedEnv` 默认分支）+ 既有单测护栏（§10.1#6） |
| bind_existing app 与 shared app 共用同 milvus 撞名 | 概率极低（bind_existing 自管理、shared 强制前缀）；同 P2 redis db 0/1-15 共用同机等价          |
| `.28` yxt-milvus 未实跑致 app 连不上               | e2e step 1 先验 + step 6 TCP 探测；供给本身不依赖 milvus 可达（只注入 env）                    |
| pymilvus .28 numpy 阻断致无法 e2e 验 CRUD          | 接受（P4 已记）；e2e 验平台侧注入/隔离/可达/回收，CRUD 是 app 端可移植性                       |

---

## 12. 未覆盖（YAGNI / 后续）

- **milvus 鉴权 / native database + RBAC**：透明隔离（每 app 一个 milvus database + scoped user），免 app 加前缀；同时解决残留 collection 清理（删 app = drop database）。独立 spec（PRD §7 剩余项）
- **残留 collection janitor**：按前缀 list + drop 的清理器（需 milvus 客户端）；或鉴权项自带
- **项目级 shared 池**：按 project_space 分 milvus 实例（扩 `LookupShared` scope）
- **UI 勾选 / deps HTTP API**：创建应用时选 kind+strategy（独立 spec，本次仍 `.anp/deps.yaml` 驱动）
- **可配前缀模板**：`isolation={"mode":"prefix","template":"..."}` 让 `genMilvusPrefix` 读模板（v1 固定 `app<short>_`）
- **监控/备份/迁移**：shared milvus 的指标与备份
- **vault/KMS**：鉴权引入后的密钥管理

---

## 13. 验收标准

1. **种子**：迁移后 `appdeploy_service_instance` 含 `svinst-milvus-shared-28`（`supply_mode=shared`, `kind=milvus`, `isolation={"mode":"prefix"}`, `host=10.10.0.28`, `port=19530`）；无新索引（复用 `uq_svbind_inst_token`）
2. **分配隔离**：两个 shared milvus app 部署后容器内 `MILVUS_COLLECTION_PREFIX` 不同（`appAAA_` / `appBBB_`），`MILVUS_ADDR` 同
3. **env 注入**：`appdeploy_env` 每 app 有 `MILVUS_ADDR` + `MILVUS_COLLECTION_PREFIX` 两行 `source=platform`，**无** `MILVUS_PASSWORD` / `MILVUS_DB`
4. **回收**：删 app 后其 binding/env CASCADE 删（前缀从占用集移除）
5. **幂等**：同 app 重部署前缀不变、不重生、不 claim
6. **可达**：app 容器 → `10.10.0.28:19530` TCP OK
7. **零回归**：P1 bind_existing / P2 redis shared / P3 redis dedicated / P4 milvus dedicated 链路、`DATABASE_URL` 注入、部署主流程不受影响；redis shared 单测全绿；Delete 仍正常（shared 靠 CASCADE，dedicated Cleanup 按 kind rm 不受影响）
8. **平台保护**：手改 `MILVUS_COLLECTION_PREFIX` 返 409

---

_本设计把 mwsupply 的 shared 从 redis 推进到 milvus：`supplyShared` 重构为 kind 分派骨架（redis 路径逐字保留），milvus 用随机 collection 前缀隔离（`app<12hex>_`，复用 `uq_svbind_inst_token` 唯一索引兜底），注入 `MILVUS_ADDR` + `MILVUS_COLLECTION_PREFIX`，CASCADE 自动回收。零 main.go、零 handler、零 docker、零新依赖、仅 1 迁移（种子）。补齐后 milvus 三策略（bind_existing / shared / dedicated）全齐。隔离是逻辑前缀（需 app 配合），透明隔离 + 残留清理留给后续「milvus 鉴权」项。审核通过后开 plan → 实现。_

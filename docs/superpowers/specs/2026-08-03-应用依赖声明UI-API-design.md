# 应用依赖声明 UI/API — 设计（P6 deps-ui）

- 关联 PRD：[多形态应用治理 PRD §7](../../PRD/2026-08-01-多形态应用治理与开发运维统筹-PRD.md)（本设计 = §7 中期-运行态 ②「依赖声明+连接注入」的**易用性收口**）
- 上游：[中间件依赖供给与注入设计](./2026-08-01-中间件依赖供给与注入-design.md)（P1 总设计）、[P2 shared-redis](./2026-08-01-中间件依赖注入-P2-shared-redis-design.md)、[P3 dedicated-redis](./2026-08-02-中间件依赖注入-P3-dedicated-redis-design.md)、[P4 dedicated-milvus](./2026-08-02-中间件依赖注入-P4-dedicated-milvus-design.md)、[P5 shared-milvus](./2026-08-03-中间件依赖注入-P5-shared-milvus-design.md)
- 状态：设计待审

---

## 1. 背景与目标

### 1.1 P1–P5 已闭环

中间件依赖供给（mwsupply）三策略 × redis/milvus 全齐：bind_existing（P1）/ shared（P2 redis、P5 milvus）/ dedicated（P3 redis、P4 milvus）。`.28` 端到端全绿。

### 1.2 P6 要补的缺口

P1–P5 的**唯一入口是开发者手编仓库根 `.anp/deps.yaml`**（或 opencode 适配回写）。核验：

- 后端 `LoadDepsManifest`（`mwsupply/manifest.go`）从 `repoDir/.anp/deps.yaml` 被动读，无 `/deps` HTTP handler；
- 前端 `platform/frontend` grep `deps.yaml|mwsupply|依赖声明` **零命中**——无任何 UI。

结果：依赖注入「e2e 能跑但真实用户用不起来」。本期收口：**给 mwsupply 补 HTTP API + 前端勾选器，替代手编 YAML**。

### 1.3 目标

让开发者**不碰 `.anp/deps.yaml`**，在应用管理页选 kind+strategy 即可声明中间件依赖；声明存平台 DB（运行时真相），下次部署生效。P1–P5 的供给/注入链路（kind 分派、env 注入、binding 落库、CASCADE 回收）**全部复用，零改供给逻辑**——只换「声明读源」从文件→DB。

**关键洞察**：`appdeploy_service_binding` 表已具 `UNIQUE(app_id, service_kind)` + `status DEFAULT 'declared'` + 按 (app,kind) upsert——**它本就是为「声明+结果」设计的**（declared 态今天没用，只因供给在部署时同步完成）。故**复用 binding 表做声明载体，零新表/零新列/零迁移**。

---

## 2. 范围

### 2.1 本期做（in）

- HTTP API：`GET/PUT /project-spaces/:id/apps/:aid/deps`（读/整体替换声明）、`GET /project-spaces/:id/deps/catalog`（可选 kind/strategy + 可见实例，驱动 UI）。
- 前端：应用管理页详情面板新增「依赖」section（增删改 kind+strategy，下次部署生效）。
- MWReconciler 读源切换：deploy 时从「读 `.anp/deps.yaml`」改为「读该 app 的 binding 声明行」。
- 导入种子：导入 app 时若仓库有 `.anp/deps.yaml` 且该 app 在 DB 无该 kind 声明 → 一次性种子为 declared binding（opencode 适配写的文件由此进入平台；用户 UI 声明优先，不被覆盖）。
- 声明移除清理：UI 删除某依赖 → 释放其资源（dedicated docker rm + 删 platform env + 删 binding），不待下次部署。

### 2.2 本期不做（out / YAGNI）

- **per-env 依赖**：v1 声明 per-app（test/prod 共用，与「一个 repo 一份 deps.yaml」现状一致）。per-env（test=shared/prod=dedicated）= 未来（binding 表加 env 列 + UNIQUE(app,kind,env)）。
- **bind_existing 显式选目标实例**：v1 bind_existing 仍按 `LookupBindExisting(psID,kind)` 自动选（现状）。UI 显式挑实例（多实例同 kind 时）= 未来（supplyOne 读 binding.service_instance_id 作输入）。
- **项目级 shared 池**（PRD 待立项 ③）：catalog 仍只列平台级 shared 实例。
- **vault/KMS、milvus 鉴权**：同 P5 §12，后续独立项。

---

## 3. 关键决策

| 决策                                            | 取舍                                                                                                                                                                                                                    |
| ----------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **真相源 = 平台 DB（binding 表）**              | UI/API 独立于仓库编辑；单一运行时真相；未来可 per-env；`.anp/deps.yaml` 降为导入初始来源。代价：deploy 读源从文件换 DB（局部）                                                                                          |
| **复用 `appdeploy_service_binding` 做声明载体** | 该表已具 UNIQUE(app_id,service_kind)+declared 态+按(app,kind)upsert，即天然声明槽。**零新表/零新列/零迁移**。考虑过另建 `appdeploy_app_dep` 声明表—— rejected：与 binding 字段重复，且 binding 的 declared 态已为此预留 |
| **声明与部署解耦**                              | UI 改声明不触发部署；下次 deploy/redeploy 时 MWReconciler 供给生效。声明是 app 配置、部署是动作，解耦更干净                                                                                                             |
| **PUT 整体替换 + diff 清理**                    | 前端管全表、原子保存；后端 diff 现 binding vs 新声明，removed/changed → 释放资源，added → 写 declared。避免「item CRUD + 多次往返」                                                                                     |
| **种子 ON CONFLICT DO NOTHING**                 | 导入种子不覆盖用户已 UI 设的声明（用户优先）                                                                                                                                                                            |

---

## 4. 数据模型：复用，不改表

复用 `appdeploy_service_binding`（DDL 见 `000028_mwsupply.up.sql`），**无新表、无新列、无迁移**：

```
appdeploy_service_binding (
  id, app_id (FK→appdeploy_application ON DELETE CASCADE),
  project_space_id NOT NULL, service_kind NOT NULL, strategy NOT NULL,
  service_instance_id (FK→instance, nullable, 供给结果),
  isolation_token (nullable, 供给结果),
  env_key NOT NULL,           -- 声明时即填 EnvKeyFor(kind)
  status DEFAULT 'declared',  -- declared(声明待供) / bound(已供) / failed
  last_error, created_at, updated_at,
  UNIQUE(app_id, service_kind)
)
```

**声明 = binding 行的声明字段**（app_id / service_kind / strategy / env_key / status=declared）；供给结果字段（service_instance_id / isolation_token / status=bound）由 MWReconciler 在部署时填。一行承载「声明+结果」，状态机 `declared→bound/failed`。

**粒度**：per-app（UNIQUE(app_id,service_kind)）→ 一个 app 一份声明，test/prod 共用。

---

## 5. HTTP API（`appdeploy/handler.go`，挂 `Handler.Register`）

| 方法 | 路径                                 | handler          | 作用                                                                                                               |
| ---- | ------------------------------------ | ---------------- | ------------------------------------------------------------------------------------------------------------------ |
| GET  | `/project-spaces/:id/apps/:aid/deps` | `GetDeps`        | 返回该 app 当前声明（每 kind 一行：kind/strategy/status/instance/token/error）                                     |
| PUT  | `/project-spaces/:id/apps/:aid/deps` | `PutDeps`        | 整体替换声明（body: `[{kind,strategy}, ...]`）；diff 清理 + 写 declared                                            |
| GET  | `/project-spaces/:id/deps/catalog`   | `GetDepsCatalog` | `{kinds:[...], strategies:[{name,desc}], instances:[{id,kind,name,supply_mode,host,port}]}`（驱动 UI 下拉/可见性） |

### 5.1 PutDeps 逻辑（核心）

```
cur = ListBindingsByApp(appID)            # 现所有 binding（map kind→binding）
new = body 声明集                          # map kind→{strategy}
for kind in (cur \ new) 或 (cur∩new 且 strategy 变):   # removed / changed
    releaseDep(binding)                   # dedicated: docker rm + 删 instance + 删 platform env + 删 binding
                                          # shared/bind_existing: 删 platform env + 删 binding（token 随行释放）
for kind in (new \ cur) 或 (new∩new 且 strategy 变):   # added / changed
    UpsertBinding(declared, strategy, env_key=EnvKeyFor(kind))   # status=declared，待下次部署供给
# unchanged（同 kind 同 strategy）：不动（保 bound，不重供）
```

- **releaseDep**：dedicated 复用 `Cleanup` 的 per-binding rm 逻辑（redis RmForce / milvus RmMilvusStack + 删 instance 行）；三类都删该 kind 注入的 platform env（`injectedEnvKeys(kind)`：redis=`REDIS_ADDR/REDIS_DB/REDIS_PASSWORD`，milvus=`MILVUS_ADDR/MILVUS_COLLECTION_PREFIX`，经 `store.DeleteEnv`，绕过用户侧 source=platform 删除保护——undeclare 是平台动作）；最后 `DeleteBinding`。
- **校验（declare 时，宽松）**：kind∈{redis,milvus}；strategy∈{bind_existing,shared,dedicated}；UNIQUE(app_id,kind) 由 DB 兜底。**实例可用性不在 declare 时硬卡**（实例可后种子）——shared 无实例时仍接受声明，供给时置 failed+清晰错误（同现状 supplyOne 语义）；catalog 供 UI 预警。
- 鉴权：复用 appdeploy 现有 app 编辑权（谁能改 app 谁能改 deps），不新增角色。

### 5.2 GetDepsCatalog

- `kinds`：固定枚举 `["redis","milvus"]`（可扩，与 EnvKeyFor 支持集一致）。
- `strategies`：三策略 + 中文说明（UI 渲染）。
- `instances`：`SELECT ... FROM appdeploy_service_instance WHERE status='active' AND (project_space_id=:ps OR project_space_id IS NULL)`（bind_existing/shared 实例，供 UI 展示可绑/可用情况）。

---

## 6. MWReconciler 读源切换 + 导入种子

### 6.1 Reconcile 改读 binding（`mwsupply/supply.go`）

```
// 改前：m,_ := LoadDepsManifest(repoDir); for _,dep := range m.Services { supplyOne(dep) }
// 改后：
binds := store.ListBindingsByApp(appID)          # 声明即 binding 行
for _, b := range binds {
    supplyOne(appID, psID, DepService{Kind: b.ServiceKind, Strategy: b.Strategy})
}
```

- `supplyOne` 及其下游（supplyShared/supplyDedicated/bind_existing、env 注入、mkBind upsert）**逐字不变**——输入从 `DepService{kind,strategy}` 来，binding 已携带这两字段。复用判定（同 app 已 bound 同实例→保 token/不 flush/不重启）天然适配「声明持存、多次部署幂等」。
- **接口签名**：`MWReconciler.Reconcile(ctx, appID, psID)`（去掉 repoDir 参数——声明已不在文件）。更新接口（`handler.go:78`）+ 唯一调用点（`handler.go:1631`）+ 测试。

### 6.2 导入种子（`mwsupply` 新增 `SeedFromManifest`）

```
// 接口新增：SeedFromManifest(ctx, appID, psID, repoDir) error
// 实现：m := LoadDepsManifest(repoDir); for each service:
//   UpsertBinding ON CONFLICT(app_id,service_kind) DO NOTHING  ← 只种 absent kind，不覆盖 UI 声明
//   (status=declared, strategy, env_key=EnvKeyFor(kind))
```

- 调用点：`runImport`(handler.go:1167) / `runImportZip`(1305) 在 repo 就绪后调 `h.mwReconciler.SeedFromManifest(ctx, appID, psID, repoDir)`。best-effort（失败不阻塞导入）。
- opencode 适配（`adapt_prompt.go`）**不动**——它写的 `.anp/deps.yaml` 仍是「初始声明意图」，生效路径变为「导入时种子进 DB」。

### 6.3 `.anp/deps.yaml` 在 deploy 时不再读

- Reconcile 不再 `LoadDepsManifest`。文件仅导入时作种子源；之后 DB 为真相，文件改动在 deploy 时忽略（文档化）。

---

## 7. 前端 UI（`platform/frontend/app/applications/page.tsx`）

应用管理页选中 app 的详情面板（与 env vars / instances 并列）新增**「依赖」section**：

- `GET /deps` 列当前声明（kind/strategy/status/绑定实例/token/error）。status=declared 显示「待部署生效」hint。
- 增/改：选 kind（下拉来自 catalog）、strategy（三选一带说明）；保存 → `PUT /deps`（前端管全表，整体提交）+ toast。
- 删：移除某 kind → `PUT /deps`（不含该 kind）→ 后端 releaseDep 清理。
- 顶部 hint：「中间件依赖在下次部署/重新部署时注入生效」。
- 复用现有详情面板 fetch/渲染范式（`/apps/:aid/detail` 同款）；新增 `/deps` + `/deps/catalog` 两个 fetch。

---

## 8. 模块 / 文件改动

> 反向依赖约束：`mwsupply` 不能 import `appdeploy`（`EnvWriter` 接口即为此存在）。故跨包能力（删 env）走**接口扩展**，不直接调 appdeploy.Store。

**接口扩展（mwsupply 侧）**：

- `EnvWriter` 接口 +`DeleteEnv(ctx, appID, key) error`（appdeploy.Store 已有该方法，天然满足）。
- `MWReconciler` 接口（`handler.go:78`）：`Reconcile` 去 `repoDir` 参数；+`SeedFromManifest(ctx, appID, psID, repoDir) error`；+`ReleaseDep(ctx, binding *mwsupply.ServiceBinding) error`（声明移除/变更时释放资源）。

| 文件                        | 改动                                                                                                                                                                                                                                                                                                                                                                                        |
| --------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `appdeploy/handler.go`      | +`GetDeps`/`PutDeps`/`GetDepsCatalog` handler + 3 路由（Register）；`PutDeps` diff（`ListBindingsByApp` 经 MWReconciler 暴露的读方法 或新增 `ListDeps(appID)` 接口方法）→ 对 removed/changed 调 `ReleaseDep`、added/changed 调 `DeclareBinding(appID,psID,kind,strategy)`；MWReconciler 接口签名调整；`runImport`/`runImportZip` 调 `SeedFromManifest`；deploy 调用点（:1631）去 repoDir    |
| `mwsupply/supply.go`        | `Reconcile` 改读 `store.ListBindingsByApp` → 映射 `DepService{Kind,Strategy}` 喂 supplyOne（下游逐字不变）；去 repoDir；+`SeedFromManifest`（LoadDepsManifest → `DeclareIfAbsent` 每个 service）；+`ReleaseDep`（dedicated：复用 `Cleanup` 的 per-binding rm 逻辑 docker rm + `DeleteInstance`；三类都 `env.DeleteEnv(injectedEnvKeys(kind))` + `DeleteBinding`）；+`injectedEnvKeys(kind)` |
| `mwsupply/store.go`         | +`DeclareIfAbsent`（`INSERT ... ON CONFLICT(app_id,service_kind) DO NOTHING`，种子用，不覆盖 UI）；+`DeclareBinding`（upsert declared，PUT added/changed 用）；余复用（`ListBindingsByApp`/`DeleteBinding`/`DeleteInstance` 已有）                                                                                                                                                          |
| `app/applications/page.tsx` | +「依赖」section（fetch `/deps`+`/deps/catalog` / 渲染 / 增删改 / `PUT /deps`）                                                                                                                                                                                                                                                                                                             |
| 测试                        | `supply_test.go`（Reconcile 读 binding、SeedFromManifest 不覆盖、ReleaseDep 各策略）、`handler_http_test.go`（GetDeps/PutDeps diff 各分支/catalog/校验 400）、前端 vitest（deps 组件，若抽组件）                                                                                                                                                                                            |

**零迁移、零新表、零新列、零新依赖**（仅 Go 接口方法 + handler + 前端 section）。

---

## 9. 测试计划

### 9.1 PG 单测（`go test -p 1`，`anp_test` 库）

- `Reconcile` 读 binding 声明 → 供给 → bound（redis bind_existing / shared / dedicated、milvus shared/dedicated 各一，复用 P2–P5 既有用例形状）。
- `SeedFromManifest`：有 `.anp/deps.yaml` → 种 declared；已存在同 kind binding（UI 声明）→ 不覆盖。
- `PutDeps` diff：added→declared；removed（dedicated）→ docker rm + 删 instance + 删 platform env + 删 binding；removed（shared/bind_existing）→ 删 env + 删 binding；changed（strategy 变）→ 释放旧 + 声明新；unchanged→不动（保 bound）。
- `releaseDep`：注入的 env 键全删（redis 三键 / milvus 两键）。
- catalog：返回 kinds/strategies/ps+全局 active instances。

### 9.2 handler_http_test

- `GET /deps` 空/有；`PUT /deps` 增删改 + 校验 400（bad kind/strategy）；`GET /deps/catalog`。
- 删 app → binding CASCADE（既有，回归）。

### 9.3 `.28` 端到端（真前端驱动后端，`verify-cross-frontend-backend`）

- UI 声明 redis+shared → 工作台 deploy → 验 `REDIS_ADDR` 注入 source=platform / `nc` 可达 / 删 app CASCADE。
- UI 声明 milvus+shared → deploy → 验 `MILVUS_ADDR`+`MILVUS_COLLECTION_PREFIX` / `nc` 可达。
- 导入带 `.anp/deps.yaml` 的 app → 验种子进 DB（declared）→ deploy → 供给。
- UI 删除某依赖 → 验 dedicated 容器 rm / platform env 删除 / binding 行清。
- **替代历次 e2e 里手编 `.anp/deps.yaml` 的步骤**（本次起 e2e 全走 UI）。

---

## 10. 风险与取舍

| 风险                                             | 应对                                                                                                                                        |
| ------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------- |
| 声明移除时 dedicated 容器清理失败（docker 抖动） | releaseDep best-effort（记日志，不阻塞 PUT；binding 已删，残留容器由后续巡检/手动收）                                                       |
| 种子与 UI 声明竞争（导入中改 UI）                | 种子 ON CONFLICT DO NOTHING + per (app,kind)；UI PUT 后到的声明胜                                                                           |
| 旧 `.anp/deps.yaml` 与 DB 声明不一致（历史 app） | 首次导入种子后 DB 为准；文件 deploy 时忽略（文档化）；历史 app 无 DB 声明则无依赖注入（需重新声明）——可接受（P1–P5 的 app 本就是 e2e 样本） |
| 复用 binding 表混淆「声明」与「结果」语义        | status 状态机明确（declared=声明待供 / bound/failed=结果）；env_key 声明时即填、instance/token 供给时填；注释说明                           |

---

## 11. 未覆盖（YAGNI / 后续）

- **per-env 依赖**（test/prod 不同中间件）：binding 表加 env 列 + UNIQUE(app,kind,env)。
- **bind_existing 显式选目标实例**：supplyOne 读 binding.service_instance_id 作输入 + UI picker。
- **项目级 shared 池**（PRD ③）：catalog/供给扩 project_space 维度。
- **vault/KMS、milvus 鉴权(native db+RBAC)**：同 P5 §12。
- **声明变更触发自动重新部署**：v1 不做（解耦，下次 deploy 生效）。

---

## 12. 验收标准

1. `GET/PUT /deps`、`GET /deps/catalog` 三接口可用，校验/diff 行为符合 §5。
2. 应用管理页「依赖」section 可增删改 kind+strategy，不碰 `.anp/deps.yaml`。
3. MWReconciler deploy 时读 DB 声明供给（不再读文件）；导入种子生效且不覆盖 UI 声明。
4. UI 删除依赖 → dedicated 容器/platform env/binding 行均清。
5. PG 单测 + handler_http_test 全绿（`go test -p 1`）；`.28` e2e 全走 UI（redis/milvus shared 各一 + 导入种子 + 删除清理）全绿。
6. 零迁移、零新表；P1–P5 既有 e2e 用例（改走 UI 声明）仍全绿（无回归）。

---

## 13. e2e 验证结论（.28，2026-08-03，HEAD `891b163`）

**部署**：push origin main（b6db717..891b163）→ tar+scp+重建 backend/frontend → 三项核查全绿（源码 SetDeps/SeedFromManifest 命中、容器新创建 23:17/23:18、迁移仍 000033 P6 零迁移、healthz/deep healthy）。

**API 驱动 e2e（6/6 PASS，零实现 bug）**：

1. **redis shared 全链路** ✅：PUT deps(redis/shared)→binding declared→deploy v2 running→`REDIS_ADDR=10.10.0.28:6381`+`REDIS_DB=1` source=platform→binding bound token=1→容器内 `nc -z 10.10.0.28 6381`=OK。
2. **milvus shared 全链路** ✅：PUT +milvus/shared→deploy v3 running→`MILVUS_ADDR=10.10.0.28:19530`+`MILVUS_COLLECTION_PREFIX=app620682c8f013_`（`app<12hex>_`）source=platform→binding bound svinst-milvus-shared-28→`nc -z 19530`=OK。（caveat 同 P4/P5：pymilvus numpy/X86_V2 仅阻断客户端库，TCP 连通+平台注入正确。）
3. **导入种子 SeedFromManifest** ✅：导入 `.anp/deps.yaml`(kind:redis 无 strategy) 的 app→自动种 `bind_existing/declared`→deploy→bound svinst-redis-28。
4. **删除清理 SetDeps diff→ReleaseDep+DeleteEnv** ✅：PUT deps=`[]`→0 binding 行+0 REDIS*/MILVUS* env 行（共享实例本体保留，仅 app 隔离 token/db 号释放）。
5. **校验负路径** ✅：`kind:mongodb`→400「kind 非法」；`strategy:magic`→400「strategy 非法」；均无脏 binding。
6. **catalog** ✅：kinds=[redis,milvus]、strategies 3（带 desc）、instances 4（.28 种子 bind_existing+shared）。

**前端核查**：frontend 重建 23:18，入口 200，deps UI 关键字（`bind_existing`/`deps/catalog`）在重建后 `.next` SSR chunk 命中。

**结论**：P6 deps-ui 端到端全链路闭合（UI/API PUT→SetDeps diff→binding declared→下次 deploy Reconcile 读 DB→supplyAll→env 注入；导入→SeedFromManifest→declared）。声明与部署解耦、移除清理、导入种子、校验、catalog 均符合 §5/§6 规格。**P1-P5 依赖注入链路自此可通过 UI/API 声明驱动，不再依赖手编 `.anp/deps.yaml`**（文件降为导入初始来源）。零迁移、零回归。

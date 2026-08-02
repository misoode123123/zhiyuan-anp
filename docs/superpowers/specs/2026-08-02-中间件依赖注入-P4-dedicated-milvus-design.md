# 中间件依赖注入 P4 设计 —— dedicated 专属 Milvus（每 app 一个 milvus+etcd+minio 栈）

- **类型**：方案 / 详细设计（P1 设计 §10 分期里的 P3 阶段的 milvus 补齐；P3 已完成 redis dedicated）
- **状态**：待审核（按「方案先成文审核」，审核通过后再开 plan → 实现）
- **日期**：2026-08-02
- **作者**：miscode + Claude
- **关联模块**：`mwsupply`（dedicated 流程按 kind 分派 + milvus launch/ready/cleanup）、`appdeploy`（Delete handler 的 Cleanup 已接入，零改）
- **关联文档**：
  - [中间件依赖供给与注入 设计（P1 总设计）](2026-08-01-中间件依赖供给与注入-design.md) —— 本文件是其 §10「P3：dedicated」的 milvus 细化
  - [P3 dedicated redis 设计](2026-08-02-中间件依赖注入-P3-dedicated-redis-design.md) —— 同一 mwsupply 包的前一阶段（每 app 一个 redis 容器），本文件延续其范式并按 milvus 特性扩展
  - [多形态应用治理 PRD §4.4/§7/§8/§9](../../PRD/2026-08-01-多形态应用治理与开发运维统筹-PRD.md)
  - [应用库与 API 统一管理设计](2026-07-21-应用库与API统一管理-设计.md)（pgsupply 范式来源）

---

## 1. 背景与目标

### 1.1 P1/P2/P3 已闭环

- **P1（bind_existing）**：`.anp/deps.yaml` 声明依赖 → 部署时 `mwsupply.Reconcile` 读清单 → 查 `appdeploy_service_instance` 注册表 → 经 `EnvWriter.UpsertEnv(source=platform)` 写 `REDIS_ADDR`/`MILVUS_ADDR` 等 → 现有 `docker run -e` 注入。`.28` live 验证通过。redis 与 milvus 的 bind_existing 均已可用（种子 `svinst-redis-28`/`svinst-milvus-28`）。
- **P2（shared）**：扩 shared 分支，共享 redis 实例 + db 号隔离。`.28` live 验证通过。
- **P3（dedicated redis）**：每 app 一个专属 redis 容器（端口池 9600-9699 + `requirepass`）+ `MWReconciler.Cleanup`（删 app `docker rm`）。`.28` live 验证通过（commit 984f685）。关键结论：dedicated 容器起在默认 bridge，`.28` 上 backend(`deploy_default`) 实测**能**拨到其 host 发布端口，best-effort 未被触发。

### 1.2 P4 要补的缺口

P3 的 dedicated 流程是 **redis 专写**：`supplyDedicated`（`supply.go:182`）直接调 `r.docker.RunRedisContainer`、`r.ready.Ping`（AUTH+PING）、`writeDedicatedEnv`（REDIS_ADDR + REDIS_PASSWORD）；`MWDockerRunner` 接口（`docker.go:13`）只有 `RunRedisContainer`；`naming.go` 的常量与 `dedicatedContainerName` 都是 redis 专属。**milvus 的 dedicated 分支不存在**——应用在 `.anp/deps.yaml` 声明 `kind: milvus, strategy: dedicated` 时，会落到 redis 专写的 dedicated 流程里（行为未定义/失败）。

本设计把 dedicated 流程**按 kind 分派**，补齐 **milvus dedicated**。

### 1.3 目标

应用在 `.anp/deps.yaml` 声明 `kind: milvus, strategy: dedicated` → 平台为该 app **docker 起一个专属 milvus standalone 栈**（milvus + etcd + minio 三容器 + 专属用户网络，1:1 复刻 .28 已验证的 `yxt-milvus` 配方）→ 仅 publish milvus gRPC 到端口池端口 → 注入 `MILVUS_ADDR=AppDeployHost:port` → 该 app 独占整栈；删 app 时 `docker rm` 三容器 + 删网络；重部署复用栈、保数据。

**与 redis dedicated 的本质区别**：redis dedicated 是「1 个轻容器（~10MB，秒起）」；milvus dedicated 是「3 容器 + 1 网络（镜像合计 ~1.4GB，启动 30-90s，内存 ~2-3GB）」。由此带来三个设计差异：① 多容器编排（专属网络 + 服务名解析）；② 就绪检测不能用 redis 的 5s best-effort（milvus 启动慢，需长超时探针）；③ Cleanup 要 rm 三容器 + 网络。

---

## 2. 范围

### 2.1 本期做（in）

- milvus dedicated 全链路：专属网络 + 起 milvus/etcd/minio 三容器 + 就绪检测 + 实例行登记 + `MILVUS_ADDR` 注入 + 删 app 回收（三容器 + 网络）
- 把 `supplyDedicated` 从 redis 专写**重构为按 kind 分派**（launch/ready/env/rm 四处分派），**redis 路径行为逐字保留**（既有 redis dedicated 单测零回归护栏）

### 2.2 本期不做（out / YAGNI）

- **milvus shared**：collection/前缀隔离的共享 milvus 实例，独立设计，留后续
- **milvus 鉴权**：v1 无 auth（同 `yxt-milvus` 现状，靠专属网络 + 端口隔离为边界；未来加 root 密码 + RBAC）
- **bind-mount 数据卷**：v1 数据落容器可写层，靠 `--restart unless-stopped` + 容器复用保数据（同 redis dedicated）。milvus 数据较大，未来按 pgsupply 范式加 host 卷
- **UI 勾选 / deps HTTP API**：本期 dedicated 仍由 `.anp/deps.yaml`（opencode 适配回写或手编）驱动；UI 勾选器与 deps CRUD 端点独立 spec
- **配额模块接入**：配额即端口池（池满 failed），不动 `internal/quota`（同 P2/P3 思路）
- **vault/KMS 密钥管理**：v1 无 auth 无 secret；未来鉴权时接 vault（同 pgsupply I1 债）
- **监控/备份/迁移**：dedicated milvus 的指标采集/备份不在本期
- **资源硬限（`--memory`/`--cpus`）**：v1 不设；端口池即软配额。未来按需加

---

## 3. 关键决策

| 维度              | 选择                                                                                                | 理由                                                                                                                                                                                                   |
| ----------------- | --------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 拓扑              | **3 容器 canonical（milvus+etcd+minio）+ 每 app 专属用户网络**                                      | 1:1 复刻 .28 已验证的 `yxt-milvus`（镜像/配置/版本全用已跑通的 v2.6.15）；milvus 必须依赖对象存储（minio）+ 元数据存储（etcd），**无法真单容器**；2 容器 embedded-etcd 偏离已验证配方，留后续          |
| 镜像              | `milvusdb/milvus:v2.6.15` / `quay.io/coreos/etcd:v3.5.16` / `minio:v20.2.5-2024.7.4`                | 与 `yxt-milvus` 同 tag，**.28 全部已缓存**（实测，消除 P2/P3「慢拉镜像」最大风险）                                                                                                                     |
| 端口              | 仅 publish milvus gRPC `<poolport>:19530`；etcd/minio **不 publish**，走专属网络内部解析            | app 容器（默认 bridge）经 `AppDeployHost:poolport` 达 milvus（同 redis dedicated，P3 §15 已验证 app 可达 host 发布端口）；sidecar 无需对外，减少暴露面与端口占用                                       |
| 端口池            | **9700-9799**（redis 占 9600-9699，避开）                                                           | 100 槽；`allocPort` 复用纯函数（最小未占用号）                                                                                                                                                         |
| 容器名 / 服务解析 | base=`mwmilvus-<short>`；etcd/minio/milvus 容器在专属网络上分别 `--network-alias etcd/minio/milvus` | 容器名须全局唯一（靠 base），网络别名在每 app 隔离网络内不冲突；别名使 milvus env 可照搬 yxt 原文 `ETCD_ENDPOINTS=etcd:2379` `MINIO_ADDRESS=minio:9000`，就绪探针用稳定名 `http://milvus:9091/healthz` |
| 就绪检测          | **alpine 探针容器**在专属网络轮询 `http://milvus:9091/healthz`，长超时 ~120s                        | milvus 启动慢（30-90s，非 redis 秒级）；探针经 docker socket 起临时容器，**不受 .28 cross-network 坑影响**（不依赖 backend↔milvus 网络可达）；alpine:3.19 已缓存                                       |
| 就绪策略          | **R3：长超时探针 + 超时/infra 错降级 best-effort**                                                  | 给 milvus 足够启动时间（正确性），同时不让 .28 怪异性（探针抖动/路径不符）误杀供给（可用性，同 redis best-effort 哲学）                                                                                |
| 鉴权              | **v1 无 auth**（同 `yxt-milvus`）                                                                   | milvus RBAC 复杂；dedicated 容器由平台控制，专属网络 + 端口隔离已为边界；暴露面等同现状 yxt-milvus:19530                                                                                               |
| 数据持久          | **不 bind-mount**，数据落容器层，`--restart unless-stopped` + 容器复用                              | 同 redis dedicated（简化生命周期，Cleanup 仅 docker rm，无 host 目录清理）；重部署复用容器 → 数据留存；接受「容器被 rm 即数据丢」（dedicated 删 app 本就应清空）                                       |
| 泛化              | **`supplyDedicated` 骨架按 kind 分派**（launch/ready/env/rm），redis 路径逐字保留                   | 单一 dedicated 入口，避免并行函数重复 + 漂移；现有 redis dedicated 单测零回归保护                                                                                                                      |
| 回收              | **Delete handler 已接 `mwReconciler.Cleanup`（P3 接入），Cleanup 内按 kind 分派 rm**                | milvus 多了网络 + 2 sidecar 的 rm；best-effort 不阻塞 Delete；instance 行显式删（无 app FK，CASCADE 删不到）                                                                                           |

---

## 4. 数据模型

### 4.1 现状（已足够，无需新迁移）

`appdeploy_service_instance` 的 `container_name` 列（P3 迁移 `000030` 加，nullable）已存在，`ServiceInstance.ContainerName` 字段（`model.go:37`）与 `instCols`（含 `COALESCE(container_name,'')`）已就位。**本设计不加迁移**。

milvus dedicated 把 base 名 `mwmilvus-<short>` 存进 `container_name`；Cleanup 时由 naming 纯函数从 base **确定性派生** 3 容器名 + 网络名回收（无需额外列）。

### 4.2 实例行（供给时落库）

```go
inst := &ServiceInstance{
    ID:            "svinst-milvus-ded-" + short,
    ProjectSpaceID: nil,                      // dedicated 实例不挂项目，靠 binding 关联 app（同 redis dedicated）
    Kind:          "milvus",
    Name:          base,                       // mwmilvus-<short>
    SupplyMode:    ModeDedicated,
    Host:          r.host,                     // AppDeployHost
    Port:          port,                       // 9700-9799
    AuthRef:       "",                         // v1 无 auth
    ContainerName: base,                       // 派生 sidecar/net 名的基底
    Status:        "active",
}
```

> `UNIQUE(app_id, service_kind)`（binding）兜底同 app 同 kind 唯一；instance id 是 PK 已唯一。同 app 并发部署起两栈的孤立容器风险同 P3（记风险，不做 partial unique）。

---

## 5. 供给流程：`supplyDedicated` 重构为 kind 分派

### 5.1 骨架（共享，kind 无关）

把现 `supplyDedicated`（`supply.go:182`，redis 专写）重构为：复用判定 / 端口分配 / 实例登记 / binding 落库 **共享**，launch / ready / env / cleanup **按 `dep.Kind` 分派**。

```
supplyDedicated(dep):
  // —— 复用判定（幂等：同 app 重部署不重启、不换端口、保数据）——  【共享，不变】
  if b=GetBinding(appID,kind); b!=nil && b.status==bound && b.service_instance_id!="":
      if inst=GetInstance(b.service_instance_id); inst!=nil && inst.status==active:
          writeDedicatedEnv(kind, inst); mkBind(bound, inst.ID,"",""); return

  // —— 新供给 ——
  port = allocPort(docker.UsedPorts(ctx), portMin(kind), portMax(kind))   // redis 9600-9699 / milvus 9700-9799
  if port==0: mkBind(failed,"","","<kind> 端口池 %d-%d 已满"); return
  short = genShortID()
  base  = dedicatedContainerName(kind, short)   // redis→mwredis-<short> / milvus→mwmilvus-<short>

  // —— launch（按 kind 分派）——
  if err = launchDedicated(kind, base, port); err != nil:        // redis:RunRedisContainer / milvus:RunMilvusStack
      mkBind(failed,"","","起 <kind> 容器: "+err); return

  // —— ready（按 kind 分派，best-effort）——
  if err = waitReady(kind, base, port); err != nil:              // redis:ready.Ping(AUTH+PING,5s) / milvus:docker.MilvusReady(/healthz,120s)
      log.Warn("<kind> 就绪检测失败 (best-effort, proceed to bound)", ...)   // 不阻塞（同 P3 §14 范式）

  // —— 登记实例行（共享）——
  inst = newInstance(kind, short, base, port, r.host)            // auth_ref：redis=genPassword / milvus=""
  if err = store.CreateInstance(ctx, inst); err != nil:
      rmDedicated(kind, base)                                    // 登记失败回收（redis:RmForce / milvus:RmMilvusStack）
      mkBind(failed,"","","登记实例: "+err); return

  writeDedicatedEnv(kind, inst)                                  // redis:REDIS_ADDR+PASSWORD / milvus:MILVUS_ADDR
  mkBind(bound, inst.ID, "", "")
```

> `launchDedicated`/`waitReady`/`writeDedicatedEnv`/`rmDedicated` 即按 kind 的分派函数（内部 `switch dep.Kind`）；也可直接在 `supplyDedicated` 内 `switch`，实现期择一，行为等价。

### 5.2 状态机（binding.status）

同 P3 redis dedicated：

```
（无 binding）──新供给──▶ bound          // 起栈+就绪+登记+env 全成后落 bound
bound ──重部署──▶ bound（复用栈，不重启/不换端口/保数据）   // 幂等
（任意）──端口池满/起栈失败/登记失败──▶ failed（不建实例行、不写 env）
failed ──重部署──▶ 重新走「新供给」        // 失败 binding 无 service_instance_id，重试
```

无 `declared`/`allocating` 中间态（同 P2/P3：成功直接 bound，失败直接 failed）。就绪失败**不**落 failed（best-effort，同 P3 §14）。

### 5.3 `writeDedicatedEnv` milvus 分支

```go
func (r *Reconciler) writeDedicatedEnv(ctx, appID string, inst *ServiceInstance) {
    _ = r.env.UpsertEnv(ctx, appID, EnvKeyFor(inst.Kind), ConnStr(inst), false, "platform") // REDIS_ADDR / MILVUS_ADDR = host:port
    if inst.Kind == "redis" {               // redis 专：写 REDIS_PASSWORD（secret）
        _ = r.env.UpsertEnv(ctx, appID, strings.ToUpper(inst.Kind)+"_PASSWORD", inst.AuthRef, true, "platform")
    }
    // milvus：v1 无 auth，不写 password；不写 db token（dedicated 整实例）
}
```

> `ConnStr`（`connstr.go:20`）返回 `host:port`，redis/milvus 通用，**不改**。`EnvKeyFor` milvus→`MILVUS_ADDR` 已就位（`connstr.go:6`），**不改**。

---

## 6. 容器编排：`RunMilvusStack`（`docker.go` 新增）

### 6.1 启动顺序与命令（1:1 复刻 yxt-milvus，仅容器名/网络/端口参数化）

```
base=<base>; net=<base>-net; port=<poolport>

1) docker network create <net>

2) docker run -d --name <base>-etcd  --network <net> --network-alias etcd  --restart unless-stopped \
     quay.io/coreos/etcd:v3.5.16 \
     etcd -advertise-client-urls=http://etcd:2379 -listen-client-urls http://0.0.0.0:2379 --data-dir /etcd

3) docker run -d --name <base>-minio --network <net> --network-alias minio --restart unless-stopped \
     -e MINIO_ACCESS_KEY=minioadmin -e MINIO_SECRET_KEY=minioadmin \
     minio:v20.2.5-2024.7.4 \
     minio server /minio_data

4) docker run -d --name <base>        --network <net> --network-alias milvus --restart unless-stopped \
     -e ETCD_ENDPOINTS=etcd:2379 -e MINIO_ADDRESS=minio:9000 \
     -p <port>:19530 \
     milvusdb/milvus:v2.6.15 \
     milvus run standalone
```

- **顺序**：net → etcd → minio → milvus。milvus 启动时若 etcd/minio 未就绪会自动重试连接（milvus standalone 行为，yxt-milvus 同此）。
- **别名**：etcd/minio/milvus 三别名使 milvus env 照搬 yxt 原文，就绪探针用稳定名。
- **端口**：仅 milvus gRPC publish 到宿主 `<port>`（9700-9799，绑 `0.0.0.0:<port>`）；etcd(2379)/minio(9000) 不 publish。
- **重启**：三容器均 `--restart unless-stopped`（宿主重启自恢复，同 pgsupply/redis dedicated）。
- **数据**：v1 不 `-v`，落容器层（同 redis dedicated）。

> 任何一步失败：`RunMilvusStack` 返错前 best-effort `docker rm -f` 已起的容器 + `docker network rm <net>`（清半成品），由 `supplyDedicated` 的 launch 失败分支兜底（再调 `RmMilvusStack`）。

### 6.2 `MWDockerRunner` 接口扩展

`docker.go`：

```go
type MWDockerRunner interface {
    UsedPorts(ctx context.Context) map[int]struct{}
    RunRedisContainer(ctx context.Context, name, password string, port int) error   // 不变
    RunMilvusStack(ctx context.Context, base string, port int) error                // 新：net + etcd + minio + milvus
    MilvusReady(ctx context.Context, base string, timeout time.Duration) error      // 新：alpine 探针轮询 /healthz
    RmForce(ctx context.Context, name string) error                                 // 不变（redis）
    RmMilvusStack(ctx context.Context, base string) error                           // 新：rm 三容器 + net
}
```

osDocker 实现调 docker CLI（同 `runDockerCmd` helper）。纯函数 `etcdRunArgs`/`minioRunArgs`/`milvusRunArgs`/`milvusProbeArgs` 抽出便于单测（同 `redisRunArgs` 范式）。

---

## 7. 就绪检测：`MilvusReady`（与 redis 的核心差异）

### 7.1 为什么不能照 redis 的 5s best-effort

redis 秒级就绪，5s 超时即放行无伤大雅。milvus standalone 启动 30-90s（etcd/minio/milvus 三进程初始化 + 加载），若同样 5s 后放行 claim→bound，app 部署后立即连一个未就绪的 milvus → 首次连接失败。故 milvus 需要**长超时**。

### 7.2 alpine 探针（绕过 cross-network 坑）

backend（`deploy_default` 网）拨 milvus host 发布端口可能不通（P2/§7 cross-network 教训；P3 redis 实测能通但 milvus 在**专属网络**上拓扑不同，不可假设）。故就绪检测**不走 backend 网络拨号**，而是经 docker socket 起一个临时 alpine 容器加入 milvus 专属网络，从网络内部探测：

```
MilvusReady(ctx, base, timeout):
  net = base + "-net"
  deadline = now + timeout                       // ~120s
  loop until deadline:
    out, err = runDockerCmd(ctx, "run","--rm","--network",net,
        "alpine:3.19","wget","-qO-","-T","3","http://milvus:9091/healthz")
    if err==nil: return nil                       // 200 → 就绪
    sleep ~5s
  return errLast                                  // 超时未就绪
```

- alpine:3.19 BusyBox `wget` 支持 HTTP，exit 0 = 200。`-T 3` 给单次探针 3s 超时（避免挂死）。
- 探针容器 `--rm` 即起即删，不留痕迹；~120s 内最多 ~24 个一次性容器，可接受。
- 走 docker socket → **不受 backend↔milvus 网络可达性影响**。

### 7.3 best-effort 降级（R3）

`supplyDedicated` 调 `MilvusReady`，**失败仅记 Warn 后继续 claim→bound**（同 P3 §14 redis 范式）：

- 依据：milvus 多在 app 容器真正发起连接前完成启动（部署 → app 进程初始化 → 连 milvus 有数秒~十余秒窗口）；探针超时/路径不符不应阻塞供给。
- 失去的是「milvus 启动失败」边缘检测（罕见，`RunMilvusStack` 的 `docker run` 失败已兜底大部分），换来 .28 可用性。
- 单测：`TestReconcile_dedicatedMilvus_readyTimeout_bestEffort`——`MilvusReady` 返超时错 → binding 仍 `bound`、不 `RmMilvusStack`、`MILVUS_ADDR` 已写。

> e2e 实测校准：healthz 路径（`/healthz`）、9091 端口、120s 是否足够。若 milvus 版本路径不同（如 `/healthz` 不存在），e2e 修正探针参数。

---

## 8. 回收：`Cleanup` 按 kind 分派 rm

P3 已把 `mwReconciler.Cleanup` 接入 Delete handler（`appdeploy/handler.go`，在 `store.Delete` 之前）。本设计把 `Cleanup`（`supply.go:251`）的 rm 步骤按 kind 分派：

```
Cleanup(ctx, appID):                                    // 总返回 nil（best-effort，同 P3）
  binds = store.ListBindingsByApp(ctx, appID)
  for b in binds:
    if b.strategy != ModeDedicated || b.service_instance_id=="" : continue
    inst = store.GetInstance(b.service_instance_id); if inst==nil: continue
    if inst.Kind == "redis":
        if inst.container_name != "": docker.RmForce(inst.container_name)         // 不变
    if inst.Kind == "milvus":
        docker.RmMilvusStack(inst.container_name)     // rm <base> <base>-etcd <base>-minio + docker network rm <base>-net
    store.DeleteBinding(b.ID)                         // 先删 binding 解 FK，再删 instance（同 P3）
    store.DeleteInstance(inst.ID)
```

`RmMilvusStack(base)`：

```
docker rm -f <base> <base>-etcd <base>-minio     // best-effort（个别已不存在不报错）
docker network rm <base>-net                     // best-effort
```

> 只动 dedicated（bind_existing/shared 靠 CASCADE，同 P3）。删 instance 行（无 app FK，CASCADE 删不到，同 P3）。

---

## 9. naming.go 改动

```go
// dedicated 供给常量（redis 不变；新增 milvus）
const (
    mwPortMin = 9600; mwPortMax = 9699           // redis dedicated（不变）
    milvusPortMin = 9700; milvusPortMax = 9799   // milvus dedicated（避开 redis）
    redisImage = "redis:7-alpine"; redisInternalPort = 6379    // 不变
    milvusImage = "milvusdb/milvus:v2.6.15"
    etcdImage   = "quay.io/coreos/etcd:v3.5.16"
    minioImage  = "minio:v20.2.5-2024.7.4"
    milvusGrpcPort   = 19530
    milvusHealthPort = 9091
    milvusReadyTimeout = 120 * time.Second       // milvus 慢启动探针上限
    readyPingTimeout   = 5 * time.Second         // redis best-effort（不变）
    readyAlpineImage   = "alpine:3.19"           // 就绪探针（.28 已缓存）
)

// portRange 按 kind 给端口池上下界。
func portRange(kind string) (int, int) {
    if kind == "milvus" { return milvusPortMin, milvusPortMax }
    return mwPortMin, mwPortMax
}

// dedicatedContainerName 按 kind 前缀：redis→mwredis- / milvus→mwmilvus-。
// 现有 redis 调用点（supply.go:200）改为传 kind="redis"，输出仍是 mwredis-<short>（零回归）。
func dedicatedContainerName(kind, short string) string {
    if kind == "milvus" { return "mwmilvus-" + short }
    return "mwredis-" + short
}

// milvus 栈命名（从 base 确定性派生）
func milvusEtcdName(base string) string  { return base + "-etcd" }
func milvusMinioName(base string) string { return base + "-minio" }
func milvusNetName(base string) string   { return base + "-net" }
```

> `genShortID`/`genPassword`/`allocPort` 不变（kind 无关）。redis 的 `dedicatedContainerName(short)` 现有调用点（`supply.go:200`）改为传 kind，或保留旧签名 + 新增 milvus 分支——实现期择一，**redis 输出 `mwredis-<short>` 不变**。

---

## 10. 模块 / 文件改动

| 动作     | 文件                                                                                    | 说明                                                                                                                                                   |
| -------- | --------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 改       | `internal/mwsupply/supply.go`                                                           | `supplyDedicated` 抽 kind 分派骨架（launch/ready/env/rm）；`writeDedicatedEnv` 加 milvus 分支；`Cleanup` rm 按 kind 分派                               |
| 改       | `internal/mwsupply/docker.go`                                                           | `MWDockerRunner` +`RunMilvusStack`/`MilvusReady`/`RmMilvusStack`；osDocker 实现；纯函数 `etcdRunArgs`/`minioRunArgs`/`milvusRunArgs`/`milvusProbeArgs` |
| 改       | `internal/mwsupply/naming.go`                                                           | `dedicatedContainerName(kind,short)`；`portRange(kind)`；milvus 镜像/端口/超时常量；`milvusEtcdName`/`milvusMinioName`/`milvusNetName`                 |
| **不改** | `internal/mwsupply/connstr.go`                                                          | `EnvKeyFor`（milvus→MILVUS_ADDR 已在）、`ConnStr`（host:port 通用）均不改；`writeDedicatedEnv` 留在 supply.go                                          |
| **不改** | `internal/mwsupply/model.go`                                                            | `ContainerName` 字段已在；`ModeDedicated` 已在                                                                                                         |
| **不改** | `internal/mwsupply/store.go`                                                            | `CreateInstance`/`GetInstance`/`DeleteInstance`/`DeleteBinding`/`ListBindingsByApp` 已在                                                               |
| **不改** | 迁移、`cmd/server/main.go`、`appdeploy/handler.go`                                      | `container_name` 列已在（000030）；`NewReconciler`/`SetMwReconciler`/Delete 接入均在（P3）                                                             |
| 改测试   | `internal/mwsupply/docker_test.go`                                                      | `milvusRunArgs`/`etcdRunArgs`/`minioRunArgs`/`milvusProbeArgs` 纯函数；`RunMilvusStack`/`RmMilvusStack`/`MilvusReady` fake                             |
| 改测试   | `internal/mwsupply/supply_test.go`                                                      | milvus 新供给/复用幂等/端口满/起栈失败/就绪超时 best-effort/Cleanup rm 三容器+net；**redis dedicated 既有用例零改动须全绿**                            |
| **不改** | shared/bind_existing 分支、redis dedicated 行为、`Deployer`/`EnvPairs` 主流程、pgsupply |                                                                                                                                                        |

> 核心改 3 文件（supply/docker/naming），零迁移、零 main.go、零 handler 改动（P3 已铺好 Cleanup 接入）。

---

## 11. 测试计划

### 11.1 PG 单测（`go test -p 1`，跑 `anp_test` 库）

> 遵循记忆 `sqlite-test-pg-type-trap` / `go-test-serial-p1`：真 PG（不 sqlite），全量回归 `-p 1` 串行；`GOPATH=C:/Users/yxt/go` 前缀。

**docker_test.go**（纯函数 + fake）：

1. `etcdRunArgs`/`minioRunArgs`/`milvusRunArgs`/`milvusProbeArgs` 产出正确参数（镜像、别名、env、`-p port:19530`、`--network`）
2. `RmMilvusStack` 派生命令含三容器名 + network rm

**supply_test.go**（fake `MWDockerRunner` 记调用）：

3. milvus dedicated 新供给：`.anp/deps.yaml` `{kind:milvus, strategy:dedicated}` → binding=bound + `MILVUS_ADDR=host:port`（无 password、无 db）写入（source=platform）+ `RunMilvusStack` 被调一次（记 base/port）+ instance 行落库（kind=milvus, supply_mode=dedicated, container_name=mwmilvus-<short>, port∈9700-9799）
4. 复用幂等：同 app 二次 `Reconcile` → `RunMilvusStack` **不**再被调、port 不变、env 重写、binding 仍 bound
5. 端口池满：fake `UsedPorts` 占满 9700-9799 → binding=failed + `last_error` 含「端口池」+ 不写 env + 不起栈
6. 起栈失败：fake `RunMilvusStack` 返错 → binding=failed（且 `RmMilvusStack` 未被调，因 launch 前无半成品；或按实现清半成品）
7. 就绪超时 best-effort：fake `MilvusReady` 返超时错 → binding 仍 `bound`、不 `RmMilvusStack`、`MILVUS_ADDR` 已写
8. `Cleanup` milvus：app 有 milvus dedicated binding → `RmMilvusStack` 被调（按 base）+ instance 行删除；同 app 的 redis dedicated binding 走 `RmForce`、shared/bind_existing binding 不被 rm
9. **redis dedicated 零回归**：既有 redis dedicated 用例（新供给/复用/端口满/起容器失败/就绪 best-effort/Cleanup）全绿、行为不变

### 11.2 `.28` 端到端（`deploy-28-no-local-test`）

> 本机不跑功能测试，`.28` 是测试库。commit → push origin main → scp + `.28` 重建。

1. **先验镜像 + 端口段**：.28 上 `milvusdb/milvus:v2.6.15`/`etcd:v3.5.16`/`minio:v20.2.5-2024.7.4`/`alpine:3.19` 是否在（实测前三个已缓存，alpine 待确认）；9700-9799 空闲
2. **先验就绪探针路径**：起临时 milvus 栈（手动或脚本）→ alpine 探针 `http://milvus:9091/healthz` 是否返 200；校准 `MilvusReady` 参数（路径/超时）
3. 造最小 python 应用（`.anp/deps.yaml` 预写 `services:[{kind:milvus, strategy:dedicated}]`；pymilvus；python:3-alpine 基础）→ CREATE（带 repo_dir，不触发 adapt）→ deploy test
4. 容器内验证：`MILVUS_ADDR=10.10.0.28:<port>` 在；`docker ps` 见 `mwmilvus-<short>` + `-etcd` + `-minio` 三容器（同网络、仅 milvus `0.0.0.0:<port>->19530`）；`docker network ls` 见 `<base>-net`
5. **app 真能用**：pymilvus connect(MILVUS_ADDR) → create collection → insert → search round-trip 成功
6. **隔离**：两个 milvus dedicated app → 两套三容器栈、两个端口、两个网络、各自独立 collection（互不可见）
7. **回收**：删 app → `docker ps` 不再见其三容器、`docker network ls` 不再见 `<base>-net` + instance/binding/env 行清
8. **重部署复用**：同 app 重 deploy → 同一 `<base>` 三容器（不新建）+ 先前写入的 collection/数据仍在
9. **平台保护**：手改 `MILVUS_ADDR` 返 409（source=platform）

---

## 12. 风险与取舍

| 风险                                              | 对策                                                                                                                               |
| ------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| 每 app 3 容器 ~2-3GB 内存（共享 .28）             | 端口池即软配额（100 槽）；shared/bind_existing 是轻量默认；dedicated 是 opt-in 强隔离；未来加 `--memory` 硬限                      |
| 就绪探针路径/超时不符（healthz 路径、9091、120s） | e2e step 2 先验校准；best-effort 降级兜底（不阻塞供给）                                                                            |
| `.28` 缺 alpine:3.19（探针镜像）                  | e2e step 1 先验；缺则 `docker pull` 或换已缓存基础镜像（python:3-alpine）                                                          |
| 数据落容器层、磁盘占用                            | v1 接受（dedicated 少、删 app 即清）；未来 bind-mount host 卷                                                                      |
| 同 app 并发部署起两栈（race）                     | binding `UNIQUE(app_id,service_kind)` 兜底绑定行；孤立栈轻微泄漏（记风险，同 P3）                                                  |
| 泛化 `supplyDedicated` 动了 redis 路径            | redis 路径逐字保留 + 既有单测护栏（§11.1#9）                                                                                       |
| milvus 启动慢致 deploy 流程阻塞 ~120s             | `MilvusReady` 超时即降级 best-effort 放行；deploy 主流程已有 `context.WithTimeout`（15min build / 3min deploy，headless 教训）兜底 |
| 三容器名/网络名派生不一致致 Cleanup 漏 rm         | naming 纯函数确定性派生 + 单测（§11.1#2/#8）                                                                                       |

---

## 13. 未覆盖（YAGNI / 后续）

- **milvus shared**：共享 milvus + collection/前缀隔离（更轻、多 app 共用），独立 spec
- **milvus 鉴权**：root 密码 + RBAC（v1 无 auth）
- **bind-mount 数据卷**：按 pgsupply 范式挂 host 卷（数据更大、可备份）
- **资源硬限**：`--memory`/`--cpus`（共享机强化）
- **UI 勾选 / deps HTTP API**：创建应用时选 kind+strategy（独立 spec，本次仍 `.anp/deps.yaml` 驱动）
- **监控/备份/迁移**：dedicated milvus 的指标与备份
- **vault/KMS**：鉴权引入后的密钥管理
- **2 容器 embedded-etcd**：`ETCD_USE_EMBED=true` 省 etcd 容器（偏离已验证配方，留后续优化）

---

## 14. 验收标准

1. **无新迁移**：`container_name` 列复用（000030），不加迁移
2. **供给**：milvus dedicated app 部署后容器内 `MILVUS_ADDR=AppDeployHost:port`（无 password/db）；`docker ps` 见 `mwmilvus-<short>`+`-etcd`+`-minio` 三容器（仅 milvus publish 端口）；`docker network ls` 见 `<base>-net`；`appdeploy_service_instance` 一行 `kind=milvus, supply_mode=dedicated, container_name=mwmilvus-<short>, port∈9700-9799`；binding `strategy=dedicated, status=bound`
3. **app 可用**：pymilvus connect → create collection → insert → search round-trip 成功
4. **隔离**：两个 milvus dedicated app = 两套栈、两端口、两网络、独立 collection
5. **回收**：删 app → 三容器 `docker rm` 消失 + `<base>-net` 删除 + instance/binding/env 行清
6. **幂等**：同 app 重部署复用栈（不新建）、collection/数据留存
7. **配额**：端口池满（>100）新 app `binding=failed`，`last_error` 含「端口池」
8. **零回归**：P1 bind_existing / P2 shared / P3 redis dedicated 链路、`DATABASE_URL` 注入、部署主流程不受影响；redis dedicated 单测全绿；Delete 仍正常（pgsupply.Cleanup + mwReconciler.Cleanup 都跑，redis/milvus 各按 kind rm）
9. **平台保护**：手改 `MILVUS_ADDR` 返 409（source=platform）

---

_本设计把 mwsupply 的 dedicated 从 redis 推进到 milvus：`supplyDedicated` 重构为 kind 分派骨架（redis 路径逐字保留），milvus 起专属网络 + milvus/etcd/minio 三容器（1:1 复刻 .28 yxt-milvus 配方，镜像全缓存），alpine 探针长超时就绪（绕过 cross-network 坑 + best-effort 降级），Cleanup 按 kind rm 三容器+网络。零迁移、零 main.go、零 handler 改动（P3 已铺好）。milvus shared / 鉴权 / 数据卷 / UI 勾选留后续。审核通过后开 plan → 实现。_

---

## 15. e2e 验证结论（.28，2026-08-02，commit 03c1ab1）

P4 dedicated milvus 已 `.28` live 验证通过（python:3-alpine app，`.anp/deps.yaml` `strategy:dedicated`）：

- **供给**：deploy 后 `docker ps` 见 `mwmilvus-<short>` + `-etcd` + `-minio` 三容器（仅 milvus `0.0.0.0:9700->19530`，sidecar 不 publish）；`docker network ls` 见 `<base>-net`；`appdeploy_env` 一行 `MILVUS_ADDR=10.10.0.28:9700`（source=platform，**无 MILVUS_PASSWORD / 无 MILVUS_DB**——仅此一个 MILVUS% key，证 milvus 分支）；`appdeploy_service_instance` 一行 `kind=milvus, supply_mode=dedicated, port=9700, container_name=mwmilvus-<short>, status=active`；binding `strategy=dedicated, status=bound`。
- **app→milvus 可达**：app 容器内 `nc -z 10.10.0.28 9700` = **APP_TO_MILVUS_TCP_OK**；app 自身 `/` 报 `MILVUS_ADDR=10.10.0.28:9700` + `TCP_CHECK=OK`（app 容器走 host LAN IP:publish 端口达 milvus，同 redis dedicated P3 范式）。
- **就绪探针实测**：手动起一套临时栈（复刻 `RunMilvusStack` 命令），alpine 探针 `http://milvus:9091/healthz` **~10s 返 OK**（milvus v2.6.15 启动比预估 30-90s 快；120s 超时是安全冗余，best-effort 未触发）。探针经 docker socket 起临时容器加入 `<base>-net`，**不受 .28 cross-network 坑影响**。
- **回收**：DELETE app → `mwmilvus-<short>` + `-etcd` + `-minio` 三容器 `docker rm` 消失 + `<base>-net` 网络删除 + `appdeploy_service_instance`/`binding`/`env` 行清 0 + 9700 端口释放（Cleanup 先 docker rm 三容器+网络，再删 binding 解 FK，再删 instance，实证正确）。
- **镜像全缓存**：`milvusdb/milvus:v2.6.15` / `quay.io/coreos/etcd:v3.5.16` / `minio:v20.2.5-2024.7.4` / `alpine:3.19` 均 .28 本地已存（消除 P2/P3 慢拉镜像风险）。

**⚠️ 既有发现（非 P4 引入，记后续）**：pymilvus 向量 CRUD（create collection/insert/search）在 .28 被 **numpy/CPU 不匹配**阻断——pip 装的 numpy 编译为 X86_V2 baseline，而 .28 CPU 老（pre-X86_V2）不支持，import 即崩（`NumPy was built with baseline optimizations: X86_V2 but your machine doesn't support`）。这是 **app 端可移植性问题**（pymilvus 依赖 numpy），非 P4 供给问题：milvus 服务本身健康（探针 OK）、可达（TCP_OK）、供给/注入/回收全对。对策（后续/app 侧）：pin `numpy<2` 或用兼容老 CPU 的 wheel，或用 milvus REST 客户端免 numpy。

**隔离 / 重部署复用**：单测全覆盖（端口池单增隔离、重部署不重启栈保数据）；e2e 单 app 实证供给机制，两 app 隔离 / 重部署复用逻辑等价（`allocPort` 最小未占用号 + reuse 判定，单测护栏）。

e2e 脚本：`/root/e2e-milvus.sh`（注：状态轮询路由应用 `/apps/:aid/detail` 而非 `/apps/:aid`，后者 404 致脚本 `set -e` 中断；本次 deploy 实际成功，手查补完状态/cleanup 验证）。fixture 宿主 `/opt/anp/data/milded1`（=容器 `/data/milded1`）。

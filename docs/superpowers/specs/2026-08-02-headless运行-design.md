# headless 应用运行态（无端口 + 进程存活健康监控）

> 日期:2026-08-02 ｜ 状态:待评审
> 路线图来源:`docs/PRD/2026-08-01-多形态应用治理与开发运维统筹-PRD.md` §4.1 + 路线图「中期-运行态 ③ headless 运行(无端口/进程健康)」。
> 与 `2026-07-29-非web应用全流程-design.md` 的关系:那份是**产物态**(desktop/mobile/cli 出安装包，明确把运行态排除)，本份是**运行态**(headless 在 ANP 容器里跑)，两份正交。

## 1. 背景与目标

PRD §4.1 把「在 ANP 运行的应用」分为几类组件:`web`/`api`/`headless(bot/worker)`/`data`/`middleware`。其中:

- **web/service**:容器运行 + 暴露 HTTP 端口 → HTTP 探活 + appgw URL。
- **headless(bot/worker)**:容器运行 + **无端口、无 URL** → **进程存活健康**(非 HTTP 探活)。

当前平台(`appdeploy` 限界上下文)只支持 HTTP 中心化链路:

- `Deployer.Deploy()`(`deployer.go:151`)**永远** `-p host:internal` + 拼 `URL` + 注入 `PORT=`,无端口应用无法部署。
- `Application`/`AppInstance` 的 `InternalPort`/`HostPort`/`URL` 都是 HTTP 中心字段。
- **没有任何应用级健康 reconcile 循环**:应用/实例 `status` 部署时设一次,容器崩溃只靠 docker `--restart unless-stopped` 兜底,DB status 不会自动更新、不告警。`monitor.go` 是节点/宿主指标采集,`ops/health.go` 是平台组件(db/agent-runtime/opencode)健康,都不是应用级。

**目标**:让平台支持 headless(bot/worker)应用——无端口部署 + 进程存活健康监控(status 自动更新 + 崩溃/crash-loop 检测 + ops 告警联动),补上「应用运行态无监控」的缺口。

## 2. 已确认的需求边界(决策表)

| 维度           | 选择                               | 说明                                                                                          |
| -------------- | ---------------------------------- | --------------------------------------------------------------------------------------------- |
| 健康信号       | **进程存活为主**                   | `docker inspect` 容器 running 态 + RestartCount;外连探活留二期                                |
| 探活触发       | **后台周期 reconcile**             | 新增 goroutine,30s 一轮,auto-update status,unhealthy 联动 ops 告警                            |
| 外连探活       | **二期**                           | backend 容器拨中间件 host 端口跨网不可达(见 §3 约束),需 sidecar/docker exec 正经方案,本期不做 |
| 探活目标来源   | (二期)自动从注入 env 推导          | 本期不落地                                                                                    |
| 建模           | **新增 `headless` AppKind**        | 与 web/service 并列,probe 分派干净;不复用 service+无端口信号                                  |
| 应用形态范围   | 长驻 bot/worker                    | cron/一次性 job 不在范围                                                                      |
| 构建方式       | 自带 Dockerfile(同 web)            | `docker build`,不造专用 builder,不产出 artifact                                               |
| reconcile 范围 | **本期只 reconcile headless 实例** | gate `app_kind=headless`,不碰 web 实例(避免 web 回归)                                         |

## 3. 关键约束(已踩坑,决定设计)

**backend 容器拨不动中间件 host 发布端口**(`mwsupply/supply.go:206-208` 注释 + 记忆 `mwsupply` P3 + commit `984f685`):

- dedicated 中间件容器注入给 app 的 `REDIS_ADDR = <docker宿主IP>:<发布端口>`。
- app 容器(默认 bridge 网)能拨到;**backend 容器(deploy_default 网)拨这个 host 发布端口会超时**(跨网/防火墙/hairpin),P3 ping 因此降级 best-effort。

**推论**:「HealthReconciler 在 backend 里周期性 dial `REDIS_ADDR`/`DATABASE_URL`」**不可靠**,会重蹈 P3 覆辙、把健康 app 误判 degraded。故本期外连探活不做,只做走 docker socket 的**进程存活**(socket 不受应用网络拓扑影响,稳)。

## 4. 数据模型

### 4.1 AppKind 加 `headless`

`model.go` 加常量:

```go
const (
    AppKindWeb      = "web"
    AppKindDesktop  = "desktop"
    AppKindMobile   = "mobile"
    AppKindCLI      = "cli"
    AppKindService  = "service"
    AppKindHeadless = "headless" // 新增:无端口长驻进程(bot/worker),进程存活健康
)
```

建应用校验 oneof 加 `headless`。**无 DB CHECK 约束**(沿用现有 Go 层校验),故 **无 schema 迁移**(headless 自带 Dockerfile,不需要 `appdeploy_build_config` 记录)。

### 4.2 状态语义(复用现有列 + 加 1 列 restart_count)

复用 `appdeploy_instance.status` + `last_error` + `updated_at`,**新增 1 列 `restart_count`**(存上次观测的 docker RestartCount,做增量判定,见 §4.4):

| docker inspect 观测                                  | status     | 含义                               | 告警          |
| ---------------------------------------------------- | ---------- | ---------------------------------- | ------------- |
| `State.Status=running` 且本周期新增重启数 `< burst`  | `running`  | 健康                               | —             |
| `running` 且本周期**新增重启数 ≥ burst**(crash-loop) | `degraded` | docker 在反复重启它                | warning 告警  |
| `exited`/容器不存在(本该 running)                    | `failed`   | 崩溃/被杀(OOMKilled 记 last_error) | critical 告警 |
| 用户主动停                                           | `stopped`  | reconcile 跳过(不纳入巡检)         | —             |

**关键:crash-loop 用「增量」判,不用累计 `RestartCount`。** docker 的 RestartCount 是累计只增不减,若用「≥阈值」判 degraded 会**粘住**——一旦达阈值就永远回不去 running(累计值不降)。改用「本巡检周期(30s)内新增重启数 ≥ `restartBurst`(默认 3)」判 degraded:应用稳下来后无新增重启 → 自然恢复 running,非粘。

另:`headless` 用 `--restart unless-stopped`,崩溃的应用会被 docker 反复重启、绝大多数时间显示 `running`,故「exited=failed」反而少触发——**crash-loop(degraded)才是主要的不健康信号**,所以增量判定是核心,不是可选。

- `restartBurst=3`(单周期新增重启阈值)、`healthInterval=30s`,走配置常量(可调)。
- `stopped` = 用户经 Stop API 主动停,reconcile 不巡检 status=stopped 的实例。
- "上次状态翻转时间"用 `updated_at`(reconcile 仅在 status 翻转时写 DB,故 updated_at 即上次翻转)。

### 4.3 健康观测结构

```go
// ContainerHealth docker inspect 解析出的容器健康观测。
type ContainerHealth struct {
    Running      bool // State.Status == "running"
    RestartCount int  // RestartCount(累计,只增)
    ExitCode     int  // State.ExitCode(exited 时有意义)
    OOMKilled    bool // State.OOMKilled
}
```

### 4.4 迁移:appdeploy_instance 加 restart_count 列(编号 000032)

```sql
ALTER TABLE appdeploy_instance ADD COLUMN IF NOT EXISTS restart_count INT NOT NULL DEFAULT 0;
```

`AppInstance` struct 加 `RestartCount int \`db:"restart_count"\``。存 reconcile 上次观测的 docker RestartCount,做增量判定(§4.2)。`insCols`、`UpdateInstance`/`SetInstanceStatus` 的 SQL 补这列。幂等(`IF NOT EXISTS`),存量行默认 0。

## 5. 部署链路改动(`deployer.go`)

### 5.1 Deploy 加 headless 分支

`Deployer.Deploy(ctx, a *Application, ins *AppInstance, env []string, dockerHost string)` 在拼 `docker run` 参数时按 `a.AppKind` 分支:

- **headless**:跳过 `-p host:internal`、跳过 `ensurePORTEnv`、不设 `ins.HostPort`/`ins.URL`。仅 `docker run -d --name <name> --restart unless-stopped -e KEY=VAL ... <image>`。
- **其余 kind**:原逻辑完全不动。

headless 不占宿主端口(省 test/prod 端口池)。容器名规则沿用现有 `appdeploy-<slug>-<env>-v<n>`。

### 5.2 新增 InspectHealth

```go
// InspectHealth 经 docker inspect 取容器健康观测。走 docker socket,不受应用网络拓扑影响。
func (d *Deployer) InspectHealth(ctx context.Context, container string) (ContainerHealth, error)
```

实现:`docker inspect --format '{{.State.Status}}|{{.RestartCount}}|{{.State.ExitCode}}|{{.State.OOMKilled}}' <container>`,纯解析(`parseInspectHealth(out string) (ContainerHealth, error)`,可单测)。容器不存在(inspect 报错)→ 返回 `Running=false` + 带 err(由调用方决定记 last_error 而非误判崩溃,见 §8)。

### 5.3 Build 不变

`Deployer.Build()` `docker build -t ...` 对 headless(自带 Dockerfile)天然适用,不改。

## 6. HealthReconciler(新增 `appdeploy/health_reconcile.go`)

仿 `ServerMonitor.Start`(`monitor.go:97`)起后台 goroutine 的既有模式。

### 6.1 结构与装配

```go
type HealthReconciler struct {
    store            InstanceHealthStore  // 列 headless 活跃实例 + 更新 status
    deployer         HealthInspector      // InspectHealth
    alerter          HealthAlerter        // 告警解耦
    interval         time.Duration // 30s
    restartBurst     int           // 3(单周期新增重启阈值,判 crash-loop)
}

func NewHealthReconciler(store InstanceHealthStore, deployer HealthInspector, alerter HealthAlerter) *HealthReconciler
```

`main.go` 装配:与 `ServerMonitor` 一起 `go healthRec.Start(ctx)`。

### 6.2 巡检循环

```text
每 healthInterval(30s) 一轮:
  targets = store.ListHeadlessActiveInstances()
     // SELECT i.app_id, i.env, i.container_name, i.status, i.restart_count, a.project_space_id, a.name
     //   FROM appdeploy_instance i JOIN appdeploy_application a ON a.id=i.app_id
     //   WHERE a.app_kind='headless' AND i.status IN ('running','degraded')
  for t in targets:
    h, err = deployer.InspectHealth(t.container_name)
    if err != nil:
       // inspect 失败(docker 不可达/容器名查不到)→ 记 last_error,本轮跳过,不误判崩溃(§8)
       store.SetInstanceStatus(t.app_id, t.env, t.status, "inspect 失败: "+err, "")
       continue
    newStatus, newCount = aggregateHealth(h, t.restart_count, restartBurst)  // 纯函数,见 §6.3
    // 每轮都回写观测到的 restart_count(无论是否翻转,保证下轮增量基准正确)
    if newStatus != t.status:
       reason = describe(h, t.restart_count)  // "OOMKilled"/"exit 137"/"crash-loop +N restarts"
       store.UpdateInstanceHealth(t.app_id, t.env, newStatus, reason, newCount)  // status+last_error+restart_count
       if newStatus in (degraded, failed):
          alerter.OnUnhealthy(ctx, t.project_space_id, t.app_id, t.name, t.env, newStatus, reason)
       if newStatus == running:
          alerter.OnRecovered(ctx, t.project_space_id, t.app_id, t.name, t.env)
    else if newCount != t.restart_count:
       store.UpdateRestartCount(t.app_id, t.env, newCount)  // 仅更新基线,不动 status
```

- **`restart_count` 每轮回写**(翻转时随 status 一起写,未翻转时单独写),保证下轮 `delta = h.RestartCount - stored` 基准正确——这是增量判定非粘的关键。
- **status 仅翻转时写**,无翻转不触告警 → 翻转才告警天然去重。
- 新增 store 方法:`ListHeadlessActiveInstances()`(上述 JOIN 查询,带 restart_count + project_space_id)、`UpdateInstanceHealth(appID,env,status,lastErr,restartCount)`、`UpdateRestartCount(appID,env,count)`(或在 `SetInstanceStatus` 扩展 restart_count 参数)。`SetInstanceStatus` 现有签名 `(appID,env,status,lastErr,buildLog)`,扩展为带 restart_count 或新增方法,实现期定。
- **接口缝(轻量)**:单实例检查抽成 `aggregateHealth(...)` 纯函数;未来加 web 进程探活/外连探活时扩展此函数与 targets 查询的 kind 过滤。**本期不为单一实现造正式 interface**(YAGNI)。

### 6.3 状态聚合纯函数

```go
// aggregateHealth 由 docker inspect 观测 + 上次存储的 restart_count 推导新 status 与新基线计数。
// 纯函数,可单测。增量判定 crash-loop(非粘)。
func aggregateHealth(h ContainerHealth, storedCount, burst int) (newStatus string, newCount int) {
    newCount = h.RestartCount
    if !h.Running {
        return "failed", newCount
    }
    if storedCount == 0 && h.RestartCount > 0 {
        // 冷启动基线:首次观测到历史重启,只记录基线不告警(避免旧容器一上线就假 degraded)
        return "running", newCount
    }
    if newCount-storedCount >= burst {
        return "degraded", newCount // 本周期新增 ≥burst 次重启 = 活跃 crash-loop
    }
    return "running", newCount
}
```

- 冷启动基线(`storedCount==0 && h.RestartCount>0`)→ 只记 running 不告警;后续周期靠 delta。
- 恢复:曾 degraded,本周期 delta < burst(无新增重启)→ running,非粘。

## 7. 告警集成(ops)

### 7.1 HealthAlerter 接口(appdeploy 内定义,解耦)

```go
type HealthAlerter interface {
    OnUnhealthy(ctx, projectSpaceID, appID, appName, env, severity, reason string) error
    OnRecovered(ctx, projectSpaceID, appID, appName, env string) error
}
```

appdeploy 不直接 import ops;由 `main.go` 注入 ops 提供的实现。

### 7.2 ops 实现(复用现有 Alert 表 + 方法)

ops store(`ops/store.go`)已有:`CreateAlert`(自动算 fingerprint)、`HasFiringFingerprint`(去重)、`ResolveAlert`(按 id resolve)、`fingerprint(source,title)`。**不新增表**,仅补 1 个便捷方法:

```go
// ResolveByFingerprint 按 fingerprint 把 firing 告警置 resolved(HealthReconciler 恢复时用)。
func (s *Store) ResolveByFingerprint(ctx context.Context, fp string) error
```

HealthAlerter 的 ops 实现:

- `title` 固定格式 `"应用 <name> <env> 异常"`(同实例同环境 → 同 title → 同 fingerprint,稳定)。
- `OnUnhealthy`:`fp = fingerprint("apphealth", title)`;`HasFiringFingerprint(fp)` 为真则跳过(已 firing,去重);否则 `CreateAlert({source:"apphealth", severity:failed→critical/degraded→warning, status:"firing", title, description:reason})`。
- `OnRecovered`:`ResolveByFingerprint(fp)` 把该实例环境的 firing 告警置 resolved + resolved_at(若无 firing 记录则 no-op)。

## 8. 错误处理

- **inspect 失败**(docker daemon 不可达、容器名查不到、超时):**不判崩溃**,记 `last_error`、保留原 status、本轮跳过。避免"docker 抽风"误报全量 app down。
- **crash-loop 判 degraded 而非 failed**:容器其实在 running(docker 还在重启它),只是不稳,用 degraded 区分"彻底崩了"。
- **告警 spam 防护**:翻转才 fire + fingerprint 去重(§7),不会每轮重复。
- **reconciler panic**:循环体 recover,单实例异常不影响其他(仿 ServerMonitor 的容错风格)。

## 9. 前端(应用详情页)

- headless 应用:实例行**不显示 URL/访问链接**,改显「运行健康」徽标:
  - `running`(绿)/`degraded`(黄)/`failed`(红),复用 ops 现有健康色板。
  - 旁边显上次状态翻转时间(`updated_at`)+ `last_error` 摘要。
- 数据来自现有实例接口(`status` 字段已带新值),**无需新端点**。
- 判定逻辑:`app.app_kind === 'headless'` 时渲染健康徽标替代 URL,其余形态不变。
- web/desktop/mobile/cli/service 详情页零改动。

## 10. 测试

纯函数优先 + fake 注入(呼应 [[sqlite-test-pg-type-trap]]:纯函数无 DB 类型陷阱;reconcile 用 fake 不碰真 PG):

1. `parseInspectHealth`:各种 inspect 输出(running/exited/OOMKilled/缺字段)→ 正确 `ContainerHealth`。
2. `aggregateHealth`(增量判定):exited→failed;running 且 delta≥burst→degraded;running 且 delta<burst→running;**冷启动**(stored=0、RestartCount>0)→running 不告警;**非粘恢复**(曾 degraded,本轮 delta=0)→running。
3. Deploy headless 分支:fake `dockerRun` 捕获 args → 断言**无 `-p`、无 `PORT=`、未设 URL**;web 分支回归(有 `-p`/URL)。
4. reconcile 翻转:fake deployer(inspect 注入)+ 内存 store + fake alerter → 崩溃翻 failed+`OnUnhealthy`;crash-loop(delta≥burst)翻 degraded+`OnUnhealthy`;恢复(delta 归 0)翻 running+`OnRecovered`;**restart_count 每轮回写**(翻转/未翻转都更新基线);无 status 翻转不告警;inspect 失败只记 last_error 不翻。
5. 告警去重:同实例连续两轮 unhealthy → `HasFiringFingerprint` 命中只一条 open alert;恢复 → `ResolveByFingerprint` 置 resolved。

## 11. .28 端到端验证(按 [[deploy-28-no-local-test]])

本机只 `go build` 编译验证;.28(anp_dev 库 + docker)端到端:

1. 建 headless 应用(指定 app_kind=headless)→ 部署 test → 确认**无 `-p`、无 URL**、容器在跑(`docker ps`)。
2. `docker stop <container>` 模拟崩溃 → 等 30s → 确认 instance status 翻 `failed` + ops `Alert` 表出 critical 告警(source=apphealth)。
3. `docker start <container>` 恢复 → 等 30s → 确认 status 翻 `running` + 告警 `resolved`。
4. crash-loop:手动让容器反复退出 → 确认 RestartCount 累计后 status 翻 `degraded` + warning 告警。
5. web 应用回归:部署/停止/详情全链路行为不变(status 不被 reconciler 改动)。
6. 前后端交叉验证([[verify-cross-frontend-backend]]):真开 headless 应用详情页 → 确认健康徽标正确渲染(非 curl);崩溃/恢复后刷新页面徽标实时变化。

## 12. 与现有九大板块的关系

| 板块           | 本期改动                                                                            |
| -------------- | ----------------------------------------------------------------------------------- |
| 2 研发工作台   | 顶部「构建部署」对 headless 仍走 build→run,但 run 无端口(部署器内分支)              |
| 6 发布中心     | headless 部署 test/prod 两环境实例,无 URL                                           |
| 7 运维中心     | **核心**:新增 HealthReconciler 巡检 + Alert 联动(headless 实例)                     |
| 横切·appdeploy | `headless` AppKind + Deploy 无端口分支 + InspectHealth + HealthReconciler(核心改动) |
| 其余板块       | 无改动                                                                              |

## 13. 本期不做(二期/愿景)

- **外连探活**(进程存活之外的功能性健康):需 sidecar 或 docker exec+下发探针二进制的正经网络方案,backend 直拨已证不可行。
- **探活目标自动从注入 env 推导**(REDIS_ADDR/MILVUS_ADDR/DATABASE_URL → dial):随外连探活二期落地。
- **web/service 进程探活纳入 reconcile**:本期 gate 仅 headless;web 进程探活/HTTP 周期探活另立。
- **自愈(auto-restart SOP)**:本期 reconcile 只观察+告警;自动重启/SOP 联动留 ops 自愈子题。
- cron/一次性 job 形态。

## 14. 验收标准

1. 建 `headless` 应用 → 部署 → 容器在跑、**无端口映射、无 URL**(详情页显健康徽标不显链接)。
2. 容器崩溃(`docker stop` 或让其退出)→ 30s 内 instance status 自动翻 `failed` + ops 出 critical 告警(source=apphealth)。
3. 容器恢复 → status 自动翻 `running` + 告警 resolved。
4. crash-loop(容器反复退出、docker 频繁重启,单周期新增 ≥burst)→ status 翻 `degraded` + warning 告警;**稳定后无新增重启 → 自动恢复 running**(非粘)。
5. `restart_count` 列每轮回写正确(增量基准),迁移 000032 落地、存量行默认 0。
6. inspect 失败(docker 抽风)→ 不误判崩溃,只记 last_error。
7. web/service/desktop/mobile/cli 全链路行为不变(回归,status 不被 reconciler 改动)。
8. 详情页健康徽标真实渲染(前后端交叉验证)。

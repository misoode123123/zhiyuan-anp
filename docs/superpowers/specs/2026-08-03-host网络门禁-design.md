# host 网络门禁 — 设计（PRD §7 中期-运行态 ④）

- 关联 PRD：[多形态应用治理 PRD §4.4 / §7 / §8 / §9.3](../../PRD/2026-08-01-多形态应用治理与开发运维统筹-PRD.md)（本设计 = §7 中期-运行态最后一项 ④「host 网络门禁」，做完即关闭整个中期-运行态里程碑）
- 状态：设计待审

---

## 1. 背景与目标

### 1.1 PRD 要求

- §4.4 网络策略：**bridge 默认；`host` 按应用+审批+角色门禁，不默认开**。
- §8 风险：host 网络削弱隔离 → 不默认；按应用+审批+角色门禁；优先 bind 可达实例。
- §9.3 待决议项 #3：**复用变更闸门 + 加网络策略字段，还是单设审批？** ← 本设计拍板（见 §3）。

### 1.2 现状核验

- platform/backend **无任何 `network_mode` / `HostNetwork` 处理**（grep 零命中）——属全新能力。
- 应用容器全部跑在 docker **默认 bridge**：`deployer.Deploy`（`appdeploy/deployer.go:148`）拼 `docker run` args 时从不指定 `--network`，web/service 靠 `-p hostPort:internalPort`（`:180`）发布端口。
- Docker 操作全是 **CLI shell-out**（`docker run`，无 Docker Go SDK）→ 加 `--network host` 仅需在拼 args 处加 flag，**零新依赖**。已有 `--network` CLI 范式：`mwsupply/docker.go`（给 milvus 建专属网络）。
- 两套现成「审批/授权」范式：① 变更闸门 `change_request`（审 **AI 代码变更**，字段 repo_dir/prompt/model/output 代码专用）；② **prod 部署的 RBAC 角色门禁**（`app.deploy.prod` → 仅 gatekeeper/admin，handler 内 env 敏感二次鉴权 `handler.go:1479`）——**这本身就是 prod 的「审批」**，无独立工单。

### 1.3 目标

让应用可在声明+授权后以 `--network host` 运行（容器共享宿主网络命名空间，直接绑宿主端口、直达 host-LAN），用于少数确需 host 网络的用例；默认 bridge 不变，host 需 gatekeeper/admin 角色开启并部署。

---

## 2. 范围

### 2.1 本期做（in）

- 数据模型：`appdeploy_application` 加 `network_mode` 列（migration `000034`）。
- RBAC：新 op `app.net.host`（gatekeeper/admin）。
- **两道门禁**：set-time（改 network_mode→host 需角色）+ deploy-time（部署 host 应用需角色）。
- 容器层：`deployer.Deploy` 加 host 分支（`--network host`、跳 `-p`/端口分配、`port=internalPort`、复用 `ins.HostPort` 语义 → appgw 路由+URL 零改动）。
- HTTP API：专用 `PUT /project-spaces/:id/apps/:aid/network-mode`（单职责，gate 干净）。
- 前端：应用设置加 network_mode 选择器（非授权角色置灰+提示）。
- `.28` e2e：正路径（host 容器验 `NetworkMode=host`+无 `-p`+绑宿主端口+appgw 可达）+ 负路径（dev 设/部署 host → 403）+ 改回 bridge 恢复。

### 2.2 本期不做（out / YAGNI）

- **独立审批工单 / 复用变更闸门**：本期用角色门禁（§3 决策 A）；两人 request→approve 流（change_request 加 kind='network' 或新建审批表）= 未来若安全策略要求再上。
- **per-env 网络策略**（test/prod 不同 network_mode）：v1 字段 app 级（两 env 共用），与「一个 app 一份运行时配置」一致。
- **custom network name / 端口白名单 / 网络隔离细粒度**：v1 仅 bridge|host 二选一。
- **host 模式 + 数据卷**：当前本就无 volume 逻辑（`docker run` 无 `-v`），host 门禁不引入 volume。
- **NativeDeployer（ssh/winrm 原生部署）的 network_mode**：原生部署跑在裸主机（本就是 host 语义），忽略 `network_mode`。

---

## 3. 关键决策

| 决策                                               | 取舍                                                                                                                                                                                                                                                                                                       |
| -------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **审批机制 = 角色门禁（PRD §9.3 → A）**            | 与 prod 部署同款「角色即审批」：gatekeeper/admin 开启+部署 host，无独立工单。rejected 复用变更闸门（B）：`change_request` 字段是代码变更专用，host 是运行时策略，语义勉强。rejected 独立审批表（C）：为罕见逃生口建一整套审批，YAGNI 偏重。未来需两人流可从 A 平滑升级（change_request 加 kind='network'） |
| **network_mode = app 级标量列**                    | 非 per-env、非 JSONB。`appdeploy_application` 全标量列无策略字段，加一列最简；JSONB `runtime_policy` 为单字段过度。app 级粒度与 deploy_mode/app_kind 同层                                                                                                                                                  |
| **专用 `PUT /network-mode` 端点**                  | 单职责端点 gate 最干净（一个 handler 一个 op 检查），镜像 deps-ui 专用端点范式。rejected 折进通用 app 更新：通用更新自带校验，且 network_mode 是特权字段宜独立                                                                                                                                             |
| **`ins.HostPort` 复用为「host 命名空间可达端口」** | host 模式无 `-p`，但 app 绑 `internalPort` 于宿主 → `ins.HostPort=internalPort`。下游 `handler.go:1762` 的 `routeWriter.UpsertRoute(..., ins.HostPort)` 与 `ins.URL` 逻辑**零改动**即路由到 `host:internalPort`                                                                                            |
| **host 分支跳 `-p`+端口分配，`port=internalPort`** | host 模式无端口映射语义；`ensurePortEnv` 仍注入（PORT=internalPort，app 在 host 上绑该端口）                                                                                                                                                                                                               |
| **set-time + deploy-time 双 gate**                 | set-time 防 dev 私开 host；deploy-time 防 dev 部署他人开的 host 应用。两道都校验 `app.net.host`，镜像 prod 的 RBAC 二次鉴权范式                                                                                                                                                                            |

---

## 4. 数据模型（migration `000034`）

```sql
-- 000034_app_network_mode.up.sql
ALTER TABLE appdeploy_application
  ADD COLUMN network_mode TEXT NOT NULL DEFAULT 'bridge';
-- 值域：'bridge'（默认）| 'host'。应用层校验，DB 不加 CHECK（同 deploy_mode/app_kind 现状）。
```

```sql
-- 000034_app_network_mode.down.sql
ALTER TABLE appdeploy_application DROP COLUMN network_mode;
```

- Go 模型 `Application`（`appdeploy/model.go:15-39`）+`NetworkMode string` 字段。
- `appCols()`（`store.go:22-24`）显式列 +`network_mode`；`Scan`/`INSERT`/`UPDATE` 同步。
- 默认 `'bridge'`：历史应用零影响，行为不变。

**零新表、零新 FK、零新索引**（只加一列）。

---

## 5. RBAC + 门禁

### 5.1 新 op（`auth/permission.go` OpRoles）

```go
"app.net.host": {RoleGatekeeper, RoleAdmin},
```

未注册的 op `Allowed` 默认放行（`permission.go:38-39`），故**必须显式登记**才会生效。

### 5.2 set-time 门禁（改 network_mode）

`PUT /project-spaces/:id/apps/:aid/network-mode`（body `{"mode":"host"|"bridge"}`）handler：

```
roles = rolesFromCtx(c)
if !auth.Allowed("app.net.host", roles) → 403「需 gatekeeper/admin 开启 host 网络」
校验 mode∈{bridge,host}（非法 → 400）
store.UpdateNetworkMode(appID, mode)
```

- **任何方向改动**（host→bridge 也算）都需 `app.net.host`：invariant 简单——只有授权角色能碰 network_mode。
- 镜像 `handler.go:1479` 的 env 敏感鉴权范式（`rolesFromCtx` + `auth.Allowed`）。

### 5.3 deploy-time 门禁（部署 host 应用）

`Deploy`（`handler.go:1465`）/ `Promote`（`:1550`）/ `DeployCommit`（`:1605`）三个 HTTP 入口，在现有 env 敏感部署检查（`:1479`/`:1557`/`:1617`）旁加：

```
if app.NetworkMode == "host" && !auth.Allowed("app.net.host", roles) {
    → 403「host 网络应用需 gatekeeper/admin 部署」
}
```

- **同步检查**（roles 只在 HTTP 层可得），失败 fast，**不进 `buildAndDeploy` goroutine**（goroutine 脱离 HTTP context 拿不到 roles）。
- 三个入口各加一处（或抽 `checkHostDeployGate(app, roles)` helper 复用）。
- `app.net.host` **不分 test/prod，均为 gatekeeper/admin**——host 是特权操作，部署到 test 也须授权角色（比 bridge 的 test=dev 允许更严，符合 host 风险定位）。
- external 应用（`deploy_mode=external`）不经 `buildAndDeploy`，gate 对其无意义（同 deps-ui 门控）。

---

## 6. 容器层（`deployer.Deploy:148`）

`a *Application` 已携带 `NetworkMode`，**Deploy 签名不变**。`network_mode` 与 `app_kind` **正交**（任意类型都可 host），故 host flag 在分支前统一加，避免 headless 分支先 return 吞掉 host：

```
func Deploy(ctx, a, ins, env, dockerHost):
    name  = appdeploy-<slug>-<env>-v<ver>
    args  = [run, -d, --name, name, --restart, unless-stopped]
    isHost := a.NetworkMode == "host"
    if isHost { args = append(args, "--network", "host") }   # 所有 app_kind 统一加

    switch {
    case a.AppKind == headless:            # 既有：无 -p/无 PORT/无 appgw（不变）
        ...(仅 -e + image)
    case isHost:                           # web/service + host：无 -p
        env = ensurePortEnv(env, a.InternalPort)
        args += [-e × len(env)] + [ins.Image]
        dockerRun(...)
        ins.ContainerName = name
        ins.HostPort = a.InternalPort      # host 命名空间可达端口
        ins.URL      = http://<urlHost>:<a.InternalPort>
    default:                               # web/service + bridge（既有：端口分配 + -p，不变）
        ...
    }
```

- host flag 对 headless 也生效（headless+host：无 -p 无 appgw，但共享宿主网络，用于需直达 host-LAN 的 worker/bot）。
- web/service+host：**跳过** `envPortRange`/`usedPortsOn`/`AllocFreePort`（host 无端口映射，不占 9100-9199/9200-9300 段）。
- `ins.HostPort = a.InternalPort` → 下游 `routeWriter.UpsertRoute(ctx, appID, psID, env, deployer.host, ins.HostPort)`（`handler.go:1763`）天然路由到 `host:internalPort`；`ins.URL` 同理。**appgw 路由写入 + URL 逻辑零改动**。
- `ensurePortEnv` 仍注入 `PORT=internalPort`（app 在 host 上监听该端口）。
- 远程节点（`dockerHost` 非空）：`--network host` 指向**该远程节点**的 host 网络（语义正确，文档化）。

---

## 7. HTTP API（`appdeploy/handler.go`，挂 `Handler.Register`）

| 方法 | 路径                                         | handler          | 作用                                                                        |
| ---- | -------------------------------------------- | ---------------- | --------------------------------------------------------------------------- |
| PUT  | `/project-spaces/:id/apps/:aid/network-mode` | `PutNetworkMode` | body `{"mode":"host"\|"bridge"}`；`app.net.host` gate → `UpdateNetworkMode` |

- network_mode 的**读取**随应用详情返回（`GET .../apps/:aid/detail` 或应用查询的 Application 序列化已含新字段），不单设 GET。
- 鉴权：单 op `app.net.host`（set-time）。

---

## 8. 前端 UI（`platform/frontend/app/applications/page.tsx`）

应用设置面板（与 env vars / instances / 依赖 section 并列）加**「网络模式」**项：

- 选择器：Bridge（默认）/ Host。
- 当前用户非 gatekeeper/admin → **置灰 + 提示**「host 网络需 gatekeeper/admin 开启」；保存按钮禁用。
- 授权用户改选 Host → `PUT /network-mode {mode:"host"}` + toast「已开启 host 网络，下次部署生效」。
- hint：「host 模式下容器共享宿主网络，直接占用宿主端口；默认 bridge 更安全」。

---

## 9. 模块 / 文件改动

| 文件                                                                     | 改动                                                                                                                                                                                                              |
| ------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `internal/db/migrations/pg/000034_app_network_mode.up.sql` / `.down.sql` | +`network_mode` 列（up/down）                                                                                                                                                                                     |
| `internal/appdeploy/model.go`                                            | `Application` +`NetworkMode string`                                                                                                                                                                               |
| `internal/appdeploy/store.go`                                            | `appCols()` +`network_mode`；Scan/Insert/Update 同步；+`UpdateNetworkMode(ctx, appID, mode) error`                                                                                                                |
| `internal/auth/permission.go`                                            | OpRoles +`"app.net.host": {gatekeeper, admin}`                                                                                                                                                                    |
| `internal/appdeploy/handler.go`                                          | +`PutNetworkMode` handler + 1 路由（set-time gate）；Deploy/Promote/DeployCommit 三入口 +deploy-time host gate（或抽 helper）                                                                                     |
| `internal/appdeploy/deployer.go`                                         | `Deploy` +host 分支（`--network host`、跳 `-p`/端口分配、`ins.HostPort=internalPort`、`ins.URL`）                                                                                                                 |
| `platform/frontend/app/applications/page.tsx`                            | +「网络模式」选择器（角色门控置灰 + `PUT /network-mode`）                                                                                                                                                         |
| 测试                                                                     | `store`/`handler_http_test`（UpdateNetworkMode、PutNetworkMode 403/400/200、deploy-time 403）、`deployer`（host 分支 args 含 `--network host` 无 `-p`、ins.HostPort=internalPort）、前端 vitest（选择器角色门控） |

**零新依赖、零新表、零新 FK/索引**（1 迁移 + 1 列 + 1 op + 2 道 gate + deployer 1 分支 + UI 1 选择器）。

---

## 10. 测试计划

### 10.1 PG 单测 / handler_http_test（`go test -p 1`）

- `UpdateNetworkMode`：写后读回正确；默认 bridge。
- `PutNetworkMode`：gatekeeper/admin → 200；dev → 403；非法 mode → 400。
- deploy-time gate：`app.NetworkMode=host` + dev 调 Deploy/Promote/DeployCommit → 403；gatekeeper → 放行进 buildAndDeploy。
- `deployer.Deploy` host 分支：args 含 `--network host`、**不含** `-p`、`ins.HostPort==a.InternalPort`、`ins.URL` 含 internalPort（用可注入的 `dockerRun` 桩校验 args）。

### 10.2 `.28` 端到端（真前端/真接口驱动后端，`verify-cross-frontend-backend`）

1. **正路径**：gatekeeper `PUT network-mode=host` → 工作台 deploy → `docker inspect <容器>` 验 `"NetworkMode": "host"` + 无 `-p` 端口映射 + app 监听宿主 internalPort + appgw 路由 `host:internalPort` 可达（curl）。
2. **set-time 负路径**：dev `PUT network-mode=host` → 403。
3. **deploy-time 负路径**：gatekeeper 开 host 后，dev deploy 该 app → 403。
4. **改回 bridge**：`PUT network-mode=bridge` → redeploy → 验恢复 `-p` + 端口分配（9100-9199 段）+ `NetworkMode=bridge`。

- 端口选高位未用端口（如 internalPort=18080），别撞 .28 他人服务（lowcode/帆软/腾讯微打）。

---

## 11. 风险与取舍

| 风险                                                                     | 应对                                                                                          |
| ------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------- |
| host 网络削弱隔离（app 见全部宿主接口、可绑任意宿主端口、直达 host-LAN） | 默认 bridge；host 需 gatekeeper/admin 双 gate；UI 置灰+提示；文档化 host 模式应用自负端口冲突 |
| host app 端口撞 .28 他人服务                                             | e2e 选高位未用端口；提示用户 host 模式下端口由应用自管                                        |
| 改回 bridge 后旧 host 容器残留                                           | `buildAndDeploy` 部署前 `RemoveByPrefix`（`handler.go:1692`）清历史容器，redeploy 干净        |
| 远程节点 host = 该节点 host 网络（非 .28）                               | 语义正确，文档化；e2e 在 .28 本地节点验                                                       |
| dev 被 gatekeeper 授权 host 后仍无法部署（deploy-time gate）             | 符合设计（host 部署也需 gatekeeper）——host 是特权操作，全链路授权角色在场                     |

---

## 12. 未覆盖（YAGNI / 后续）

- **两人审批流**（change_request 加 kind='network' 或独立审批表）：未来安全策略要求再上，可从角色门禁平滑升级。
- **per-env 网络策略**（test=bridge/prod=host 等）：network_mode 加 env 维度（instance 级）。
- **custom network name / 端口白名单 / 网络段隔离**：v1 仅 bridge|host。
- **host 模式 + 数据卷**：随 volume 能力一起做（当前无 volume）。

---

## 13. 验收标准

1. migration `000034` 上线，`appdeploy_application.network_mode` 存在且默认 bridge；历史应用行为不变。
2. `PUT /network-mode`：gatekeeper/admin 可改；dev → 403；非法 mode → 400。
3. Deploy/Promote/DeployCommit：`network_mode=host` 应用由 dev 部署 → 403；gatekeeper/admin 放行。
4. host 应用部署后 `docker inspect` 验 `NetworkMode=host` + 无 `-p` + 绑宿主 internalPort + appgw 路由可达；改回 bridge 恢复 `-p`+端口分配。
5. 前端「网络模式」选择器：授权角色可改，非授权置灰+提示。
6. PG 单测 + handler_http_test + deployer 测试全绿（`go test -p 1`）；`.28` e2e 正/负路径全绿；既有 bridge 应用零回归。

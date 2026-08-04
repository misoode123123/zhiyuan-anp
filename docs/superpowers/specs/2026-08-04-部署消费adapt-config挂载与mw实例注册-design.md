# 部署消费 adapt 产物：config 挂载 + mw 实例注册

> 日期：2026-08-04 ｜ 状态：设计稿（待审）
> 触发：导入真实复杂项目 `yxt_eino_v2`（Go 客服机器人）端到端测试，暴露 ANP 部署侧两处消费缺口。

## 1. 背景（实测暴露的缺口）

导入 `yxt_eino_v2`（多 cmd Go 服务 + Redis/Milvus/PG 依赖 + config.yaml）走 ANP 全流程：

- **opencode adapt 极强**：系统性改造代码（新增 `internal/config/env.go` env-over-config 架构、改 Dockerfile 双端口+HEALTHCHECK、声明 `.anp/deps.yaml`），681 行，**非模板、是读码改造**。
- **build 成功**（多阶段 Go build）。
- **但部署后应用崩溃重启**，两处缺口：

| #   | 缺口                   | 现状                                                                                                                                                                                                                                                                                                                 | 实测手动补丁                                                               |
| --- | ---------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------- |
| ①   | **config.yaml 没挂载** | adapt 改的 Dockerfile 假设「ANP 把 config.yaml 作 secret 挂到 `CONFIG_PATH`(缺省 /app/config.yaml)」，但 `deployer.Deploy` 的 docker run **完全没有 `-v` volume 参数** → 容器内无 config.yaml → 应用初始化崩                                                                                                         | 手动改 Dockerfile `COPY config.yaml`（打进镜像，违背 secret 语义、含密钥） |
| ②   | **mw env 没注入**      | adapt 写 `env.go` 读 `REDIS_ADDR/MILVUS_ADDR/PG_HOST`，`deps.yaml` 声明 redis/milvus，但部署 `Reconcile→LookupBindExisting` 查 `appdeploy_service_instance` 表找 bind_existing 实例——**.28 的 yxt-redis/yxt-milvus 是 docker-compose 起的、没注册到 ANP**，且生产库无种子 → 找不到 → 不注入 env → 应用连默认地址失败 | 手动 `UpsertEnv` 8 个 env 指向宿主 IP+映射端口                             |

**根因**：ANP 的 adapt（改造代码）已达生产级，但**部署侧没把 adapt 的假设消费到位**。

## 2. 目标

兑现 adapt 的两个假设，让「导入已含依赖的项目 → adapt → 部署」端到端跑通，无需手动改 Dockerfile 或注入 env：

- **①** 部署时自动把仓库 `config.yaml` 挂载进容器（兑现 secret 挂载假设）。
- **②** 提供运行时注册 bind_existing 中间件实例的 API + UI，让运维把部署机上已有的 redis/milvus/pg 注册给 ANP，部署时自动注入连接 env。

非目标（留后续）：③ 容器网络对接（应用加入依赖网络 / host 自动化）、④ deps 完整性（adapt 补全 PG 等声明）。

## 3. ① config.yaml 挂载

### 3.1 现状

`deployer.Deploy(ctx, a, ins, env, dockerHost)` 构造 docker run：`-d --name --restart [--network host] -e <env> [-p host:internal] <image>`。**无 volume**。

### 3.2 设计

- `Deploy` 签名新增 `configPath string`（空则不挂）。
- `buildAndDeploy`（handler.go:1795 调 Deploy 处）在调用前检测 `<a.RepoDir>/config.yaml` 是否存在：
  - 存在 → `configPath = filepath.Join(a.RepoDir, "config.yaml")`，并注入 env `CONFIG_PATH=/app/config.yaml`（adapt 的 Dockerfile 据此定位；缺省也是它，显式注入更稳）。
  - 不存在 → `configPath=""`，不挂（兼容无 config 项目）。
- `Deploy` 当 `configPath != ""` 时追加 args：`-v <configPath>:/app/config.yaml:ro`。
- host 网络 / bridge 均挂（volume 与网络模式正交）。

### 3.3 边界与安全

- **路径安全**：`configPath` 必须是 `<a.RepoDir>/config.yaml`（平台托管仓库内），禁止任意路径——防 `-v` 逃逸挂载宿主敏感文件。校验 `filepath.Clean(configPath) == filepath.Join(a.RepoDir, "config.yaml")`。
- **只读**：`:ro`，容器不改 config。
- **密钥**：ro 挂载不进镜像层（镜像可分发、不含 config 密钥），契合 adapt 的 secret 语义。
- headless 应用同样适用（volume 与端口逻辑正交）。

## 4. ② bind_existing 中间件实例注册

### 4.1 现状

- 表 `appdeploy_service_instance`（`ServiceInstance`：kind/supply_mode/host/port/auth_ref/status/project_space_id/env_key...）。
- `LookupBindExisting(psID, kind)`：`supply_mode='bind_existing' AND status='active' AND (project_space_id=$psID OR NULL)`，项目级优先。
- `CreateInstance` 现仅 dedicated 供给链路用（supply.go:308）。
- **bind_existing 实例无运行时注册 API**——只能迁移种子插入，生产 `anp` 库缺种子 → 找不到。

### 4.2 设计

新增**注册 bind_existing 实例**的 API + store 方法 + UI：

- **store**：新增 `RegisterBindExisting(ctx, inst *ServiceInstance)`，向 `appdeploy_service_instance` 插入 `supply_mode='bind_existing', status='active'` 行（`ON CONFLICT (id) DO NOTHING` 幂等；按 `kind + project_space_id + host + port` 去重）。`CreateInstance` 复用或独立。
- **API**（mwsupply handler 注册到 v1）：
  - `POST /project-spaces/:id/mw-instances`：注册实例，body `{kind, host, port, auth_ref?, scope?"project"}`（scope=project 用 :id，省略=平台级 NULL）。
  - `GET /project-spaces/:id/mw-instances`：列表（脱敏 auth_ref）。
  - `DELETE /project-spaces/:id/mw-instances/:iid`：删除（status=archived 或物理删）。
- **Reconcile 无需改**：`LookupBindExisting` 命中注册行 → `ConnStr(inst)` → `UpsertEnv(REDIS_ADDR/MILVUS_ADDR..., source=platform)`，走现有 docker run `-e` 链路。**这是现有逻辑，注册后自动生效**。
- **UI**：依赖声明页（/governance 或应用详情 deps 区）加「注册已有实例」入口（kind 下拉 + host/port/auth 表单）。

### 4.3 边界

- **鉴权**：注册/删除需 admin（平台级）或项目 admin（项目级）；普通 dev 只读。
- **脱敏**：`auth_ref`（密码/token）存密文或掩码返回（对齐 appdeploy_env 的 is_secret）。
- **去重**：同 kind+scope 多实例——当前 LookupBindExisting 取一条（项目级优先）；多实例选择留后续（spec §6）。
- **PG 注意**：PG 非 mwsupply 当前覆盖的 kind（redis/milvus）；本设计 kind 开放，PG 可作为 kind=postgres 注册（env_key=PG_HOST/PG_PORT，connstr 拼 host:port）。但 PG 的 env 注入要适配（PG 拆 host/port/user/password/dbname 多个 env，非单 _ADDR）——见 §5。

## 5. PG 依赖的衔接（②的延伸）

客服机器人依赖 PG（`yxt_wecom_bot`）。adapt `env.go` 读 `PG_HOST/PG_PORT/PG_USER/PG_PASSWORD/PG_DBNAME`（多 env，非单 ADDR）。

- mwsupply 现有 `EnvKeyFor(kind)` 对 redis/milvus 产单 `<KIND>_ADDR`。
- PG 需多 env（host/port/user/password/dbname）。
- **方案**：`ServiceInstance` 对 PG 存结构化字段（host/port/auth_ref/user/dbname），`ConnStr`/env 注入按 kind 分支——PG 注入 `PG_HOST/PG_PORT/PG_USER/PG_PASSWORD/PG_DBNAME` 一组。
- 或最小化：本 spec ② 先支持 redis/milvus（单 ADDR），PG 留 §6 后续；PG 这次靠手动 UpsertEnv（已验证可行）。

> 推荐：② 第一版只做 redis/milvus 注册（最小可用，覆盖 .28 的 yxt-redis/yxt-milvus）；PG 多 env 注入作为 §6 增强先把 PG 接进 mwsupply kind 体系。

## 6. 不做（留后续）

- ③ 容器网络对接（应用加入依赖 docker 网络 / host 网络 / 宿主 IP 自动选择）——本次靠宿主 IP+映射端口手动解。
- ④ adapt deps 完整性（让 opencode adapt 补全 PG 等声明）。
- bind_existing 多实例选择策略。
- PG 完整接入 mwsupply（多 env 注入）——除非第一版含 PG。

## 7. 测试

- **①**：`deployer_test.go` 加 `TestDeploy_mountsConfigYaml`（configPath 非空 → args 含 `-v <path>:/app/config.yaml:ro`）+ `TestDeploy_noConfigNoMount`（空 → 无 -v）；`handler_test.go` 加 buildAndDeploy 检测 config.yaml 存在性。
- **②**：`store_test.go` 加 `TestStore_RegisterBindExisting`（注册后 LookupBindExisting 命中）+ 幂等；`supply_test.go` 加注册实例后 Reconcile 注入 env；handler HTTP 测注册/列表/删除 + 鉴权。
- 端到端：导入含 config.yaml+deps 的小项目，部署后容器内 `/app/config.yaml` 存在 + REDIS_ADDR 已注入（无需手动）。

## 8. 验收

1. 导入 yxt_eino_v2（或新测试项目）→ 部署后容器有 config.yaml（①）+ 注册 yxt-redis 后 REDIS_ADDR 自动注入（②）→ 应用启动不崩。
2. 单测全绿 + 全量回归。
3. 部署 .28 验证。

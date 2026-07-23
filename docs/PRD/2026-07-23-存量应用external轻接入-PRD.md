# 存量应用接入（B 类 ① 轻接入）PRD/设计

日期：2026-07-23
范围：把**已在运行的外部应用**纳入 ANP 管理：注册 + 统一入口 + 运维探活，**代码完全不动**（不 AI 编码/不部署/不迁库）

## 1. 背景

总方案 `docs/企业AI原生研发平台方案.md` 16.2 规划三类项目接入形态：

| 形态                | 例子             | 接入策略                                                                            |
| ------------------- | ---------------- | ----------------------------------------------------------------------------------- |
| A. AI 新研发        | 全新应用         | 平台 AI 编码 + 部署（**已实现**：appdeploy Create → EnsureRepo → AI 编码 → Deploy） |
| **B. 企业存量系统** | 已跑的老 ERP/CRM | **轻接入：只接管需求 + 运维；存量代码渐进式纳入**                                   |
| C. 外部 SaaS        | 钉钉/飞书        | API 集成                                                                            |

ANP 当前只支持 A 类。本任务做 **B 的 ① 轻接入**（最小子集，代码不动）：把已在运行的外部应用注册到平台，复用 appgw 给统一入口 `/apps/<app_id>/`，ops 按 external_url 探活。

参考已有：`pg_instance.deploy_mode = managed/external`（纳管远程 PG 思路）—— appdeploy 应用同样加 external 模式。

## 2. 范围与非目标

**做**：

- `appdeploy_application` 加 `deploy_mode`（managed/external）+ `external_url`
- appdeploy Create 支持 external 模式分支：校验 URL + 落库 + 写 appgw 路由（**不 EnsureRepo / 不 AI 编码 / 不部署 / 不建库**）
- appgw 反代支持 external：route 加 `external_url` 列；gateway 检测到 external 时直接反代 external_url（含路径）
- appdeploy Stats 探活支持 external：无容器时按 external_url HTTP GET
- 前端：应用列表加「接入外部应用」入口 + external 徽章

**不做**（B ②/③ 留后续）：

- 不动外部应用代码/库/部署（**纯纳管**）
- 不迁库、不接管需求（B ②）、不渐进纳入代码（B ③）

## 3. 模块结构

### 3.1 迁移 000010：加 external 字段

`platform/backend/internal/db/migrations/pg/000010_app_external.{up,down}.sql`

```sql
-- up
ALTER TABLE appdeploy_application
    ADD COLUMN IF NOT EXISTS deploy_mode   TEXT NOT NULL DEFAULT 'managed',
    ADD COLUMN IF NOT EXISTS external_url  TEXT NOT NULL DEFAULT '';
ALTER TABLE appdeploy_route
    ADD COLUMN IF NOT EXISTS external_url  TEXT NOT NULL DEFAULT '';
-- deploy_mode: managed(A类,平台托管) / external(B类,纳管外部)
-- appdeploy_application.external_url: external 模式时外部应用访问地址
-- appdeploy_route.external_url: external 应用路由时直接反代此 URL（managed 为空,走 host:port）

-- down: 反向 DROP（先 route 后 application，无依赖关系但保持对称）
ALTER TABLE appdeploy_route DROP COLUMN IF EXISTS external_url;
ALTER TABLE appdeploy_application DROP COLUMN IF EXISTS external_url;
ALTER TABLE appdeploy_application DROP COLUMN IF EXISTS deploy_mode;
```

NOT NULL DEFAULT 让现存行自动填 managed / ''，老 INSERT 不受影响。

### 3.2 appdeploy model + store

`internal/appdeploy/model.go`：

```go
type Application struct {
    // ... 现有字段 ...
    DeployMode  string `json:"deploy_mode" db:"deploy_mode"`   // managed / external
    ExternalURL string `json:"external_url" db:"external_url"` // external 模式的外部地址
}

const (
    AppManaged  = "managed"  // A 类：平台托管（建 git + AI 编码 + 部署）
    AppExternal = "external" // B 类：纳管外部应用（不建仓/不部署/不建库）
)
```

`internal/appdeploy/store.go`：

- `appCols()` 加 `deploy_mode, COALESCE(external_url,'') AS external_url`
- `Create()` 扩展 INSERT：加 `deploy_mode` + `external_url` 两列（managed 时取默认/空串）

### 3.3 appgw Route + Store

`internal/appgw/model.go`：

```go
type Route struct {
    // ... 现有字段 ...
    ExternalURL string `json:"external_url" db:"external_url"` // 非空=external 应用,直接反代此 URL
}
```

`internal/appgw/store.go`：

- `routeCols` 加 `external_url`
- `UpsertRoute` 现有签名不变（managed 用，external_url 走默认 ''）
- **新增** `UpsertExternalRoute(ctx, appID, psID, env, externalURL)`：写 external_url，并解析 URL 填 upstream_host/port（仅用于展示，反代用 external_url）
- `RouteWriter` 接口加 `UpsertExternalRoute` 方法

### 3.4 appgw Gateway 反代 external

`internal/appgw/gateway.go`：

```go
var target *url.URL
if route.ExternalURL != "" {
    target, err = url.Parse(route.ExternalURL)
    if err != nil { /* 502 invalid upstream */ }
} else {
    target = &url.URL{Scheme: "http", Host: fmt.Sprintf("%s:%d", route.UpstreamHost, route.UpstreamPort)}
}
```

`NewSingleHostReverseProxy(target)` 自带路径合并：target.Path 非空时拼到请求前；这里把 rest 拼到 external_url 的路径之后（保留 external_url 自带路径如 `/api/v1`）。Director 仍负责前缀剥离 + 身份头注入。

### 3.5 appdeploy handler Create external 分支

`internal/appdeploy/handler.go`：

```go
type createBody struct {
    Name         string `json:"name" binding:"required"`
    RepoDir      string `json:"repo_dir"`      // managed 可选
    InternalPort int    `json:"internal_port"` // managed 可选
    DeployMode   string `json:"deploy_mode"`   // managed(默认) / external
    ExternalURL  string `json:"external_url"`  // external 必填
}

func (h *Handler) Create(c *gin.Context) {
    // ... validate name + quota ...
    if in.DeployMode == AppExternal {
        // 校验 external_url（必填 + url.Parse 合法 + scheme http/https）
        u, err := url.Parse(in.ExternalURL)
        if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
            httpx.Err(c, 400, 40001, "external_url 非法（需 http(s)://host[:port][/path]）")
            return
        }
        a := &Application{
            ProjectSpaceID: psID, Name: in.Name,
            DeployMode: AppExternal, ExternalURL: in.ExternalURL,
            Status: "running", // external 应用注册即"运行中"（外部已活）
        }
        if err := h.store.Create(...); err != nil { ... }
        if h.routeWriter != nil {
            if err := h.routeWriter.UpsertExternalRoute(ctx, a.ID, psID, EnvProd, in.ExternalURL); err != nil {
                _ = h.store.SetStatus(..., "路由写入失败: "+err.Error(), "")
            }
        }
        httpx.Created(c, a)
        return
    }
    // ... 现有 managed 流程不变 ...
}
```

### 3.6 appdeploy Stats 探活 external

`Stats` handler 加 external 分支（在 GetInstance 之前）：

```go
if a.DeployMode == AppExternal {
    httpx.OK(c, gin.H{
        "env": env, "deployed": true, "external": true,
        "url": a.ExternalURL,
        "health": probeHealth(a.ExternalURL),
    })
    return
}
```

不查实例、不调 docker（external 无容器）。

### 3.7 前端 applications/page.tsx

- App 类型加 `deploy_mode` + `external_url`
- 注册表单加「接入模式」select（managed/external）；external 时只填 name + external_url
- 应用列表：external 应用显示「external」徽章 + external_url，不显示 部署/上线/编码 按钮（这些是 A 类才有的），显示「访问」按钮直达 external_url
- Stats 探活 external：`/apps/<id>/stats` 已返回 health，前端同样渲染

## 4. 测试（PG，testutil，禁 sqlite，`go test -p 1 ./...`）

| 模块                        | 测试                              | 覆盖                                           |
| --------------------------- | --------------------------------- | ---------------------------------------------- |
| appdeploy/store_test        | TestStore_CreateExternal          | Create 写入 deploy_mode/external_url，读回正确 |
| appdeploy/handler_http_test | TestHandler_CreateExternal        | HTTP POST 创建 external app + route 写入       |
| appdeploy/handler_http_test | TestHandler_CreateExternal_BadURL | external_url 非法 → 400                        |
| appdeploy/handler_http_test | TestHandler_Stats_External        | external app Stats 返回 deployed=true+health   |
| appgw/store_test            | TestStore_UpsertExternalRoute     | external route 写入 + 读回 external_url        |
| appgw/gateway_test          | TestGateway_ExternalURL           | 反代到 external_url（httptest fake upstream）  |

## 5. 部署 .28 + 验证

```bash
ssh -i ~/.ssh/miscode root@10.10.0.28  # keyless
scp platform/backend/...  root@10.10.0.28:/opt/anp/...
scp platform/frontend/... root@10.10.0.28:/opt/anp/...
cd /opt/anp && docker-compose build backend frontend && docker-compose up -d
```

验证 4 项：

1. `POST /project-spaces/ps_default/apps {deploy_mode:external, name:"存量ERP", external_url:"http://10.10.0.28:8088"}` → 应用创建 + appdeploy_route 写入 external_url
2. `GET /apps/<app_id>/`（带 Bearer token）→ 反代到 8088 响应
3. `GET /project-spaces/ps_default/apps/<id>/stats` → health=up
4. 前端 /applications → 接入外部应用 + external 徽章

## 6. commit 分块（每 logical 块 commit）

1. 迁移 000010 + appdeploy model/store 字段
2. appgw Route model/store UpsertExternalRoute
3. appgw Gateway 反代 external_url
4. appdeploy handler Create external 分支 + Stats 探活
5. 前端 applications external 模式
6. 测试 + 文档

## 7. 顾虑

- **external_url 域名 vs host:port**：appgw 原 route 是 host:port 反代，external 时直接用完整 URL（含 scheme + 可能的 path）。target 用 url.Parse(external_url)；Director 拼路径时保留 external_url.Path 作为前缀。
- **path 合并**：`/apps/<app_id>/api/q` → external_url=`http://h/prefix` 时应反代到 `http://h/prefix/api/q`。`NewSingleHostReverseProxy` 的默认 Director 把 target.Path 拼到前面，再追加请求 path。我们覆写 Director 时需保留这个拼接行为。
- **鉴权头**：external 应用大概率不认 X-User/X-Project-Space-Id；仍注入（应用可忽略）。route.auth_required 仍可强制平台登录（防外部直接访问）。
- **统计接入**：B ② 的「接管需求」未做，external 应用暂时不与需求关联（仅注册 + 入口 + 探活）。

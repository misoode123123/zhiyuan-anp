# 部署消费 adapt 产物(config 挂载 + mw 实例注册)实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 兑现 opencode adapt 的两个部署假设——部署时挂载仓库 config.yaml + 运行时注册 bind_existing 中间件实例自动注入连接 env,让导入已含依赖的项目端到端跑通。

**Architecture:** ① 给 `deployer.Deploy` 加 volume 支持(configPath 参数 + `-v` 挂载);`buildAndDeploy` 检测仓库 config.yaml。② 仿 deps API 模式:appdeploy handler 加 `/mw-instances` 路由 → `mwReconciler` 新方法 → `mwsupply.Store` 注册/查/删 bind_existing 实例行,`Reconcile` 现有 `LookupBindExisting` 命中即自动注入 env。

**Tech Stack:** Go(Gin + sqlx)后端 / Next.js 前端 / PostgreSQL / TDD。

## Global Constraints

- 中间件实例表 `appdeploy_service_instance`,`supply_mode='bind_existing'`,`status='active'`,`project_space_id` NULL=平台级。
- `LookupBindExisting(psID, kind)` 项目级优先(project_space_id IS NOT NULL DESC)——已存在,不改。
- env 注入经 `appdeploy_env` 表(source='platform'),`EnvPairs` 在 `docker run -e` 消费——已存在。
- 第一版 ② 只 redis/milvus(单 `<KIND>_ADDR`);PG 多 env 注入留后续(spec §6)。
- 鉴权:注册/删除 mw-instances 需 admin;`auth.Allowed("app.net.host", roles)` 风格。
- 测试:`go test ./internal/appdeploy/ ./internal/mwsupply/`,连 .28 anp_test 库(testutil.TestDB)。
- commit:conventional commits 中文,body 每行 ≤100 字符(husky commitlint)。

---

### Task 1: deployer.Deploy 加 configPath volume 支持

**Files:**

- Modify: `platform/backend/internal/appdeploy/deployer.go:150`(Deploy 签名 + args)
- Modify: `platform/backend/internal/appdeploy/deployer_test.go:242,277,310`(3 处调用补 configPath 参数)
- Test: `platform/backend/internal/appdeploy/deployer_test.go`(新增 TestDeploy_mountsConfigYaml)

**Interfaces:**

- Produces: `Deploy(ctx, a, ins, env, dockerHost, configPath string) error`——configPath 空则不挂,非空则 `-v <configPath>:/app/config.yaml:ro`。

- [ ] **Step 1: 写失败测试(挂载 configPath)**

在 `deployer_test.go` 末尾加:

```go
func TestDeploy_mountsConfigYaml(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfg, []byte("k: v"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &Deployer{host: "h"}
	a := &Application{ID: "app_1", Name: "demo", RepoDir: dir, AppKind: AppKindWeb, InternalPort: 8080}
	ins := &AppInstance{Env: "test", HostPort: 9100}
	_ = d.Deploy(context.Background(), a, ins, nil, "", cfg)
	calls := deployCalls(t)
	last := calls[len(calls)-1]
	joined := strings.Join(last, " ")
	if !strings.Contains(joined, "-v "+cfg+":/app/config.yaml:ro") {
		t.Fatalf("应挂载 config.yaml,实际 args: %s", joined)
	}
}

func TestDeploy_noConfigNoMount(t *testing.T) {
	d := &Deployer{host: "h"}
	a := &Application{ID: "app_1", Name: "demo", AppKind: AppKindWeb, InternalPort: 8080}
	ins := &AppInstance{Env: "test", HostPort: 9100}
	_ = d.Deploy(context.Background(), a, ins, nil, "", "")
	calls := deployCalls(t)
	last := calls[len(calls)-1]
	for _, arg := range last {
		if arg == "-v" || strings.HasPrefix(arg, "-v=") {
			t.Fatalf("空 configPath 不应挂载,但 args 含 -v: %v", last)
		}
	}
}
```

> 注:`deployCalls(t)` 取测试注入的 docker 调用——deployer_test.go 现有 `var dockerRun = ...` 可注入 fake(参考 `host 网络门禁` 测试 `TestDeploy_Host_NoPortMap_NetworkHost` 的 fake 用法)。如无现成 helper,在本 task 顶部声明 `var capturedArgs [][]string` + 注入 `dockerRun = captureRun`。

- [ ] **Step 2: 跑测试看失败**

Run: `cd platform/backend && go test ./internal/appdeploy/ -run TestDeploy_mountsConfigYaml -v`
Expected: FAIL(签名不匹配 / 无 -v)。

- [ ] **Step 3: 改 Deploy 签名 + 加 -v**

`deployer.go:150` 改:

```go
func (d *Deployer) Deploy(ctx context.Context, a *Application, ins *AppInstance, env []string, dockerHost, configPath string) error {
	name := fmt.Sprintf("appdeploy-%s-%s-v%d", dockerSlug(a.Name), ins.Env, ins.Version)
	args := []string{"run", "-d", "--name", name, "--restart", "unless-stopped"}
	isHost := a.NetworkMode == "host"
	if isHost {
		args = append(args, "--network", "host")
	}
	if configPath != "" {
		// 路径安全:仅挂平台托管仓库内的 config.yaml(调用方保证 configPath=<RepoDir>/config.yaml)
		args = append(args, "-v", configPath+":/app/config.yaml:ro")
	}
	// ...其余(headless 分支 / web -p / image)不变
```

> 注:headless 分支(158)与 web 分支(172)都共用顶部 args,`-v` 加在顶部即两分支都挂。

- [ ] **Step 4: 改 deployer_test.go 3 处调用补空 configPath**

`deployer_test.go:242/277/310` 的 `d.Deploy(ctx, a, ins, env, "")` → `d.Deploy(ctx, a, ins, env, "", "")`。

- [ ] **Step 5: 跑测试看通过**

Run: `cd platform/backend && go test ./internal/appdeploy/ -run 'TestDeploy_(mountsConfigYaml|noConfigNoMount|Host_NoPortMap)' -v`
Expected: PASS。

- [ ] **Step 6: commit**

```bash
git add platform/backend/internal/appdeploy/deployer.go platform/backend/internal/appdeploy/deployer_test.go
git commit -m "feat(appdeploy): Deploy 加 configPath volume 挂载 config.yaml(兑现 adapt secret 挂载)"
```

---

### Task 2: buildAndDeploy 检测 config.yaml + 注入 CONFIG_PATH

**Files:**

- Modify: `platform/backend/internal/appdeploy/handler.go:1795`(buildAndDeploy 调 Deploy 处)
- Test: `platform/backend/internal/appdeploy/handler_http_test.go`(新增 TestHandler_Deploy_mountsConfig)

**Interfaces:**

- Consumes: Task 1 的 `Deploy(..., configPath)`。
- Produces: 部署时 `<RepoDir>/config.yaml` 存在 → 传 configPath + 注入 env `CONFIG_PATH=/app/config.yaml`。

- [ ] **Step 1: 写失败测试**

`handler_http_test.go` 加(参考现有 buildAndDeploy 相关测试的 handler 搭建):

```go
func TestHandler_Deploy_mountsConfig(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "config.yaml"), []byte("k: v"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := seedApp(t, h, "ps_1", "demo", repoDir)
	// 触发 test 部署(注入 fake deployer 记录 configPath)——h.deployer 用 fake,capturedConfigPath != ""
	_ = doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/deploy", nil)
	// 断言:传给 Deploy 的 configPath == <repoDir>/config.yaml
	if got := capturedConfigPath(t); got != filepath.Join(repoDir, "config.yaml") {
		t.Fatalf("configPath 应为 <repoDir>/config.yaml,得 %s", got)
	}
}
```

> 注:`capturedConfigPath` 经 fake deployer 记录(newHTTPHandler 注入的 fake Deployer 需暴露 configPath 字段;若现 fake 无,本 task 在 fake 上加 `LastConfigPath string`)。

- [ ] **Step 2: 跑测试看失败**

Run: `cd platform/backend && go test ./internal/appdeploy/ -run TestHandler_Deploy_mountsConfig -v`
Expected: FAIL(configPath 空)。

- [ ] **Step 3: 改 handler.go:1795 检测 config + 传 configPath**

`handler.go:1795` 上下文(`envPairs, _ := h.store.EnvPairs(...)` 之后,`deployer.Deploy` 之前)加:

```go
	// config.yaml 挂载(spec ①):仓库根有 config.yaml 则挂到 /app/config.yaml(ro),兑现 adapt secret 挂载假设。
	configPath := ""
	if fi, e := os.Stat(filepath.Join(a.RepoDir, "config.yaml")); e == nil && !fi.IsDir() {
		configPath = filepath.Join(a.RepoDir, "config.yaml")
		envPairs = append(envPairs, "CONFIG_PATH=/app/config.yaml")
	}
	dErr := h.deployer.Deploy(deployCtx, a, ins, envPairs, dockerHost, configPath)
```

- [ ] **Step 4: 跑测试看通过**

Run: `cd platform/backend && go test ./internal/appdeploy/ -run TestHandler_Deploy_mountsConfig -v`
Expected: PASS。

- [ ] **Step 5: 全量回归**

Run: `cd platform/backend && go test ./internal/appdeploy/ ./internal/mwsupply/`
Expected: PASS。

- [ ] **Step 6: commit**

```bash
git add platform/backend/internal/appdeploy/handler.go platform/backend/internal/appdeploy/handler_http_test.go
git commit -m "feat(appdeploy): buildAndDeploy 检测 config.yaml 挂载+注入 CONFIG_PATH"
```

---

### Task 3: mwsupply Store 注册/查/删 bind_existing 实例

**Files:**

- Modify: `platform/backend/internal/mwsupply/store.go`(加 RegisterBindExisting / ListBindExisting / DeleteInstance 已有)
- Test: `platform/backend/internal/mwsupply/store_test.go`

**Interfaces:**

- Produces:
  - `Store.RegisterBindExisting(ctx, inst *ServiceInstance) error`——幂等插 bind_existing 行。
  - `Store.ListBindExisting(ctx, psID string) ([]ServiceInstance, error)`——列注册实例(平台级+项目级)。
  - (`DeleteInstance` 已存在 store.go:147)

- [ ] **Step 1: 写失败测试**

`store_test.go` 加(参考 `TestStore_LookupBindExisting_seed` 的 store 构建):

```go
func TestStore_RegisterBindExisting(t *testing.T) {
	s := NewStore(testutil.TestDB(t))
	pid := "ps_reg"
	inst := &ServiceInstance{
		ID: "svinst-redis-test", Kind: "redis", Name: "my-redis",
		SupplyMode: ModeBindExisting, Host: "10.10.0.28", Port: 6381, Status: "active",
		ProjectSpaceID: &pid,
	}
	if err := s.RegisterBindExisting(context.Background(), inst); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, err := s.LookupBindExisting(context.Background(), pid, "redis")
	if err != nil || got == nil {
		t.Fatalf("注册后应能 Lookup 到: %v %v", got, err)
	}
	if got.Host != "10.10.0.28" || got.Port != 6381 {
		t.Fatalf("实例数据不符: %+v", got)
	}
	// 幂等:同 kind+scope+host+port 再注不报错不重复
	if err := s.RegisterBindExisting(context.Background(), inst); err != nil {
		t.Fatalf("幂等注册应不报错: %v", err)
	}
}

func TestStore_ListBindExisting(t *testing.T) {
	s := NewStore(testutil.TestDB(t))
	pid := "ps_list2"
	_ = s.RegisterBindExisting(context.Background(), &ServiceInstance{ID: "svinst-mil-list", Kind: "milvus", SupplyMode: ModeBindExisting, Host: "h", Port: 19530, Status: "active", ProjectSpaceID: &pid})
	list, err := s.ListBindExisting(context.Background(), pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) == 0 {
		t.Fatal("应至少列出刚注册的 milvus")
	}
}
```

- [ ] **Step 2: 跑测试看失败**

Run: `cd platform/backend && go test ./internal/mwsupply/ -run 'TestStore_RegisterBindExisting|TestStore_ListBindExisting' -v`
Expected: FAIL(方法未定义)。

- [ ] **Step 3: 实现 RegisterBindExisting + ListBindExisting**

`store.go` 在 `CreateInstance` 后加:

```go
// RegisterBindExisting 注册一个 bind_existing 实例(运维把部署机已有服务登记给 ANP)。
// 幂等:同 kind + project_space_id + host + port 已存在则不重复插(先查后定)。
func (s *Store) RegisterBindExisting(ctx context.Context, inst *ServiceInstance) error {
	// 同 kind+scope+host+port 已存在 → 跳过
	var exist string
	ps := inst.ProjectSpaceID
	_ = s.db.GetContext(ctx, &exist,
		`SELECT id FROM appdeploy_service_instance
		 WHERE kind=$1 AND supply_mode='bind_existing' AND host=$2 AND port=$3
		   AND (project_space_id IS NOT DISTINCT FROM $4)`,
		inst.Kind, inst.Host, inst.Port, ps)
	if exist != "" {
		return nil
	}
	if inst.ID == "" {
		inst.ID = "svinst-" + uuid.NewString()[:12]
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO appdeploy_service_instance
		   (id, project_space_id, kind, name, supply_mode, host, port, auth_ref, status)
		 VALUES ($1,$2,$3,$4,'bind_existing',$5,$6,NULLIF($7,''),'active')
		 ON CONFLICT (id) DO NOTHING`,
		inst.ID, ps, inst.Kind, inst.Name, inst.Host, inst.Port, inst.AuthRef)
	return err
}

// ListBindExisting 列出某项目空间可见的 bind_existing 实例(项目级 + 平台级 NULL)。
func (s *Store) ListBindExisting(ctx context.Context, psID string) ([]ServiceInstance, error) {
	var out []ServiceInstance
	err := s.db.SelectContext(ctx, &out,
		`SELECT `+instCols+` FROM appdeploy_service_instance
		 WHERE supply_mode='bind_existing' AND status='active'
		   AND (project_space_id=$1 OR project_space_id IS NULL)
		 ORDER BY (project_space_id IS NOT NULL) DESC, kind`, psID)
	return out, err
}
```

> import 补 `"github.com/google/uuid"`(若 store.go 未引)。

- [ ] **Step 4: 跑测试看通过**

Run: `cd platform/backend && go test ./internal/mwsupply/ -run 'TestStore_RegisterBindExisting|TestStore_ListBindExisting' -v`
Expected: PASS。

- [ ] **Step 5: commit**

```bash
git add platform/backend/internal/mwsupply/store.go platform/backend/internal/mwsupply/store_test.go
git commit -m "feat(mwsupply): Store 加 RegisterBindExisting/ListBindExisting 注册已有中间件实例"
```

---

### Task 4: Reconciler 暴露注册/列表 + appdeploy handler mw-instances API

**Files:**

- Modify: `platform/backend/internal/mwsupply/supply.go`(Reconciler 委托 store)
- Modify: `platform/backend/internal/appdeploy/handler.go`(mw-instances 路由 + handler)
- Test: `platform/backend/internal/appdeploy/handler_http_test.go`

**Interfaces:**

- Consumes: Task 3 的 `Store.RegisterBindExisting/ListBindExisting/DeleteInstance`。
- Produces:
  - `Reconciler.RegisterBindExisting(ctx, inst) error` / `.ListBindExisting(ctx, psID) ([]ServiceInstance, error)` / `.DeleteInstance(ctx, id) error`
  - API: `POST/GET/DELETE /project-spaces/:id/mw-instances[/:iid]`。

- [ ] **Step 1: 写失败测试(HTTP)**

`handler_http_test.go` 加(用 newHTTPHandlerWithGates 或带 mwReconciler 的 handler):

```go
func TestHandler_RegisterMwInstance(t *testing.T) {
	h, db := newHTTPHandler(t)
	// 确保 mwReconciler 已注入(newHTTPHandler 默认注入,若否则用 newHTTPHandlerWithGates)
	body := strings.NewReader(`{"kind":"redis","host":"10.10.0.28","port":6381,"scope":"project"}`)
	code, resp := doReq(t, newRouterWith(h), http.MethodPost, "/api/v1/project-spaces/ps_1/mw-instances", body)
	if code != 200 {
		t.Fatalf("注册应 200,得 %d %s", code, resp)
	}
	// GET 列表能查到
	gcode, gresp := doReq(t, newRouterWith(h), http.MethodGet, "/api/v1/project-spaces/ps_1/mw-instances", nil)
	if gcode != 200 || !strings.Contains(gresp, "redis") {
		t.Fatalf("列表应含 redis,得 %d %s", gcode, gresp)
	}
	_ = db
}
```

- [ ] **Step 2: 跑测试看失败**

Run: `cd platform/backend && go test ./internal/appdeploy/ -run TestHandler_RegisterMwInstance -v`
Expected: FAIL(404 路由不存在)。

- [ ] **Step 3: Reconciler 委托方法**

`supply.go` 在 `ListDeps` 附近加:

```go
func (r *Reconciler) RegisterBindExisting(ctx context.Context, inst *appdeploy.MWInstance) error {
	return r.store.RegisterBindExisting(ctx, inst.ToServiceInstance())
}
func (r *Reconciler) ListBindExisting(ctx context.Context, psID string) ([]appdeploy.MWInstance, error) {
	list, err := r.store.ListBindExisting(ctx, psID)
	if err != nil {
		return nil, err
	}
	out := make([]appdeploy.MWInstance, len(list))
	for i, s := range list {
		out[i] = appdeploy.FromServiceInstance(s)
	}
	return out, nil
}
func (r *Reconciler) DeleteInstance(ctx context.Context, id string) error {
	return r.store.DeleteInstance(ctx, id)
}
```

> 注:`appdeploy.MWInstance` 是 appdeploy 层 DTO(脱敏 auth_ref),在 appdeploy/model.go 定义(见 Step 4)。`r.store` 需暴露——Reconciler 已有 store 字段(supply.go:35 NewReconciler 注入)。

- [ ] **Step 4: appdeploy 加 MWInstance DTO + handler + 路由**

`appdeploy/model.go` 加:

```go
// MWInstance mw-instances API 的 DTO(auth_ref 脱敏返回)。
type MWInstance struct {
	ID      string `json:"id,omitempty"`
	Kind    string `json:"kind"`
	Name    string `json:"name,omitempty"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	AuthRef string `json:"auth_ref,omitempty"` // 注册时填;列表返回掩码(同 is_secret 风格)
	Scope   string `json:"scope,omitempty"`   // "project"=项目级,空=平台级
}
```

在 `mwsupply` 包加转换 helper(避免 appdeploy 反向依赖 mwsupply ServiceInstance 循环):

```go
// appdeploy/model.go
func (m MWInstance) ToServiceInstance() *mwsupply.ServiceInstance {
	var ps *string
	if m.Scope == "project" { /* 调用方用 psID 填,见 handler */ }
	_ = ps
	return &mwsupply.ServiceInstance{Kind: m.Kind, Name: m.Name, Host: m.Host, Port: m.Port, AuthRef: m.AuthRef, SupplyMode: mwsupply.ModeBindExisting, Status: "active"}
}
```

> 循环依赖处理:`ToServiceInstance` 的 project_space_id 由 handler 用 psID 设(handler 知道 :id)。或把转换放 mwsupply 包(mwsupply 依赖 appdeploy 已存在——supply.go 已 import appdeploy)。**采用后者**:在 mwsupply 加 `func MWToServiceInstance(m appdeploy.MWInstance, psID string, scope string) *ServiceInstance`,handler 调它。

`appdeploy/handler.go` 加路由(在 deps 路由 146-149 附近):

```go
	r.POST("/project-spaces/:id/mw-instances", h.RegisterMwInstance)
	r.GET("/project-spaces/:id/mw-instances", h.ListMwInstances)
	r.DELETE("/project-spaces/:id/mw-instances/:iid", h.DeleteMwInstance)
```

handler 实现:

```go
func (h *Handler) RegisterMwInstance(c *gin.Context) {
	if !auth.Allowed("mw.instance.admin", rolesFromCtx(c)) { // admin 鉴权(矩阵加 mw.instance.admin 给 admin)
		httpx.Err(c, 403, 40301, "无权限注册中间件实例(仅 admin)")
		return
	}
	var in MWInstance
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error()); return
	}
	if in.Kind == "" || in.Host == "" || in.Port == 0 {
		httpx.Err(c, 400, 40001, "kind/host/port 必填"); return
	}
	psID := c.Param("id")
	inst := mwsupply.MWToServiceInstance(in, psID, in.Scope)
	if err := h.mwReconciler.RegisterBindExisting(c.Request.Context(), inst); err != nil {
		httpx.Err(c, 500, 50020, err.Error()); return
	}
	httpx.OK(c, gin.H{"registered": true, "id": inst.ID})
}

func (h *Handler) ListMwInstances(c *gin.Context) {
	list, err := h.mwReconciler.ListBindExisting(c.Request.Context(), c.Param("id"))
	if err != nil { httpx.Err(c, 500, 50020, err.Error()); return }
	httpx.OK(c, gin.H{"data": list})
}

func (h *Handler) DeleteMwInstance(c *gin.Context) {
	if !auth.Allowed("mw.instance.admin", rolesFromCtx(c)) {
		httpx.Err(c, 403, 40301, "无权限删除中间件实例(仅 admin)"); return
	}
	if err := h.mwReconciler.DeleteInstance(c.Request.Context(), c.Param("iid")); err != nil {
		httpx.Err(c, 500, 50020, err.Error()); return
	}
	httpx.OK(c, gin.H{"deleted": c.Param("iid")})
}
```

> 注:`mw.instance.admin` 需在 auth 权限矩阵给 admin(OpRoles)。若不想加新 op,复用现有 admin-only 判断(参考 PutNetworkMode 的 app.net.host,admin 持有)。

- [ ] **Step 5: 跑测试看通过 + 回归**

Run: `cd platform/backend && go test ./internal/appdeploy/ ./internal/mwsupply/`
Expected: PASS。

- [ ] **Step 6: commit**

```bash
git add platform/backend/internal/mwsupply/supply.go platform/backend/internal/appdeploy/handler.go platform/backend/internal/appdeploy/model.go platform/backend/internal/appdeploy/handler_http_test.go
git commit -m "feat(appdeploy): mw-instances API 注册/列表/删除 bind_existing 实例"
```

---

### Task 5: 前端「注册已有实例」UI

**Files:**

- Modify: `platform/frontend/app/_components/deps-section.tsx`(或应用详情 deps 区组件,参考现有 deps UI)
- Modify: `platform/frontend/lib/api.ts`(加 mw-instances 调用)

**Interfaces:**

- Consumes: Task 4 的 `POST/GET/DELETE /project-spaces/:id/mw-instances`。

- [ ] **Step 1: 加 API 调用**

`lib/api.ts` 加:

```ts
export async function listMwInstances(psID: string): Promise<any[]> {
  const r = await fetch(`${API_BASE_URL}/project-spaces/${psID}/mw-instances`, {
    headers: authHeaders(),
  });
  return (await r.json()).data ?? [];
}
export async function registerMwInstance(
  psID: string,
  body: { kind: string; host: string; port: number; auth_ref?: string; scope?: string }
): Promise<any> {
  const r = await fetch(`${API_BASE_URL}/project-spaces/${psID}/mw-instances`, {
    method: "POST",
    headers: { "Content-Type": "application/json", ...authHeaders() },
    body: JSON.stringify(body),
  });
  return r.json();
}
export async function deleteMwInstance(psID: string, iid: string): Promise<any> {
  const r = await fetch(`${API_BASE_URL}/project-spaces/${psID}/mw-instances/${iid}`, {
    method: "DELETE",
    headers: authHeaders(),
  });
  return r.json();
}
```

- [ ] **Step 2: deps 区加「已注册实例」面板**

在 deps-section 组件加:列表(listMwInstances)+「+注册实例」表单(kind 下拉 redis/milvus + host/port/auth_ref + scope)+ 删除按钮。注册成功后 toast + 刷新列表。

- [ ] **Step 3: 手测**

`pnpm dev` → 应用详情 deps 区 → 注册 yxt-redis(10.10.0.28:6381)→ 列表出现 → 部署应用 → 容器 REDIS_ADDR 自动注入。

- [ ] **Step 4: commit**

```bash
git add platform/frontend/lib/api.ts platform/frontend/app/_components/deps-section.tsx
git commit -m "feat(frontend): 依赖区加「注册已有中间件实例」面板"
```

---

### Task 6: 端到端验证 + 部署 .28

**Files:** 无代码改动,验证 + 部署。

- [ ] **Step 1: 本地全量回归**

Run: `cd platform/backend && go build ./... && go test ./internal/appdeploy/ ./internal/mwsupply/`
Expected: 全 PASS。

- [ ] **Step 2: 端到端验证(本地或 .28)**

导入含 config.yaml + .anp/deps.yaml(redis) 的小项目 → 注册一个 redis 实例 → 部署 → 确认:① 容器内 `/app/config.yaml` 存在;② `docker exec <c> env | grep REDIS_ADDR` 自动注入(非手动);③ 应用不崩。

- [ ] **Step 3: 合并 + 部署 .28**

```bash
git checkout main && git merge --ff-only feat/deploy-consume-adapt
git push origin main
# 增量部署 backend + frontend 到 .28(tar+scp+docker-compose up --build)
```

- [ ] **Step 4: .28 验证**

重新部署 yxt-eino-v2(或新项目):注册 yxt-redis 后 REDIS_ADDR 自动注入 + config.yaml 挂载 → 应用启动不崩(对比本次手动补丁)。

- [ ] **Step 5: 写教训总结文档(B,Task #20)+ commit**

把 spec §1 的缺口 + 本次优化总结到 `docs/superpowers/specs/2026-08-04-部署消费adapt-教训与优化总结.md`。

---

## Self-Review(写后自查)

**Spec 覆盖**:① config 挂载 → Task 1-2 ✓;② mw 实例注册 → Task 3-4 ✓;UI → Task 5 ✓;验证 → Task 6 ✓;§5 PG 留后续(spec §6 明确)✓;§7 测试 → 各 task TDD ✓;§8 验收 → Task 6 ✓。

**占位**:无 TBD/TODO;deps-section 组件名若与实际不符,Task 5 Step 1 标注"或应用详情 deps 区组件"——执行时核实实际文件名。

**类型一致**:`Deploy(...,configPath)` 全链路一致(handler:1795 + deployer_test 3 处);`RegisterBindExisting` store→Reconciler→handler 一致;`MWInstance` DTO + `MWToServiceInstance` 转换避免循环依赖(mwsupply 转换)。

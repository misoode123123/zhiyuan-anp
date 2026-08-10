# 应用全景视图 P1-b（引擎消费 needs 全字段）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `.anp/deploy.yaml` 的 `needs` 四字段（ports/command/env_keys/mounts）真正驱动部署——当前引擎只消费 config mount 一个，opencode 维护的其余字段无人看。P1-a 已把 AGENTS.md 的「消费现状」诚实标注为「仅 mounts 待 P1-b」，本计划兑现之。

**Architecture:** 三层改动，向后兼容（`DeployOpts` 零值 = 旧行为）：① manifest 层加 `ResolvedMount` + `ResolveExtraMounts`（解析非 config 挂载，**不动** `ResolveConfigMount`/`RecordActuals` 及其 9 个测试）；② deployer 层加 `DeployOpts{ConfigPath,Mounts,Command,Port}` + `splitCommand`，`Deploy` 签名从 `configPath string` 改为 `opts DeployOpts`，body 接 extra-mounts / 命令覆盖 / needs 端口优先；③ handler 层 `buildAndDeploy` 把 needs 拼进 opts + `missingEnvKeys` 软校验（缺值 WARN 不阻断）。`NativeDeployer.Deploy` 是独立方法，不受容器 `Deploy` 签名变更影响。

**Tech Stack:** Go（module `zhiyuan-anp/platform/backend`），`testing`，`go.uber.org/zap`，PG（既有测试用，本计划不改表）。

## Scope Note

只覆盖 P1-b。P1-c（全景聚合+前端）、P2（opencode 备料）、P3（可观测）另起计划。P1 全程不改表。

## Global Constraints

- **不改表结构**（零迁移）。
- **conventional commits**：type(scope): subject，中文 body 可，**body 每行 ≤ 100 字符**，`Co-Authored-By: Claude <noreply@anthropic.com>` trailer。main 历史为**线性 ff**（本仓无 merge commit，见 `git log`），合 main 用 `git merge --ff-only`。
- **分支**：`feat/app-panorama-p1b`。
- **后端测试**：`cd platform/backend && go test -p 1 -count=1 ./...`（PG service 容器 + pgvector）。
- **不破坏既有**：`ResolveConfigMount`（6 测试）、`RecordActuals`（3 测试）、5 个 `Deploy` 调用测试——签名/行为保持兼容。
- **module 路径**：`zhiyuan-anp/platform/backend`。

---

## File Structure

| 文件                                         | 改动                                                                                     |
| -------------------------------------------- | ---------------------------------------------------------------------------------------- |
| `internal/appdeploy/deploy_manifest.go`      | 加 `ResolvedMount` 类型 + `ResolveExtraMounts`（不动既有函数）                           |
| `internal/appdeploy/deploy_manifest_test.go` | 加 `TestResolveExtraMounts_*` + `TestMissingEnvKeys_*`                                   |
| `internal/appdeploy/deployer.go`             | 加 `DeployOpts` + `ResolvedMount.dockerVolArg` + `splitCommand`；`Deploy` 签名+body 改造 |
| `internal/appdeploy/deployer_test.go`        | 更新 5 个 `Deploy` 调用点（configPath→opts）；加 4 组新测试                              |
| `internal/appdeploy/handler.go`              | `buildAndDeploy` 拼needs→opts；加 `missingEnvKeys`+`validateEnvKeys`                     |

不改：`deploy_manifest.go` 的 `ResolveConfigMount`/`RecordActuals`/`Load`/`Write`、`NativeDeployer`、前端、迁移。

---

## 事实基线（已核实）

1. **needs 消费现状**（`handler.go:1929-1944`）：`LoadDeployManifest` → `ResolveConfigMount`（仅 dst=/app/config.yaml）→ 注 `CONFIG_PATH` → `Deploy(..., configHostPath)` → `RecordActuals`。`needs.Ports/Command/EnvKeys` 零消费。
2. **Deploy 现签名**（`deployer.go:149`）：`Deploy(ctx, a, ins, env []string, dockerHost, configPath string)`。调用方仅 `handler.go:1943` + `deployer_test.go` 5 处（:245/280/313/355/372）。`NativeDeployer.Deploy`（deployer_native）是独立方法，不同签名，不受影响。
3. **Deploy 内部用 a.InternalPort 处**：headless 无端口；web/service 用 `a.InternalPort` 做 `ensurePortEnv`(:191) + `-p port:a.InternalPort`(:196)，host 模式 `port=a.InternalPort`(:179)。
4. **config 挂载特例**（`deployer.go:158-160`）：`-v configPath:/app/config.yaml:ro`；`CONFIG_PATH` env 在 `handler.go:1941` 注入（hasConfig 时）。
5. **无 shell 分词工具**（已 grep 全 backend）→ 需自加 `splitCommand`。
6. **handler.go 已 import** `"strings"`(:16) + zap（:1877 等多处用）。
7. **测试文件**：`deploy_manifest_test.go`（纯、DB-free）、`deployer_test.go`（用 `dockerRun` var 注入）、`handler_test.go`/`handler_http_test.go` 存在。
8. **向后兼容不变式**：`DeployOpts{}` 零值 → listen=a.InternalPort、无 command、无 extra-mount、ConfigPath 空 = 旧行为；`DeployOpts{ConfigPath: cfg}` = 旧 configPath 行为。故 5 个旧测试仅需改调用点，断言全不变。

---

### Task 1: ResolvedMount + ResolveExtraMounts（manifest 层）

**Files:**

- Modify: `platform/backend/internal/appdeploy/deploy_manifest.go`（末尾加类型+函数）
- Test: `platform/backend/internal/appdeploy/deploy_manifest_test.go`（加 `TestResolveExtraMounts_*`）

**Interfaces:**

- Consumes: `resolveMountHostSrc`、`mf.findActualMount`（既有）、`MountSpec`（既有）。
- Produces: `ResolvedMount` 类型 + `ResolveExtraMounts(repoDir, mf) []ResolvedMount`（Task 2 `DeployOpts.Mounts` 用）。

- [ ] **Step 1: 写失败测试**

`deploy_manifest_test.go` 末尾追加：

```go
// === ResolveExtraMounts（P1-b）===

// TestResolveExtraMounts_SkipsConfigResolvesOthers 跳过 config 挂载，解析其余挂载到宿主源。
func TestResolveExtraMounts_SkipsConfigResolvesOthers(t *testing.T) {
	repoDir := t.TempDir()
	os.MkdirAll(filepath.Join(repoDir, "secrets"), 0o755)
	os.WriteFile(filepath.Join(repoDir, "secrets", "tls.crt"), []byte("cert"), 0o644)
	mf := &DeployManifest{Needs: NeedsSpec{Mounts: []MountSpec{
		{Src: "config.yaml", Dst: "/app/config.yaml", ReadOnly: true},      // config 跳过
		{Src: "secrets/tls.crt", Dst: "/etc/tls/tls.crt", ReadOnly: true},  // 解析
	}}}
	got := ResolveExtraMounts(repoDir, mf)
	if len(got) != 1 {
		t.Fatalf("应只返回 1 条非 config 挂载，得 %d 条 %+v", len(got), got)
	}
	want := toHostRepoDir(filepath.Join(repoDir, "secrets", "tls.crt"))
	if got[0].HostSrc != want || got[0].Dst != "/etc/tls/tls.crt" || !got[0].ReadOnly {
		t.Fatalf("解析错误 got=%+v want HostSrc=%q", got[0], want)
	}
}

func TestResolveExtraMounts_NilManifest(t *testing.T) {
	if got := ResolveExtraMounts(t.TempDir(), nil); got != nil {
		t.Fatalf("nil manifest 应返回 nil，得 %+v", got)
	}
}

func TestResolveExtraMounts_AllConfig(t *testing.T) {
	mf := &DeployManifest{Needs: NeedsSpec{Mounts: []MountSpec{
		{Src: "config.yaml", Dst: "/app/config.yaml"},
	}}}
	if got := ResolveExtraMounts(t.TempDir(), mf); len(got) != 0 {
		t.Fatalf("全 config 挂载应返回空，得 %+v", got)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd platform/backend && go test -run TestResolveExtraMounts ./internal/appdeploy/`
Expected: 编译失败 `undefined: ResolveExtraMounts` / `ResolvedMount`。

- [ ] **Step 3: 实现**

`deploy_manifest.go` 末尾（`RecordActuals` 之后）追加：

```go
// ResolvedMount 一条已解析到宿主源的挂载（Deployer.Deploy 据此拼 docker -v）。
type ResolvedMount struct {
	HostSrc  string // 宿主绝对路径（resolveMountHostSrc 解析：actual 记录优先→toHostRepoDir 重算）
	Dst      string // 容器内目标路径
	ReadOnly bool   // 只读挂载（密钥/配置类默认 true）
}

// ResolveExtraMounts 解析 needs.mounts 中**非 config** 的挂载（dst != /app/config.yaml）为宿主源。
// config 挂载由 ResolveConfigMount 专门处理（含 legacy 探测 + 确定性 actual 重放），此处跳过避免重复/冲突。
// mf 为 nil（legacy 无 manifest）返回 nil。每条按 resolved-priority 解析。
// 注：extra 挂载的 actual 记录 v1 不回填（RecordActuals 仍只记 config），故恒走重算路径——
// 结果正确，只是不享受确定性缓存；extra 挂载确定性重放为 follow-up。
func ResolveExtraMounts(repoDir string, mf *DeployManifest) []ResolvedMount {
	if mf == nil {
		return nil
	}
	var out []ResolvedMount
	for _, m := range mf.Needs.Mounts {
		if m.Dst == "/app/config.yaml" || m.Src == "" {
			continue
		}
		out = append(out, ResolvedMount{
			HostSrc:  resolveMountHostSrc(repoDir, m.Src, mf.findActualMount(m.Src)),
			Dst:      m.Dst,
			ReadOnly: m.ReadOnly,
		})
	}
	return out
}
```

- [ ] **Step 4: 运行确认通过**

Run: `cd platform/backend && go test -run TestResolveExtraMounts ./internal/appdeploy/`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd platform/backend
gofmt -w internal/appdeploy/deploy_manifest.go internal/appdeploy/deploy_manifest_test.go
cd .. && cd ..
git add platform/backend/internal/appdeploy/deploy_manifest.go platform/backend/internal/appdeploy/deploy_manifest_test.go
git commit -m "feat(appdeploy): ResolveExtraMounts 解析 needs 非config挂载(P1-b)

加 ResolvedMount 类型 + ResolveExtraMounts：跳过 config 挂载(由
ResolveConfigMount 专门处理)，resolved-priority 解析其余挂载到宿主源。
不动 ResolveConfigMount/RecordActuals 及其测试。P1-b 地基。

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: DeployOpts + splitCommand + Deploy 签名改造（deployer 层）

**Files:**

- Modify: `platform/backend/internal/appdeploy/deployer.go`（加 `DeployOpts`/`dockerVolArg`/`splitCommand`；重写 `Deploy`）
- Test: `platform/backend/internal/appdeploy/deployer_test.go`（改 5 调用点 + 加 4 组测试）

**Interfaces:**

- Consumes: `ResolvedMount`（Task 1）。
- Produces: `DeployOpts` 结构（Task 3 `buildAndDeploy` 构造）、`splitCommand`（纯函数）、新 `Deploy(ctx,a,ins,env,dockerHost,opts DeployOpts)` 签名。

- [ ] **Step 1: 加 splitCommand 测试（先纯函数）**

`deployer_test.go` 末尾追加：

```go
// TestSplitCommand 启动命令字符串 → docker run argv 片段（支持引号包裹含空格参数）。
func TestSplitCommand(t *testing.T) {
	cases := map[string][]string{
		"":                        {},
		"./app":                   {"./app"},
		"python main.py --port 8000": {"python", "main.py", "--port", "8000"},
		`sh -c "node server.js"`:  {"sh", "-c", "node server.js"},
		`echo 'hello world'`:      {"echo", "hello world"},
	}
	for in, want := range cases {
		got := splitCommand(in)
		if len(got) != len(want) {
			t.Fatalf("splitCommand(%q)=%v want %v", in, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("splitCommand(%q)[%d]=%q want %q", in, i, got[i], want[i])
			}
		}
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd platform/backend && go test -run TestSplitCommand ./internal/appdeploy/`
Expected: `undefined: splitCommand`。

- [ ] **Step 3: 实现 splitCommand + DeployOpts + dockerVolArg**

`deployer.go`，在端口常量块（`portProdMax` 之后）插入：

```go
// DeployOpts 聚合 .anp/deploy.yaml needs 驱动的部署选项（P1-b：needs 全字段生效）。
// 零值 = 无任何覆盖（等同旧行为：仅可选 ConfigPath、用 a.InternalPort、镜像默认 CMD）。
type DeployOpts struct {
	ConfigPath string          // config.yaml 宿主源（dst=/app/config.yaml:ro）；空=无 config 挂载
	Mounts     []ResolvedMount // 额外挂载（非 config 的 needs.mounts）；Deploy 逐条 -v
	Command    string          // 覆盖镜像 CMD/ENTRYPOINT；空=用镜像默认
	Port       int             // 容器监听端口（ensurePortEnv 的 PORT + -p 容器侧）；0=a.InternalPort
}

// dockerVolArg 拼 docker -v 参数：HostSrc:Dst[:ro]。
func (m ResolvedMount) dockerVolArg() string {
	s := m.HostSrc + ":" + m.Dst
	if m.ReadOnly {
		s += ":ro"
	}
	return s
}

// splitCommand 把启动命令字符串拆成 docker run 的 argv 片段（覆盖镜像 CMD）。
// 支持单/双引号包裹含空格的单个参数（如 sh -c "node server.js"）。
// 不做 shell 元变量/$ 展开（部署命令是声明式，非交互 shell）。
func splitCommand(s string) []string {
	var out []string
	var b strings.Builder
	inSingle, inDouble := false, false
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case (r == ' ' || r == '\t') && !inSingle && !inDouble:
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return out
}
```

- [ ] **Step 4: 运行 splitCommand 测试通过**

Run: `cd platform/backend && go test -run TestSplitCommand ./internal/appdeploy/`
Expected: PASS。

- [ ] **Step 5: 重写 Deploy 签名 + body**

`deployer.go`，把整个 `Deploy` 方法（`func (d *Deployer) Deploy(...)` 到其 `return nil`/`}` 结束，约 :146-214）替换为：

```go
// Deploy 运行容器。headless:无端口/无 URL；web/service:bridge 分配宿主端口 + -p，host 直接绑宿主 listen。
// opts 携带 .anp/deploy.yaml needs 驱动的覆盖（extra mounts / command / needs 端口优先）；零值=旧行为。
// dockerHost 非空时远程部署（host = 该远程节点的 host 网络）。
func (d *Deployer) Deploy(ctx context.Context, a *Application, ins *AppInstance, env []string, dockerHost string, opts DeployOpts) error {
	name := fmt.Sprintf("appdeploy-%s-%s-v%d", dockerSlug(a.Name), ins.Env, ins.Version)
	args := []string{"run", "-d", "--name", name, "--restart", "unless-stopped"}
	isHost := a.NetworkMode == "host"
	if isHost {
		args = append(args, "--network", "host")
	}
	// config.yaml 挂载(特例 dst=/app/config.yaml:ro)：opts.ConfigPath 由 buildAndDeploy 经
	// ResolveConfigMount 解析（manifest 声明优先 + legacy 探测，含确定性 actual 重放）。
	if opts.ConfigPath != "" {
		args = append(args, "-v", opts.ConfigPath+":/app/config.yaml:ro")
	}
	// P1-b：needs.mounts 其余挂载（非 config）逐条 -v（密钥/证书等，不进镜像层）。
	for _, m := range opts.Mounts {
		args = append(args, "-v", m.dockerVolArg())
	}
	// 启动命令覆盖：needs.command 非空 → 拼在镜像后覆盖镜像 CMD/ENTRYPOINT（docker run <img> <cmd...>）。
	cmdArgs := splitCommand(opts.Command)

	if a.AppKind == AppKindHeadless {
		for _, e := range env {
			args = append(args, "-e", e)
		}
		args = append(args, ins.Image)
		args = append(args, cmdArgs...)
		out, err := dockerRun(ctx, dockerHost, args...)
		if err != nil {
			return fmt.Errorf("docker run 失败: %w: %s", err, out)
		}
		ins.ContainerName = name
		return nil
	}

	// web/service：needs.ports[0](opts.Port) 优先于 a.InternalPort 作容器监听端口。
	listen := a.InternalPort
	if opts.Port > 0 {
		listen = opts.Port
	}
	var port int
	if isHost {
		port = listen
	} else {
		min, max := d.envPortRange(ins.Env)
		used := d.usedPortsOn(ctx, dockerHost)
		port = ins.HostPort
		if _, occupied := used[port]; port < min || port > max || occupied {
			port = AllocFreePort(used, min, max)
		}
		if port == 0 {
			return fmt.Errorf("无可用宿主端口（%s 环境 %d-%d 已满）", ins.Env, min, max)
		}
	}
	env = ensurePortEnv(env, listen)
	for _, e := range env {
		args = append(args, "-e", e)
	}
	if !isHost {
		args = append(args, "-p", fmt.Sprintf("%d:%d", port, listen))
	}
	args = append(args, ins.Image)
	args = append(args, cmdArgs...)
	out, err := dockerRun(ctx, dockerHost, args...)
	if err != nil {
		return fmt.Errorf("docker run 失败: %w: %s", err, out)
	}
	ins.ContainerName = name
	ins.HostPort = port
	urlHost := d.host
	if dockerHost != "" {
		parts := strings.Split(strings.TrimPrefix(strings.TrimPrefix(dockerHost, "tcp://"), "http://"), ":")
		if len(parts) > 0 && parts[0] != "" {
			urlHost = parts[0]
		}
	}
	ins.URL = fmt.Sprintf("http://%s:%d", urlHost, port)
	return nil
}
```

- [ ] **Step 6: 更新 5 个旧调用点（configPath → opts）**

`deployer_test.go`，逐处把第 6 个参数从 `configPath` 字符串改成 `DeployOpts`：

- :245 `d.Deploy(context.Background(), a, ins, []string{"FOO=bar"}, "", "")` → 末参 `DeployOpts{}`
- :280 `d.Deploy(context.Background(), a, ins, nil, "", "")` → `DeployOpts{}`
- :313 `d.Deploy(context.Background(), a, ins, nil, "", "")` → `DeployOpts{}`
- :355 `d.Deploy(context.Background(), a, ins, nil, "", cfg)` → `DeployOpts{ConfigPath: cfg}`
- :372 `d.Deploy(context.Background(), a, ins, nil, "", "")` → `DeployOpts{}`

（用各自完整行作 `old_string` 精确替换，避免歧义。）

- [ ] **Step 7: 加 Deploy 新行为测试**

`deployer_test.go` 追加（均用 `dockerRun` 注入断言 args）：

```go
// TestDeploy_ExtraMounts opts.Mounts 逐条 -v（含 :ro）。
func TestDeploy_ExtraMounts(t *testing.T) {
	var got []string
	orig := dockerRun
	dockerRun = func(_ context.Context, _ string, args ...string) (string, error) { got = args; return "cid", nil }
	defer func() { dockerRun = orig }()
	d := NewDeployer("h")
	a := &Application{Name: "demo", AppKind: AppKindWeb, InternalPort: 8080}
	ins := &AppInstance{Env: EnvTest, Version: 1, HostPort: 9100}
	opts := DeployOpts{Mounts: []ResolvedMount{{HostSrc: "/data/secrets/tls.crt", Dst: "/etc/tls/tls.crt", ReadOnly: true}}}
	if err := d.Deploy(context.Background(), a, ins, nil, "", opts); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(got, " "), "-v /data/secrets/tls.crt:/etc/tls/tls.crt:ro") {
		t.Fatalf("应挂载 extra mount，得 %v", got)
	}
}

// TestDeploy_CommandOverride opts.Command 拆词后追加在镜像之后，覆盖 CMD。
func TestDeploy_CommandOverride(t *testing.T) {
	var got []string
	orig := dockerRun
	dockerRun = func(_ context.Context, _ string, args ...string) (string, error) { got = args; return "cid", nil }
	defer func() { dockerRun = orig }()
	d := NewDeployer("h")
	a := &Application{Name: "demo", AppKind: AppKindWeb, InternalPort: 8080}
	ins := &AppInstance{Env: EnvTest, Version: 1, HostPort: 9100}
	if err := d.Deploy(context.Background(), a, ins, nil, "", DeployOpts{Command: "python main.py --port 8000"}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, " ")
	img := ins.Image
	if !strings.Contains(joined, img+" python main.py --port 8000") {
		t.Fatalf("镜像后应追加命令词，得 %v", got)
	}
}

// TestDeploy_NeedsPortOverrides opts.Port 覆盖 a.InternalPort：-p 用 opts.Port + PORT=opts.Port。
func TestDeploy_NeedsPortOverrides(t *testing.T) {
	var got []string
	orig := dockerRun
	dockerRun = func(_ context.Context, _ string, args ...string) (string, error) { got = args; return "cid", nil }
	defer func() { dockerRun = orig }()
	d := NewDeployer("10.10.0.28")
	a := &Application{Name: "webapp", AppKind: AppKindWeb, InternalPort: 3000}
	ins := &AppInstance{Env: EnvTest, Version: 1}
	if err := d.Deploy(context.Background(), a, ins, nil, "", DeployOpts{Port: 5000}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "-p 9100:5000") {
		t.Fatalf("-p 应映射到 needs 端口 5000，得 %v", got)
	}
	if !strings.Contains(joined, "PORT=5000") {
		t.Fatalf("PORT 应注入 needs 端口 5000，得 %v", got)
	}
}
```

- [ ] **Step 8: 运行 deployer 全测试**

Run: `cd platform/backend && go test -run 'TestDeploy|TestSplitCommand|TestPortRange|TestEnvPortRange' -v ./internal/appdeploy/`
Expected: PASS（含更新后的 5 旧测试 + 4 新测试）。

- [ ] **Step 9: 提交**

```bash
cd platform/backend && gofmt -w internal/appdeploy/deployer.go internal/appdeploy/deployer_test.go
cd .. && cd ..
git add platform/backend/internal/appdeploy/deployer.go platform/backend/internal/appdeploy/deployer_test.go
git commit -m "feat(appdeploy): Deploy 接 DeployOpts 消费 needs(command/port/mounts)

- 加 DeployOpts{ConfigPath,Mounts,Command,Port}+splitCommand+dockerVolArg
- Deploy 签名 configPath→opts；body 接 extra-mounts/命令覆盖/needs端口优先
- 零值opts=旧行为，5旧调用点改opts断言不变；加4组新测试

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: buildAndDeploy 接线 needs + env_keys 软校验（handler 层）

**Files:**

- Modify: `platform/backend/internal/appdeploy/handler.go`（`buildAndDeploy` :1939-1943 改；加 `missingEnvKeys`/`validateEnvKeys`）
- Test: `platform/backend/internal/appdeploy/deploy_manifest_test.go`（加 `TestMissingEnvKeys_*`，纯函数）

**Interfaces:**

- Consumes: `DeployOpts`/`ResolveExtraMounts`（Task 1/2）、`mf.Needs.Ports/Command/EnvKeys`。
- Produces: needs 四字段真正驱动部署 + env_keys 缺值软告警。

- [ ] **Step 1: 写 missingEnvKeys 测试**

`deploy_manifest_test.go` 末尾追加：

```go
// === missingEnvKeys（P1-b 软校验）===

func TestMissingEnvKeys_DeclaredWithoutValue(t *testing.T) {
	mf := &DeployManifest{Needs: NeedsSpec{EnvKeys: []string{"REDIS_ADDR", "FOO"}}}
	// envPairs 无 REDIS_ADDR 无 FOO → 两者都缺
	got := missingEnvKeys(mf, []string{"BAR=1"}, false)
	if len(got) != 2 {
		t.Fatalf("应缺 2 个 got=%v", got)
	}
}

func TestMissingEnvKeys_AutoInjectedCounted(t *testing.T) {
	// PORT 恒注入；CONFIG_PATH 在 hasConfig 时注入 → 均不算缺
	mf := &DeployManifest{Needs: NeedsSpec{EnvKeys: []string{"PORT", "CONFIG_PATH"}}}
	if got := missingEnvKeys(mf, nil, true); len(got) != 0 {
		t.Fatalf("PORT/CONFIG_PATH 自动注入不应缺 got=%v", got)
	}
	// hasConfig=false 时 CONFIG_PATH 缺
	mf2 := &DeployManifest{Needs: NeedsSpec{EnvKeys: []string{"CONFIG_PATH"}}}
	if got := missingEnvKeys(mf2, nil, false); len(got) != 1 || got[0] != "CONFIG_PATH" {
		t.Fatalf("无 config 时 CONFIG_PATH 应缺 got=%v", got)
	}
}

func TestMissingEnvKeys_PresentInEnvPairs(t *testing.T) {
	mf := &DeployManifest{Needs: NeedsSpec{EnvKeys: []string{"FOO"}}}
	if got := missingEnvKeys(mf, []string{"FOO=bar"}, false); len(got) != 0 {
		t.Fatalf("envPairs 含 FOO 不应缺 got=%v", got)
	}
}

func TestMissingEnvKeys_NilManifest(t *testing.T) {
	if got := missingEnvKeys(nil, nil, false); got != nil {
		t.Fatalf("nil manifest 应 nil got=%v", got)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd platform/backend && go test -run TestMissingEnvKeys ./internal/appdeploy/`
Expected: `undefined: missingEnvKeys`。

- [ ] **Step 3: 实现 missingEnvKeys + validateEnvKeys**

`handler.go`，在 `buildAndDeploy` 之前（或文件末尾合适处，如 `markBuilding` 附近）追加两个函数：

```go
// missingEnvKeys 返回 needs.env_keys 中无值来源的 key（envPairs 含 KEY= 或自动注入 PORT/CONFIG_PATH）。
// 纯函数，供 validateEnvKeys 软校验单测。
func missingEnvKeys(mf *DeployManifest, envPairs []string, hasConfig bool) []string {
	if mf == nil || len(mf.Needs.EnvKeys) == 0 {
		return nil
	}
	present := map[string]bool{"PORT": true} // ensurePortEnv 恒注入 PORT
	if hasConfig {
		present["CONFIG_PATH"] = true
	}
	for _, kv := range envPairs {
		if i := strings.IndexByte(kv, '='); i > 0 {
			present[kv[:i]] = true
		}
	}
	var missing []string
	for _, k := range mf.Needs.EnvKeys {
		if !present[k] {
			missing = append(missing, k)
		}
	}
	return missing
}

// validateEnvKeys 软校验 needs.env_keys：声明但无值来源的 key 记 WARN，不阻断部署。
// best-effort（避免 opencode 误填/中间件未绑定卡死部署）。
func validateEnvKeys(a *Application, mf *DeployManifest, envPairs []string, hasConfig bool) {
	for _, k := range missingEnvKeys(mf, envPairs, hasConfig) {
		zap.L().Warn("[appdeploy] needs.env_keys 声明但无值来源（不阻断，检查中间件绑定/密钥配置）",
			zap.String("app", a.Name), zap.String("env_key", k))
	}
}
```

- [ ] **Step 4: 运行 missingEnvKeys 测试通过**

Run: `cd platform/backend && go test -run TestMissingEnvKeys ./internal/appdeploy/`
Expected: PASS。

- [ ] **Step 5: 接线 buildAndDeploy**

`handler.go`，把 :1939-1943 段：

```go
	configHostPath, configSrc, hasConfig := ResolveConfigMount(a.RepoDir, mf)
	if hasConfig {
		envPairs = append(envPairs, "CONFIG_PATH=/app/config.yaml")
	}
	dErr := h.deployer.Deploy(deployCtx, a, ins, envPairs, dockerHost, configHostPath)
```

替换为：

```go
	configHostPath, configSrc, hasConfig := ResolveConfigMount(a.RepoDir, mf)
	if hasConfig {
		envPairs = append(envPairs, "CONFIG_PATH=/app/config.yaml")
	}
	// P1-b：needs 全字段消费。ports→容器监听端口(优先 needs.ports[0])；command→覆盖镜像 CMD；
	// mounts→额外挂载(非 config，config 由 ConfigPath 单独处理)；env_keys→校验有值来源(缺失仅 WARN)。
	deployOpts := DeployOpts{
		ConfigPath: configHostPath,
		Mounts:     ResolveExtraMounts(a.RepoDir, mf),
	}
	if mf != nil {
		if len(mf.Needs.Ports) > 0 {
			deployOpts.Port = mf.Needs.Ports[0]
		}
		deployOpts.Command = mf.Needs.Command
	}
	validateEnvKeys(a, mf, envPairs, hasConfig)
	dErr := h.deployer.Deploy(deployCtx, a, ins, envPairs, dockerHost, deployOpts)
```

- [ ] **Step 6: 构建验证**

Run: `cd platform/backend && go build ./internal/appdeploy/ && go vet ./internal/appdeploy/`
Expected: 成功（`handler.go:1943` 旧 configPath 调用已改为 opts，编译通过）。

- [ ] **Step 7: 提交**

```bash
cd platform/backend && gofmt -w internal/appdeploy/handler.go internal/appdeploy/deploy_manifest_test.go
cd .. && cd ..
git add platform/backend/internal/appdeploy/handler.go platform/backend/internal/appdeploy/deploy_manifest_test.go
git commit -m "feat(appdeploy): buildAndDeploy 消费 needs 全字段+env_keys软校验(P1-b)

needs.ports[0]→容器监听端口；needs.command→覆盖镜像CMD；
needs.mounts(非config)→额外挂载；needs.env_keys→缺值WARN不阻断
(missingEnvKeys 纯函数+validateEnvKeys)。Deploy 传 DeployOpts。

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 4: 全量验证 + 合 main + 部署 .28

- [ ] **Step 1: 全后端测试**

Run: `cd platform/backend && go test -p 1 -count=1 ./...`
Expected: 全 PASS（含 deploy_manifest/deployer 新旧测试、handler 编译）。

- [ ] **Step 2: ff 合 main**

```bash
cd <仓库根>
git checkout main
git merge --ff-only feat/app-panorama-p1b
```

（origin 推送待用户确认。）

- [ ] **Step 3: 部署 backend 到 .28**

改了 4 个后端文件（deploy_manifest.go/.deployer.go/handler.go + 2 测试）。测试文件不影响运行，scp 3 个源文件 + 重建 backend：

```bash
SSH="ssh -o PubkeyAcceptedAlgorithms=+ssh-rsa -o StrictHostKeyChecking=no -i ~/.ssh/miscode root@10.10.0.28"
SCP_OPTS="-o PubkeyAcceptedAlgorithms=+ssh-rsa -o StrictHostKeyChecking=no -i ~/.ssh/miscode"
for f in platform/backend/internal/appdeploy/deploy_manifest.go \
         platform/backend/internal/appdeploy/deployer.go \
         platform/backend/internal/appdeploy/handler.go ; do
  scp $SCP_OPTS "$f" "root@10.10.0.28:/opt/anp/$f"
done
$SSH "cd /opt/anp && docker-compose -f deploy/docker-compose.prod.yml up --build -d backend"
```

验证 `deploy_backend_1` Up、日志无 panic。

- [ ] **Step 4: e2e 抽查（待用户）**

有 `.anp/deploy.yaml` 声明 needs.ports/command/mounts 的应用，点构建部署 → 容器按 needs 启动（端口/命令/挂载生效），build_log 无 env_keys WARN（或 WARN 对应真实缺值）。标记**待用户浏览器 e2e**。

---

## Self-Review

**1. Spec 覆盖**（spec §4.1 P1-b 四字段）：

- ports → Task 2 Step5（opts.Port 优先）+ Task 3 Step5（needs.Ports[0]→opts.Port）✅
- command → Task 2 splitCommand+Deploy 追加 + Task 3 needs.Command→opts.Command ✅
- env_keys → Task 3 missingEnvKeys+validateEnvKeys（WANR 不阻断）✅
- mounts → Task 1 ResolveExtraMounts + Task 2 Deploy -v + Task 3 opts.Mounts ✅
- 保留软回退 → LoadDeployManifest 失败仅 warn（既有 :1931-1934，未改）✅；mf==nil 时 opts 仅 ConfigPath，其余零值=旧行为 ✅

**2. 占位符扫描**：无 TBD；每步完整代码；commit/测试完整。✅

**3. 类型/命名一致性**：

- `ResolvedMount{HostSrc,Dst,ReadOnly}`：Task1 定义 → Task2 `dockerVolArg()` → Task2/3 测试，一致。✅
- `DeployOpts{ConfigPath,Mounts,Command,Port}`：Task2 定义 → Task3 构造 → Deploy 消费，字段名一致。✅
- `splitCommand`/`missingEnvKeys`/`validateEnvKeys`/`ResolveExtraMounts` 定义与调用一致。✅
- Deploy 新签名 `(ctx,a,ins,env,dockerHost,opts DeployOpts)`：handler:1943 + 5 测试全更新。✅
- 向后兼容不变式：零值 opts = 旧行为，5 旧测试断言不变。✅

无遗留问题。

---

## Execution

inline 执行（用户已选模式 1）。逐 task TDD → 提交 → T4 合 main + 部署 .28。

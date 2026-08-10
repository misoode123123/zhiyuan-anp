package appdeploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"zhiyuan-anp/platform/backend/internal/standard"
)

// 各环境宿主端口分配区间（互不冲突；避开 .28 上 lowcode/帆软/ANP 已用端口）。
// 单一源在 standard 包（PortTestMin 等）；此处编译期别名引用——改 standard 一处，
// AGENTS.md 渲染与本引擎端口分配同步，防规则源与实现脱节。
const (
	portTestMin = standard.PortTestMin
	portTestMax = standard.PortTestMax
	portProdMin = standard.PortProdMin
	portProdMax = standard.PortProdMax
)

// Deployer 通过宿主 docker socket 构建运行应用容器。
type Deployer struct {
	host string // 公布 URL 的主机（10.10.0.28 / localhost）
}

// NewDeployer 构造。host 用于拼访问 URL。
func NewDeployer(host string) *Deployer { return &Deployer{host: host} }

// envPortRange 按环境返回宿主端口区间：test 9100-9199，prod 9200-9300。
func (d *Deployer) envPortRange(env string) (int, int) {
	if env == EnvProd {
		return portProdMin, portProdMax
	}
	return portTestMin, portTestMax
}

// runDocker 执行 docker 子命令（本地），返回合并输出。
func runDocker(ctx context.Context, args ...string) (string, error) {
	return runDockerOn(ctx, "", args...)
}

// runDockerOn 在指定 Docker host 执行命令（host 为空=本地，否则 tcp://ip:2375）。
// 用于多节点部署：应用容器可部署到 .30 等远程节点。
func runDockerOn(ctx context.Context, dockerHost string, args ...string) (string, error) {
	fullArgs := args
	if dockerHost != "" {
		// docker -H tcp://10.10.0.30:2375 <args>
		fullArgs = append([]string{"-H", dockerHost}, args...)
	}
	cmd := exec.CommandContext(ctx, "docker", fullArgs...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

var hostPortRe = regexp.MustCompile(`(?::[\d.]+)?:(\d+)->`)

// usedPortsOn 查询指定 docker host 上运行中容器占用的宿主端口。
func (d *Deployer) usedPortsOn(ctx context.Context, dockerHost string) map[int]struct{} {
	used := map[int]struct{}{}
	out, _ := runDockerOn(ctx, dockerHost, "ps", "--format", "{{.Ports}}")
	for _, line := range regexp.MustCompile(`\r?\n`).Split(out, -1) {
		for _, m := range hostPortRe.FindAllStringSubmatch(line, -1) {
			if len(m) > 1 {
				if p, err := strconv.Atoi(m[1]); err == nil {
					used[p] = struct{}{}
				}
			}
		}
	}
	return used
}

// AllocFreePort 在 [min,max] 内选首个未被占用的端口；无可用返回 0。纯函数，可单测。
func AllocFreePort(used map[int]struct{}, min, max int) int {
	for p := min; p <= max; p++ {
		if _, ok := used[p]; !ok {
			return p
		}
	}
	return 0
}

// ensurePortEnv 若 env 未含 PORT= 则补 PORT=port；应用显式设了 PORT 则尊重不覆盖。
// 让 PORT-driven 应用(node process.env.PORT / python)监听与 docker -p 映射一致的端口。
func ensurePortEnv(env []string, port int) []string {
	for _, e := range env {
		if strings.HasPrefix(e, "PORT=") {
			return env
		}
	}
	return append(env, fmt.Sprintf("PORT=%d", port))
}

// dashRe 匹配连续的 -，用于折叠 dockerSlug 的分隔符。
var dashRe = regexp.MustCompile(`-+`)

// slugValidRe docker 镜像路径段/容器名片段的合法性：[a-z0-9] 起止，中间允许 -。
// （docker reference 路径段正则 [a-z0-9]+(?:(?:[._]|__|[-]+)[a-z0-9]+)* 的子集，保守只用 - 分隔。）
var slugValidRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// dockerSlug 把应用名转成 docker 合法的镜像 tag / 容器名片段（小写字母数字，- 分隔）。
// docker reference 只允许 ASCII（路径段须 [a-z0-9] 起止）；应用名含中文等非 ASCII 字符时直接
// 拼进 `appdeploy/<name>-<env>:v<n>` 会报 invalid reference format（如「客服机器人」→
// appdeploy/客服机器人-test:v4 → docker build exit 125）。这里把非法 rune 替换为 -；纯非法
// （如全中文，替换后为空）时退回名字 sha256 前缀——稳定可复现，同一应用每次得到相同 slug，
// RemoveByPrefix / RemoveImages 仍能匹配历史容器/镜像，不残留孤儿。
// 与 codews.sanitizeID 同思路（小写、保 [a-z0-9]、其余转 -），但兜底用名哈希而非固定串，
// 避免两个纯中文应用撞名导致容器名冲突。
func dockerSlug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	s := strings.Trim(dashRe.ReplaceAllString(b.String(), "-"), "-")
	if s != "" {
		return s
	}
	sum := sha256.Sum256([]byte(name))
	return "app-" + hex.EncodeToString(sum[:6])
}

// Build 构建镜像（docker build -t <image> <buildDir>），版本号按环境实例自增。
// buildDir 取码目录：test 环境从开发者 worktree 构建时是 dev-<user> 目录，否则为主仓 a.RepoDir。
// dockerHost 非空时在远程节点构建（tcp://10.10.0.30:2375）。
func (d *Deployer) Build(ctx context.Context, a *Application, ins *AppInstance, dockerHost, buildDir string) (log string, err error) {
	ins.Version++
	ins.Image = fmt.Sprintf("appdeploy/%s-%s:v%d", dockerSlug(a.Name), ins.Env, ins.Version)
	out, e := runDockerOn(ctx, dockerHost, "build", "-t", ins.Image, buildDir)
	return out, e
}

// Deploy 运行容器。headless:无端口/无 URL；web/service:bridge 分配宿主端口 + -p，host 直接绑宿主 internalPort（无 -p）。
// network_mode 与 app_kind 正交：host flag 对所有 app_kind 生效（headless+host 也共享宿主网络）。
// dockerHost 非空时远程部署（host = 该远程节点的 host 网络）。
func (d *Deployer) Deploy(ctx context.Context, a *Application, ins *AppInstance, env []string, dockerHost, configPath string) error {
	name := fmt.Sprintf("appdeploy-%s-%s-v%d", dockerSlug(a.Name), ins.Env, ins.Version)
	args := []string{"run", "-d", "--name", name, "--restart", "unless-stopped"}
	isHost := a.NetworkMode == "host"
	if isHost {
		args = append(args, "--network", "host")
	}
	// config.yaml 挂载(spec ①):configPath 非空则 ro 挂到 /app/config.yaml,兑现 adapt secret 挂载假设。
	// 调用方保证 configPath=<RepoDir>/config.yaml(buildAndDeploy 检测),防 -v 逃逸挂载宿主敏感文件。
	if configPath != "" {
		args = append(args, "-v", configPath+":/app/config.yaml:ro")
	}

	if a.AppKind == AppKindHeadless {
		// headless(bot/worker)：无端口、无 URL。不分配宿主端口、不注入 PORT、不 -p（host flag 已在 args 顶部）。
		for _, e := range env {
			args = append(args, "-e", e)
		}
		args = append(args, ins.Image)
		out, err := dockerRun(ctx, dockerHost, args...)
		if err != nil {
			return fmt.Errorf("docker run 失败: %w: %s", err, out)
		}
		ins.ContainerName = name
		return nil // ins.HostPort/URL 保持零值
	}

	// web/service：host 模式无 -p（绑宿主 internalPort）；bridge 模式分配宿主端口 + -p。
	var port int
	if isHost {
		port = a.InternalPort
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
	env = ensurePortEnv(env, a.InternalPort)
	for _, e := range env {
		args = append(args, "-e", e)
	}
	if !isHost {
		args = append(args, "-p", fmt.Sprintf("%d:%d", port, a.InternalPort))
	}
	args = append(args, ins.Image)
	out, err := dockerRun(ctx, dockerHost, args...)
	if err != nil {
		return fmt.Errorf("docker run 失败: %w: %s", err, out)
	}
	ins.ContainerName = name
	ins.HostPort = port // host 模式 = internalPort（host 命名空间可达端口）→ appgw UpsertRoute 零改动
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

// Stop 停止容器。
func (d *Deployer) Stop(ctx context.Context, container string) (string, error) {
	return runDocker(ctx, "stop", container)
}

// Start 启动已停止容器。
func (d *Deployer) Start(ctx context.Context, container string) (string, error) {
	return runDocker(ctx, "start", container)
}

// Remove 删除容器（强删）。
func (d *Deployer) Remove(ctx context.Context, container string) (string, error) {
	return runDocker(ctx, "rm", "-f", container)
}

// RemoveByPrefix 删除所有名字含 prefix 的容器（清理同 app+env 的历史残留/孤儿容器，
// 彻底释放端口，避免新部署端口漂移或 Conflict——只删 DB 记录的一个清不到孤儿）。
func (d *Deployer) RemoveByPrefix(ctx context.Context, prefix string) (string, error) {
	out, _ := runDocker(ctx, "ps", "-a", "--filter", "name="+prefix, "--format", "{{.Names}}")
	var combined string
	for _, name := range parseContainerNames(out) {
		o, _ := runDocker(ctx, "rm", "-f", name)
		combined += o
	}
	return combined, nil
}

// RemoveImages 删除某应用名的所有镜像(appdeploy/<name>-*:*)，避免删除应用后镜像堆积。
func (d *Deployer) RemoveImages(ctx context.Context, appName string) (string, error) {
	out, _ := runDocker(ctx, "images", "--format", "{{.Repository}}:{{.Tag}}", "appdeploy/"+appName+"-*")
	var combined string
	for _, img := range parseContainerNames(out) { // 复用换行分割
		if img == "" {
			continue
		}
		o, _ := runDocker(ctx, "rmi", "-f", img)
		combined += o
	}
	return combined, nil
}

// parseContainerNames 解析 `docker ps --format {{.Names}}` 输出为容器名列表（纯函数，可单测）。
func parseContainerNames(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// Logs 取容器日志尾部。
func (d *Deployer) Logs(ctx context.Context, container string, tail int) (string, error) {
	if tail <= 0 {
		tail = 100
	}
	return runDocker(ctx, "logs", "--tail", strconv.Itoa(tail), container)
}

// ContainerStats 容器资源占用快照（docker stats 解析）。
type ContainerStats struct {
	CPUPerc  string `json:"cpu_perc"`  // "0.03%"
	MemUsage string `json:"mem_usage"` // "7.5MiB / 7.66GiB"
	MemPerc  string `json:"mem_perc"`  // "0.1%"
	NetIO    string `json:"net_io"`    // "1.2kB / 3.4kB"
	PIDs     string `json:"pids"`
}

// Stats 取容器资源占用（单次快照，非流式）。
func (d *Deployer) Stats(ctx context.Context, container string) (*ContainerStats, error) {
	out, err := runDocker(ctx, "stats", "--no-stream", "--format", "{{json .}}", container)
	if err != nil {
		return nil, fmt.Errorf("docker stats: %w: %s", err, out)
	}
	var raw struct {
		CPUPerc, MemUsage, MemPerc, NetIO, PIDs string
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &raw); err != nil {
		return nil, fmt.Errorf("解析 stats JSON: %w (原文: %s)", err, out)
	}
	return &ContainerStats{CPUPerc: raw.CPUPerc, MemUsage: raw.MemUsage, MemPerc: raw.MemPerc, NetIO: raw.NetIO, PIDs: raw.PIDs}, nil
}

// ContainerHealth docker inspect 解析出的容器健康观测。
type ContainerHealth struct {
	Running      bool
	RestartCount int
	ExitCode     int
	OOMKilled    bool
}

// InspectHealth 经 docker inspect 取容器健康观测。走 docker socket,不受应用网络拓扑影响。
func (d *Deployer) InspectHealth(ctx context.Context, container string) (ContainerHealth, error) {
	out, err := runDocker(ctx, "inspect", "--format",
		"{{.State.Status}}|{{.RestartCount}}|{{.State.ExitCode}}|{{.State.OOMKilled}}", container)
	if err != nil {
		return ContainerHealth{}, fmt.Errorf("docker inspect %s: %w: %s", container, err, out)
	}
	return parseInspectHealth(strings.TrimSpace(out))
}

// parseInspectHealth 解析 `status|restartCount|exitCode|oomKilled` 输出。纯函数,可单测。
func parseInspectHealth(out string) (ContainerHealth, error) {
	parts := strings.Split(out, "|")
	if len(parts) != 4 {
		return ContainerHealth{}, fmt.Errorf("parseInspectHealth: expect 4 fields, got %d in %q", len(parts), out)
	}
	rc, _ := strconv.Atoi(parts[1])
	ec, _ := strconv.Atoi(parts[2])
	return ContainerHealth{
		Running:      parts[0] == "running",
		RestartCount: rc,
		ExitCode:     ec,
		OOMKilled:    parts[3] == "true",
	}, nil
}

// dockerRun 可替换的 docker 执行函数（测试注入 fake）。默认走 runDockerOn。
// 新代码（builders 等）调 dockerRun(...) 而非 runDockerOn，便于单测注入不执行真实 docker 的桩。
var dockerRun = runDockerOn

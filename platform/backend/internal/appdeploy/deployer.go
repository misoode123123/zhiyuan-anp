package appdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// 各环境宿主端口分配区间（互不冲突；避开 .28 上 lowcode/帆软/ANP 已用端口）。
const (
	portTestMin = 9100
	portTestMax = 9199
	portProdMin = 9200
	portProdMax = 9300
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

// runDocker 执行 docker 子命令，返回合并输出。
func runDocker(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

var hostPortRe = regexp.MustCompile(`(?::[\d.]+)?:(\d+)->`)

// usedPorts 查询当前运行中容器占用的宿主端口。
func (d *Deployer) usedPorts(ctx context.Context) map[int]struct{} {
	used := map[int]struct{}{}
	out, _ := runDocker(ctx, "ps", "--format", "{{.Ports}}")
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

// Build 构建镜像（docker build -t <image> <repo_dir>），版本号按环境实例自增。
// 镜像名带环境后缀(test/prod)，避免两环境镜像互相覆盖。
func (d *Deployer) Build(ctx context.Context, a *Application, ins *AppInstance) (log string, err error) {
	ins.Version++
	ins.Image = fmt.Sprintf("appdeploy/%s-%s:v%d", a.Name, ins.Env, ins.Version)
	out, e := runDocker(ctx, "build", "-t", ins.Image, a.RepoDir)
	return out, e
}

// Deploy 运行容器（docker run -d --name -p host:internal -e KEY=VALUE ...）。
// 端口段按环境；优先复用该环境实例原端口（同环境多次发布 URL 稳定）。
// env 为应用的运行时环境变量（含密钥），逐个 -e 注入容器。
func (d *Deployer) Deploy(ctx context.Context, a *Application, ins *AppInstance, env []string) error {
	min, max := d.envPortRange(ins.Env)
	used := d.usedPorts(ctx)
	port := ins.HostPort
	if _, occupied := used[port]; port < min || port > max || occupied {
		port = AllocFreePort(used, min, max)
	}
	if port == 0 {
		return fmt.Errorf("无可用宿主端口（%s 环境 %d-%d 已满）", ins.Env, min, max)
	}
	name := fmt.Sprintf("appdeploy-%s-%s-v%d", a.Name, ins.Env, ins.Version)
	// 注入 PORT=internal_port(应用未显式设时): node/python 等 PORT-driven 应用据此
	// 监听与 -p 映射一致的端口; 否则它们监听默认端口(如 8080)而 docker -p 映射的是
	// internal_port → host 端口连不上(曾导致 snake test/prod URL 打不开)。
	env = ensurePortEnv(env, a.InternalPort)
	args := []string{"run", "-d", "--name", name}
	for _, e := range env {
		args = append(args, "-e", e)
	}
	args = append(args, "-p", fmt.Sprintf("%d:%d", port, a.InternalPort), "--restart", "unless-stopped", ins.Image)
	out, err := runDocker(ctx, args...)
	if err != nil {
		return fmt.Errorf("docker run 失败: %w: %s", err, out)
	}
	ins.ContainerName = name
	ins.HostPort = port
	ins.URL = fmt.Sprintf("http://%s:%d", d.host, port)
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

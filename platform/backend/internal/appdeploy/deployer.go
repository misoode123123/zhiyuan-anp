package appdeploy

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
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

// Build 构建镜像（docker build -t <image> <repo_dir>），版本号按环境实例自增。
// 镜像名带环境后缀(test/prod)，避免两环境镜像互相覆盖。
func (d *Deployer) Build(ctx context.Context, a *Application, ins *AppInstance) (log string, err error) {
	ins.Version++
	ins.Image = fmt.Sprintf("appdeploy/%s-%s:v%d", a.Name, ins.Env, ins.Version)
	out, e := runDocker(ctx, "build", "-t", ins.Image, a.RepoDir)
	return out, e
}

// Deploy 运行容器（docker run -d --name -p host:internal）。
// 端口段按环境；优先复用该环境实例原端口（同环境多次发布 URL 稳定）。
func (d *Deployer) Deploy(ctx context.Context, a *Application, ins *AppInstance) error {
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
	out, err := runDocker(ctx, "run", "-d", "--name", name,
		"-p", fmt.Sprintf("%d:%d", port, a.InternalPort),
		"--restart", "unless-stopped", ins.Image)
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

// Logs 取容器日志尾部。
func (d *Deployer) Logs(ctx context.Context, container string, tail int) (string, error) {
	if tail <= 0 {
		tail = 100
	}
	return runDocker(ctx, "logs", "--tail", strconv.Itoa(tail), container)
}

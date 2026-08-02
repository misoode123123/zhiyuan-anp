package mwsupply

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
)

// MWDockerRunner 经宿主 docker socket 管理 dedicated 中间件容器。抽接口便于单测用 fake。
type MWDockerRunner interface {
	UsedPorts(ctx context.Context) map[int]struct{}
	RunRedisContainer(ctx context.Context, name, password string, port int) error
	RmForce(ctx context.Context, name string) error
}

// osDocker 默认实现：调 docker CLI。
type osDocker struct{}

// NewOSDocker 构造。
func NewOSDocker() MWDockerRunner { return osDocker{} }

// runDockerCmd 执行 docker 子命令，返回合并输出。
func runDockerCmd(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

var hostPortRe = regexp.MustCompile(`(?::[\d.]+)?:(\d+)->`)

// parsePortsOutput 解析 docker ps --format {{.Ports}} 的输出，提取宿主 publish 端口。
func parsePortsOutput(out string) map[int]struct{} {
	used := map[int]struct{}{}
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

// UsedPorts 查运行中容器占用的宿主端口。
func (osDocker) UsedPorts(ctx context.Context) map[int]struct{} {
	out, _ := runDockerCmd(ctx, "ps", "--format", "{{.Ports}}")
	return parsePortsOutput(out)
}

// redisRunArgs 构造 docker run 参数（纯函数，可单测）。
//   redis:7-alpine + redis-server --requirepass；-p host:6379 publish；--restart unless-stopped 自恢复。
func redisRunArgs(name, password string, port int) []string {
	return []string{
		"run", "-d", "--name", name,
		"-e", "REDIS_PASSWORD=" + password,
		"-p", fmt.Sprintf("%d:%d", port, redisInternalPort),
		"--restart", "unless-stopped",
		redisImage,
		"redis-server", "--requirepass", password,
	}
}

// RunRedisContainer docker run -d 起 dedicated redis 容器（不做就绪检测，由 supplyDedicated 的 ReadyChecker 负责）。
func (osDocker) RunRedisContainer(ctx context.Context, name, password string, port int) error {
	out, err := runDockerCmd(ctx, redisRunArgs(name, password, port)...)
	if err != nil {
		return fmt.Errorf("docker run redis: %w: %s", err, out)
	}
	return nil
}

// RmForce 强删容器（清理失败的供给 / 删 app 回收）。
func (osDocker) RmForce(ctx context.Context, name string) error {
	_, err := runDockerCmd(ctx, "rm", "-f", name)
	return err
}

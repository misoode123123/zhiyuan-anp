package pgsupply

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
)

// DockerRunner 经宿主 docker socket 管理 PG 容器。抽接口便于单测用 fake。
type DockerRunner interface {
	UsedPorts(ctx context.Context) map[int]struct{}
	RunPGContainer(ctx context.Context, name, password string, port int) error
	RmForce(ctx context.Context, name string) error
}

// osDocker 默认实现：调 docker CLI。
type osDocker struct{}

// NewOSDocker 构造。
func NewOSDocker() DockerRunner { return osDocker{} }

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

// UsedPorts 查运行中容器占用的宿主端口。
func (osDocker) UsedPorts(ctx context.Context) map[int]struct{} {
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

// RunPGContainer docker run -d --name <name> -e POSTGRES_PASSWORD=<pwd> -p <port>:5432 pgvector/pgvector:pg16。
func (osDocker) RunPGContainer(ctx context.Context, name, password string, port int) error {
	args := []string{
		"run", "-d", "--name", name,
		"-e", "POSTGRES_PASSWORD=" + password,
		"-p", fmt.Sprintf("%d:%d", port, pgInternalPort),
		"--restart", "unless-stopped",
		pgImage,
	}
	out, err := runDocker(ctx, args...)
	if err != nil {
		return fmt.Errorf("docker run pg: %w: %s", err, out)
	}
	return nil
}

// RmForce 强删容器（清理失败的供给）。
func (osDocker) RmForce(ctx context.Context, name string) error {
	_, err := runDocker(ctx, "rm", "-f", name)
	return err
}

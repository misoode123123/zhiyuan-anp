package mwsupply

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// MWDockerRunner 经宿主 docker socket 管理 dedicated 中间件容器。抽接口便于单测用 fake。
type MWDockerRunner interface {
	UsedPorts(ctx context.Context) map[int]struct{}
	RunRedisContainer(ctx context.Context, name, password string, port int) error
	RunMilvusStack(ctx context.Context, base string, port int) error           // P4：专属网络 + milvus/etcd/minio 三容器
	MilvusReady(ctx context.Context, base string, timeout time.Duration) error // P4：alpine 探针轮询 /healthz
	RmForce(ctx context.Context, name string) error
	RmMilvusStack(ctx context.Context, base string) error // P4：rm 三容器 + 网络
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

// etcdRunArgs 构造 etcd 容器 docker run 参数（纯函数，可单测）。
// 在 base-net 网络上别名 etcd，供 milvus 经 ETCD_ENDPOINTS=etcd:2379 解析。
func etcdRunArgs(base string) []string {
	return []string{
		"run", "-d", "--name", milvusEtcdName(base),
		"--network", milvusNetName(base), "--network-alias", "etcd",
		"--restart", "unless-stopped",
		etcdImage,
		"etcd", "-advertise-client-urls=http://etcd:2379",
		"-listen-client-urls", "http://0.0.0.0:2379",
		"--data-dir", "/etcd",
	}
}

// minioRunArgs 构造 minio 容器 docker run 参数（纯函数）。
// 在 base-net 上别名 minio，固定 access/secret=minioadmin（v1 无鉴权要求，内部网络）。
func minioRunArgs(base string) []string {
	return []string{
		"run", "-d", "--name", milvusMinioName(base),
		"--network", milvusNetName(base), "--network-alias", "minio",
		"--restart", "unless-stopped",
		"-e", "MINIO_ACCESS_KEY=minioadmin",
		"-e", "MINIO_SECRET_KEY=minioadmin",
		minioImage, "minio", "server", "/minio_data",
	}
}

// milvusRunArgs 构造 milvus 容器 docker run 参数（纯函数）。
// 经 ETCD_ENDPOINTS/MINIO_ADDRESS 解析 sidecar；仅 publish gRPC 到宿主 port。
func milvusRunArgs(base string, port int) []string {
	return []string{
		"run", "-d", "--name", base,
		"--network", milvusNetName(base), "--network-alias", "milvus",
		"--restart", "unless-stopped",
		"-e", fmt.Sprintf("ETCD_ENDPOINTS=etcd:%d", etcdInternalPort),
		"-e", fmt.Sprintf("MINIO_ADDRESS=minio:%d", minioInternalPort),
		"-p", fmt.Sprintf("%d:%d", port, milvusGrpcPort),
		milvusImage, "milvus", "run", "standalone",
	}
}

// milvusProbeArgs 构造就绪探针参数：--rm 临时 alpine 在 base-net 上 wget milvus healthz。
func milvusProbeArgs(base string) []string {
	return []string{
		"run", "--rm", "--network", milvusNetName(base), readyAlpineImage,
		"wget", "-qO-", "-T", "3", fmt.Sprintf("http://milvus:%d/healthz", milvusHealthPort),
	}
}

// RunMilvusStack 起 dedicated milvus 栈：建网络 → etcd → minio → milvus。
// 任一 run 失败：best-effort rm 已起容器 + 网络，返错（由 supplyDedicated 兜底再 RmMilvusStack）。
func (osDocker) RunMilvusStack(ctx context.Context, base string, port int) error {
	if out, err := runDockerCmd(ctx, "network", "create", milvusNetName(base)); err != nil {
		return fmt.Errorf("docker network create: %w: %s", err, out)
	}
	for _, args := range [][]string{etcdRunArgs(base), minioRunArgs(base), milvusRunArgs(base, port)} {
		if out, err := runDockerCmd(ctx, args...); err != nil {
			_, _ = runDockerCmd(ctx, "rm", "-f", base, milvusEtcdName(base), milvusMinioName(base)) // best-effort 清半成品
			_, _ = runDockerCmd(ctx, "network", "rm", milvusNetName(base))
			return fmt.Errorf("docker run milvus 栈: %w: %s", err, out)
		}
	}
	return nil
}

// MilvusReady 在专属网络上轮询 milvus /healthz 直至就绪或超时。
// 经 docker socket 起临时 alpine 探针，不受 backend↔milvus 网络可达性影响。
func (osDocker) MilvusReady(ctx context.Context, base string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		out, err := runDockerCmd(ctx, milvusProbeArgs(base)...)
		if err == nil && strings.TrimSpace(out) != "" {
			return nil
		}
		lastErr = fmt.Errorf("milvus 未就绪: %v: %s", err, strings.TrimSpace(out))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("milvus 就绪超时 %v", timeout)
	}
	return lastErr
}

// RmMilvusStack 强删 milvus 栈：rm 三容器 + 删网络（best-effort）。
func (osDocker) RmMilvusStack(ctx context.Context, base string) error {
	_, _ = runDockerCmd(ctx, "rm", "-f", base, milvusEtcdName(base), milvusMinioName(base))
	_, err := runDockerCmd(ctx, "network", "rm", milvusNetName(base))
	return err
}

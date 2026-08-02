package mwsupply

import (
	"testing"
)

func TestRedisRunArgs(t *testing.T) {
	got := redisRunArgs("mwredis-abc", "s3cr3t", 9631)
	want := []string{
		"run", "-d", "--name", "mwredis-abc",
		"-e", "REDIS_PASSWORD=s3cr3t",
		"-p", "9631:6379",
		"--restart", "unless-stopped",
		"redis:7-alpine",
		"redis-server", "--requirepass", "s3cr3t",
	}
	if len(got) != len(want) {
		t.Fatalf("参数数应 %d，得 %d: %v", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("args[%d] 想 %q 得 %q", i, w, got[i])
		}
	}
}

// TestDockerUsedPortsParse 校验 docker ps 端口输出解析（提取宿主 publish 端口）。
func TestDockerUsedPortsParse(t *testing.T) {
	used := parsePortsOutput(`0.0.0.0:9631->6379/tcp
0.0.0.0:9500->5432/tcp, :::9500->5432/tcp
`)
	if _, ok := used[9631]; !ok {
		t.Errorf("应含 9631，得 %v", used)
	}
	if _, ok := used[9500]; !ok {
		t.Errorf("应含 9500，得 %v", used)
	}
	if len(used) != 2 {
		t.Errorf("应 2 端口，得 %d: %v", len(used), used)
	}
}

func TestMilvusRunArgs(t *testing.T) {
	got := milvusRunArgs("mwmilvus-abc", 9701)
	want := []string{
		"run", "-d", "--name", "mwmilvus-abc",
		"--network", "mwmilvus-abc-net", "--network-alias", "milvus",
		"--restart", "unless-stopped",
		"-e", "ETCD_ENDPOINTS=etcd:2379",
		"-e", "MINIO_ADDRESS=minio:9000",
		"-p", "9701:19530",
		"milvusdb/milvus:v2.6.15",
		"milvus", "run", "standalone",
	}
	if len(got) != len(want) {
		t.Fatalf("milvus args 数应 %d，得 %d: %v", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("milvus args[%d] 想 %q 得 %q", i, w, got[i])
		}
	}
}

func TestEtcdRunArgs(t *testing.T) {
	got := etcdRunArgs("mwmilvus-abc")
	for _, w := range []string{"--name", "mwmilvus-abc-etcd", "--network", "mwmilvus-abc-net", "--network-alias", "etcd", etcdImage, "-advertise-client-urls=http://etcd:2379", "--data-dir", "/etcd"} {
		if !contains(got, w) {
			t.Errorf("etcd args 应含 %q，得 %v", w, got)
		}
	}
}

func TestMinioRunArgs(t *testing.T) {
	got := minioRunArgs("mwmilvus-abc")
	for _, w := range []string{"--name", "mwmilvus-abc-minio", "--network", "mwmilvus-abc-net", "--network-alias", "minio", "-e", "MINIO_ACCESS_KEY=minioadmin", "-e", "MINIO_SECRET_KEY=minioadmin", minioImage, "server", "/minio_data"} {
		if !contains(got, w) {
			t.Errorf("minio args 应含 %q，得 %v", w, got)
		}
	}
}

func TestMilvusProbeArgs(t *testing.T) {
	got := milvusProbeArgs("mwmilvus-abc")
	for _, w := range []string{"--rm", "--network", "mwmilvus-abc-net", readyAlpineImage, "wget", "-qO-", "-T", "3", "http://milvus:9091/healthz"} {
		if !contains(got, w) {
			t.Errorf("probe args 应含 %q，得 %v", w, got)
		}
	}
}

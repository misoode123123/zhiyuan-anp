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

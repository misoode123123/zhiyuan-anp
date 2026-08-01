package appdeploy

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyze_LanguageBuildPorts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module demo\n\ngo 1.24\n")
	writeFile(t, dir, "Dockerfile", "FROM golang:alpine\nEXPOSE 8080\nCMD [\"/app\"]\n")
	a, err := Analyze(dir)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if a.Language != "go" {
		t.Errorf("Language=%q want go", a.Language)
	}
	if !a.Build.Dockerfile {
		t.Errorf("Build.Dockerfile=false want true")
	}
	got := append([]int(nil), a.Ports.Expose...)
	sort.Ints(got)
	if len(got) != 1 || got[0] != 8080 {
		t.Errorf("Ports.Expose=%v want [8080]", got)
	}
}

func TestAnalyze_DepsAndNetwork(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module x\n")
	writeFile(t, dir, "config.yaml", "redis:\n  addr: \"127.0.0.1:6379\"\n")
	writeFile(t, dir, "docker-compose.yml", "services:\n  bot:\n    image: bot\n  redis:\n    image: redis\nnetworks:\n  default:\nnetwork_mode: host\n")
	a, err := Analyze(dir)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	foundRedis := false
	for _, d := range a.Deps {
		if d.Kind == "redis" {
			foundRedis = true
			if d.Addr != "127.0.0.1:6379" {
				t.Errorf("redis addr=%q want 127.0.0.1:6379", d.Addr)
			}
		}
	}
	if !foundRedis {
		t.Errorf("Deps 缺 redis: %+v", a.Deps)
	}
	if !a.Network.HostModeRequired {
		t.Errorf("Network.HostModeRequired=false want true")
	}
	if !containsStr(a.Build.ComposeServices, "redis") {
		t.Errorf("ComposeServices 缺 redis: %+v", a.Build.ComposeServices)
	}
}

func containsStr(s []string, v string) bool {
	for _, x := range s {
		if strings.Contains(x, v) {
			return true
		}
	}
	return false
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestAnalyze_Golden_KeFuBot(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module yxt_eino_v2\n\ngo 1.24\n")
	writeFile(t, dir, "Dockerfile", "FROM golang:alpine AS build\nWORKDIR /src\nCOPY . .\nRUN go build -o /out/bot ./cmd/bot/\nFROM alpine:3.20\nCOPY --from=build /out/bot /app/bot\nEXPOSE 9090 9091\nENTRYPOINT [\"/app/bot\"]\n")
	writeFile(t, dir, "docker-compose.yml", "services:\n  bot:\n    image: yxt-bot:latest\n    network_mode: host\n  redis:\n    image: redis:7-alpine\n  milvus:\n    image: milvusdb/milvus\n  postgres:\n    image: postgres:16-alpine\n")
	writeFile(t, dir, "config.yaml", "# yxt_eino_v2\nwecom:\n  bot_id: x\nredis:\n  addr: \"127.0.0.1:6379\"\nkb:\n  milvus:\n    address: \"127.0.0.1:19530\"\npgsql:\n  host: \"127.0.0.1\"\n  port: 5432\n  database: \"yxt_wecom_bot\"\n")
	a, err := Analyze(dir)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if a.Language != "go" {
		t.Errorf("Language=%q want go", a.Language)
	}
	if a.AppKindGuess != "headless" {
		t.Errorf("AppKindGuess=%q want headless", a.AppKindGuess)
	}
	if !a.Network.HostModeRequired {
		t.Errorf("应检出 host 网络需求")
	}
	kinds := map[string]bool{}
	for _, d := range a.Deps {
		kinds[d.Kind] = true
	}
	for _, want := range []string{"redis", "milvus", "postgres"} {
		if !kinds[want] {
			t.Errorf("Deps 缺 %s: %+v", want, a.Deps)
		}
	}
	if !containsInt(a.Ports.Expose, 9090) || !containsInt(a.Ports.Expose, 9091) {
		t.Errorf("Ports.Expose=%v want 含 9090/9091", a.Ports.Expose)
	}
	if !containsStr(a.Mismatches, "host") {
		t.Errorf("Mismatches 应含 host 相关: %+v", a.Mismatches)
	}
}

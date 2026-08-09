package appdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAppContextPrompt 自主发起编码 session 注入的应用上下文：含应用名/形态/类型、
// 仓库结构概览、依赖声明、开发指引（指向 AGENTS.md 与 .anp/deploy.yaml）。nil 返空。
func TestAppContextPrompt(t *testing.T) {
	// nil 守卫
	if got := AppContextPrompt(nil); got != "" {
		t.Fatalf("nil 应用应返回空串，得到 %q", got)
	}

	// 构造一个带源码 + deps 的临时 repo
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main(){}\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, ".anp"), 0o755)
	os.WriteFile(filepath.Join(dir, ".anp", "deps.yaml"), []byte("services:\n  - kind: redis\n"), 0o644)

	a := &Application{
		Name:       "yxt-eino-v2",
		AppKind:    AppKindHeadless,
		DeployMode: "managed",
		Version:    7,
		Status:     "running",
		RepoDir:    dir,
	}
	got := AppContextPrompt(a)

	// 应用元信息
	for _, want := range []string{"yxt-eino-v2", "headless", "自管", "v7", "running"} {
		if !strings.Contains(got, want) {
			t.Fatalf("应用上下文应含 %q，得到 %q", want, got)
		}
	}
	// 仓库结构概览（readRepoCode 收录 main.go）
	if !strings.Contains(got, "main.go") {
		t.Fatalf("应用上下文应含仓库结构概览(main.go)，得到 %q", got)
	}
	// 依赖声明
	if !strings.Contains(got, "kind: redis") {
		t.Fatalf("应用上下文应含 .anp/deps.yaml 内容，得到 %q", got)
	}
	// 开发指引：指向 AGENTS.md 与 .anp/deploy.yaml
	if !strings.Contains(got, "AGENTS.md") || !strings.Contains(got, ".anp/deploy.yaml") {
		t.Fatalf("应用上下文应指向 AGENTS.md 与 .anp/deploy.yaml，得到 %q", got)
	}
}

// TestReadDepsSummary 不存在/损坏返回空串；存在返回去空白正文。
func TestReadDepsSummary(t *testing.T) {
	dir := t.TempDir()
	// 不存在
	if got := readDepsSummary(dir); got != "" {
		t.Fatalf("无 deps.yaml 应返回空，得到 %q", got)
	}
	// 存在
	os.MkdirAll(filepath.Join(dir, ".anp"), 0o755)
	os.WriteFile(filepath.Join(dir, ".anp", "deps.yaml"), []byte("  services: []  \n"), 0o644)
	if got := readDepsSummary(dir); got != "services: []" {
		t.Fatalf("readDepsSummary 应去首尾空白，得到 %q", got)
	}
}

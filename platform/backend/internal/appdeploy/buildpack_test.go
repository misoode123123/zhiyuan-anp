package appdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureDockerfile_GeneratesForGo(t *testing.T) {
	dir, _ := os.MkdirTemp("", "bp-go")
	defer os.RemoveAll(dir)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\ngo 1.25\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main(){}\n"), 0o644)

	gen, port, err := EnsureDockerfile(dir, 8080)
	if err != nil {
		t.Fatalf("EnsureDockerfile: %v", err)
	}
	if !gen {
		t.Fatal("无 Dockerfile 应生成")
	}
	if port != 8080 {
		t.Fatalf("go 默认端口 8080，得到 %d", port)
	}
	b, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if err != nil {
		t.Fatalf("生成的 Dockerfile 读取失败: %v", err)
	}
	if !contains(string(b), "golang:") || !contains(string(b), "EXPOSE 8080") {
		t.Fatalf("生成的 Dockerfile 不符: %s", string(b)[:120])
	}

	// 再次调用：已有 Dockerfile，不再生成
	gen2, _, _ := EnsureDockerfile(dir, 8080)
	if gen2 {
		t.Fatal("已有 Dockerfile 不应重复生成")
	}
}

func TestEnsureDockerfile_NodeType(t *testing.T) {
	dir, _ := os.MkdirTemp("", "bp-node")
	defer os.RemoveAll(dir)
	os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o644)
	if _, port, _ := EnsureDockerfile(dir, 0); port != 3000 {
		t.Fatalf("node 默认端口 3000，得到 %d", port)
	}
}

func TestEnsureDockerfile_RespectsExplicitPort(t *testing.T) {
	dir, _ := os.MkdirTemp("", "bp-node-exp")
	defer os.RemoveAll(dir)
	os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o644)
	// 显式传 8080（opencode 实际监听）→ 不应被 node 默认 3000 覆盖
	if _, port, _ := EnsureDockerfile(dir, 8080); port != 8080 {
		t.Fatalf("显式端口 8080 应保留，得到 %d", port)
	}
}

func TestEnsureDockerfile_ExistingDockerfileUntouched(t *testing.T) {
	dir, _ := os.MkdirTemp("", "bp-exist")
	defer os.RemoveAll(dir)
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM custom\n"), 0o644)
	gen, _, _ := EnsureDockerfile(dir, 8080)
	if gen {
		t.Fatal("已有自定义 Dockerfile 不应被覆盖")
	}
}

// TestDetectType 各项目类型识别（按特征文件）；空仓库兜底 static。
func TestDetectType(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"go.mod", "go"},
		{"main.go", "go"}, // 单 main.go 也算 go
		{"package.json", "node"},
		{"requirements.txt", "python"},
		{"app.py", "python"},
		{"main.py", "python"},
		{"index.html", "static"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, _ := os.MkdirTemp("", "bp-detect")
			defer os.RemoveAll(dir)
			os.WriteFile(filepath.Join(dir, c.name), []byte("x"), 0o644)
			if got := detectType(dir); got != c.want {
				t.Fatalf("detectType(%s)=%q want %q", c.name, got, c.want)
			}
		})
	}
	// 空仓库兜底 static（避免误判 go 导致 "cannot find main module"）
	t.Run("空仓库兜底 static", func(t *testing.T) {
		dir, _ := os.MkdirTemp("", "bp-empty")
		defer os.RemoveAll(dir)
		if got := detectType(dir); got != "static" {
			t.Fatalf("空仓库应兜底 static，得到 %q", got)
		}
	})
}

// TestDetectType_PriorityGoOverNode 多类型特征并存时按 switch 顺序：go 优先于 node。
func TestDetectType_PriorityGoOverNode(t *testing.T) {
	dir, _ := os.MkdirTemp("", "bp-prio")
	defer os.RemoveAll(dir)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x"), 0o644)
	os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o644)
	if got := detectType(dir); got != "go" {
		t.Fatalf("go.mod + package.json 应识别为 go，得到 %q", got)
	}
}

// TestDefaultPortForType 各类型默认端口（未知类型/空 → 8080）。
func TestDefaultPortForType(t *testing.T) {
	cases := map[string]int{
		"node":   3000,
		"static": 80,
		"go":     8080,
		"python": 8080,
		"":       8080,
		"unknown": 8080,
	}
	for t_, want := range cases {
		if got := defaultPortForType(t_); got != want {
			t.Fatalf("defaultPortForType(%q)=%d want %d", t_, got, want)
		}
	}
}

// TestDockerfileFor 各类型 Dockerfile 模板包含正确的 base image + EXPOSE 端口。
func TestDockerfileFor(t *testing.T) {
	cases := []struct {
		typ, base string
	}{
		{"go", "golang:1.25-alpine"},
		{"node", "node:20-alpine"},
		{"python", "python:3-alpine"},
		{"static", "nginx:alpine"},
		{"unknown", "busybox"},
	}
	port := 8080
	for _, c := range cases {
		got := dockerfileFor(c.typ, port)
		if !strings.Contains(got, c.base) {
			t.Fatalf("type=%q 应含 %q，得到 %s", c.typ, c.base, got[:min(80, len(got))])
		}
		// static 类型应包含 listen <port> 覆盖（不然 nginx 默认 80 容器内无人监听 port）
		if c.typ == "static" {
			if !strings.Contains(got, "listen 8080") {
				t.Fatalf("static 应覆盖 nginx listen 8080: %s", got)
			}
		} else if !strings.Contains(got, "EXPOSE 8080") {
			t.Fatalf("type=%q 应含 EXPOSE 8080", c.typ)
		}
		// 都应含自动生成头部
		if !strings.Contains(got, "由 ANP buildpack 自动生成") {
			t.Fatalf("type=%q 应含自动生成头", c.typ)
		}
	}
}

// TestDockerfileFor_PortPropagation 端口正确传入 EXPOSE。
func TestDockerfileFor_PortPropagation(t *testing.T) {
	got := dockerfileFor("node", 3000)
	if !strings.Contains(got, "EXPOSE 3000") {
		t.Fatalf("port=3000 应出现在 EXPOSE，得到 %s", got)
	}
}

// TestEnsureDockerfile_PythonAndStatic 补全另外两类（go/node 外）。
func TestEnsureDockerfile_PythonAndStatic(t *testing.T) {
	t.Run("python", func(t *testing.T) {
		dir, _ := os.MkdirTemp("", "bp-py")
		defer os.RemoveAll(dir)
		os.WriteFile(filepath.Join(dir, "app.py"), []byte("print('hi')"), 0o644)
		_, port, err := EnsureDockerfile(dir, 0)
		if err != nil {
			t.Fatalf("ensure: %v", err)
		}
		if port != 8080 {
			t.Fatalf("python 默认端口 8080，得到 %d", port)
		}
	})
	t.Run("static", func(t *testing.T) {
		dir, _ := os.MkdirTemp("", "bp-static")
		defer os.RemoveAll(dir)
		os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html/>"), 0o644)
		_, port, _ := EnsureDockerfile(dir, 0)
		if port != 80 {
			t.Fatalf("static 默认端口 80，得到 %d", port)
		}
	})
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

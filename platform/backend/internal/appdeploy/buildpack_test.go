package appdeploy

import (
	"os"
	"path/filepath"
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

func TestEnsureDockerfile_ExistingDockerfileUntouched(t *testing.T) {
	dir, _ := os.MkdirTemp("", "bp-exist")
	defer os.RemoveAll(dir)
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM custom\n"), 0o644)
	gen, _, _ := EnsureDockerfile(dir, 8080)
	if gen {
		t.Fatal("已有自定义 Dockerfile 不应被覆盖")
	}
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

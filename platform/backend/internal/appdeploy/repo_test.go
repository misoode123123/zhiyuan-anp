package appdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestManagedRepoDir 应用名 → 确定性托管仓库路径，含 sanitize（特殊字符→"-"）。
func TestManagedRepoDir(t *testing.T) {
	cases := map[string]string{
		"snake":   filepath.Join(ManagedRepoBase, "snake"),
		"MyApp":   filepath.Join(ManagedRepoBase, "myapp"), // 小写化
		"foo bar": filepath.Join(ManagedRepoBase, "foo-bar"),
		"应用中文":     filepath.Join(ManagedRepoBase, "----"), // 非 ASCII 字母数字→"-"（按 rune 计数，4 字符 = 4 dashes）
	}
	for name, want := range cases {
		got := ManagedRepoDir(name)
		if got != want {
			t.Fatalf("ManagedRepoDir(%q)=%q want %q", name, got, want)
		}
	}
	// 空名兜底 "app"
	if got := ManagedRepoDir(""); got != filepath.Join(ManagedRepoBase, "app") {
		t.Fatalf("空名应兜底 app，得到 %q", got)
	}
}

// TestSanitizeID 小写化 + 非[ a-z0-9]→"-"；空兜底 "dev"。
func TestSanitizeID(t *testing.T) {
	cases := map[string]string{
		"Alice":    "alice",
		"Bob_2024": "bob-2024", // _ 也被替换（仅 a-z0-9 通过）
		"user@x.y": "user-x-y",
		"中文":       "--", // 2 个 rune 各替换为 1 个 -
		"":         "dev", // 空兜底
		"UPPER":    "upper",
	}
	for in, want := range cases {
		if got := sanitizeID(in); got != want {
			t.Fatalf("sanitizeID(%q)=%q want %q", in, got, want)
		}
	}
}

// TestSanitizeName 小写化 + 不安全字符→"-"；空兜底 "app"。
func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"Snake":       "snake",
		"my app":      "my-app",
		"foo.bar_BAZ": "foo.bar_baz", // _ . 保留
		"":            "app",
		"   ":         "app", // trim 后空 → "app"
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Fatalf("sanitizeName(%q)=%q want %q", in, got, want)
		}
	}
}

// TestEnsureFile_幂等 文件不存在→创建；存在→不覆盖。
func TestEnsureFile(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ensure-file")
	defer os.RemoveAll(dir)

	rel := "docs/设计.md"
	ensureFile(dir, rel, "v1")
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatalf("ensureFile 后应存在: %v", err)
	}
	if string(b) != "v1" {
		t.Fatalf("首次创建内容应 v1，得到 %q", string(b))
	}
	// 二次调用：已有文件不应覆盖
	ensureFile(dir, rel, "v2-changed")
	b, _ = os.ReadFile(filepath.Join(dir, rel))
	if string(b) != "v1" {
		t.Fatalf("已有文件不应被覆盖，得到 %q", string(b))
	}
	// 多级目录自动创建
	ensureFile(dir, "a/b/c.txt", "deep")
	if _, err := os.Stat(filepath.Join(dir, "a/b/c.txt")); err != nil {
		t.Fatalf("多级目录自动创建失败: %v", err)
	}
}

// TestAppendFile 文件不存在→创建；存在→追加。
func TestAppendFile(t *testing.T) {
	dir, _ := os.MkdirTemp("", "append-file")
	defer os.RemoveAll(dir)

	rel := "docs/开发日志.md"
	appendFile(dir, rel, "line1\n")
	appendFile(dir, rel, "line2\n")
	b, _ := os.ReadFile(filepath.Join(dir, rel))
	got := string(b)
	if !strings.Contains(got, "line1") || !strings.Contains(got, "line2") {
		t.Fatalf("append 后应含两行，得到 %q", got)
	}
	if strings.Index(got, "line1") > strings.Index(got, "line2") {
		t.Fatalf("追加顺序错: %q", got)
	}
}

// TestScanDocs 扫描应排除 .git/node_modules 等并跳隐藏文件，返回相对 repo 的正斜杠路径。
func TestScanDocs(t *testing.T) {
	dir, _ := os.MkdirTemp("", "scan-docs")
	defer os.RemoveAll(dir)

	// 正常文件（应收录）
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("r"), 0o644)
	os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	os.WriteFile(filepath.Join(dir, "src/main.go"), []byte("x"), 0o644)
	// 排除目录
	os.MkdirAll(filepath.Join(dir, ".git/objects"), 0o755)
	os.WriteFile(filepath.Join(dir, ".git/objects/abc"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(dir, "node_modules/pkg"), 0o755)
	os.WriteFile(filepath.Join(dir, "node_modules/pkg/index.js"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(dir, "dist"), 0o755)
	os.WriteFile(filepath.Join(dir, "dist/bundle.js"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(dir, "__pycache__"), 0o755)
	os.WriteFile(filepath.Join(dir, "__pycache__/x.pyc"), []byte("x"), 0o644)
	// 隐藏文件（应跳过）
	os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET"), 0o644)

	docs, err := ScanDocs(dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := map[string]bool{"README.md": true, "src/main.go": true}
	got := map[string]bool{}
	for _, d := range docs {
		got[d.Path] = true
		// 路径必须是正斜杠（即使 Windows）
		if strings.Contains(d.Path, "\\") {
			t.Fatalf("路径应正斜杠，得到 %q", d.Path)
		}
		// Name 应等于 filepath.Base
		if d.Name != filepath.Base(d.Path) {
			t.Fatalf("Name=%q 不等于 Base(Path)=%q", d.Name, filepath.Base(d.Path))
		}
	}
	for k := range want {
		if !got[k] {
			t.Fatalf("应收录 %q，实际 %v", k, got)
		}
	}
	// 排除项不应出现
	for _, banned := range []string{".env", ".git/objects/abc", "node_modules/pkg/index.js", "dist/bundle.js", "__pycache__/x.pyc"} {
		if got[banned] {
			t.Fatalf("不应收录排除项 %q", banned)
		}
	}
}

// TestScanDocs_emptyRepo 空仓库返回空切片不报错。
func TestScanDocs_emptyRepo(t *testing.T) {
	dir, _ := os.MkdirTemp("", "scan-empty")
	defer os.RemoveAll(dir)

	docs, err := ScanDocs(dir)
	if err != nil {
		t.Fatalf("scan empty: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("空仓库应返回 0 项，得到 %d", len(docs))
	}
}

// TestReadRepoFile 正常读取 + path traversal 拒绝。
func TestReadRepoFile(t *testing.T) {
	dir, _ := os.MkdirTemp("", "read-repo")
	defer os.RemoveAll(dir)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub/a.txt"), []byte("hello"), 0o644)

	// 正常读
	got, err := ReadRepoFile(dir, "main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if got != "package main" {
		t.Fatalf("内容不匹配: %q", got)
	}
	// 子目录文件
	got, _ = ReadRepoFile(dir, "sub/a.txt")
	if got != "hello" {
		t.Fatalf("子目录文件内容: %q", got)
	}
	// 不存在的文件 → 报错
	if _, err := ReadRepoFile(dir, "ghost.go"); err == nil {
		t.Fatal("不存在文件应报错")
	}
}

// TestReadRepoFile_PathTraversal ../ 越权访问必须被拒。
func TestReadRepoFile_PathTraversal(t *testing.T) {
	dir, _ := os.MkdirTemp("", "read-trav")
	defer os.RemoveAll(dir)
	// 在 dir 父目录放一个 secret
	secretPath := filepath.Join(filepath.Dir(dir), "secret.txt")
	os.WriteFile(secretPath, []byte("TOP-SECRET"), 0o644)
	defer os.Remove(secretPath)

	if _, err := ReadRepoFile(dir, "../secret.txt"); err == nil {
		t.Fatal("../ 越权访问必须被拒")
	}
	// 绝对路径也不应越权
	if _, err := ReadRepoFile(dir, secretPath); err == nil {
		t.Fatal("绝对路径越权访问必须被拒")
	}
}

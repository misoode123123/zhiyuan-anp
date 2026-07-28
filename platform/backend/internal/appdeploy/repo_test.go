package appdeploy

import (
	"archive/zip"
	"bytes"
	"context"
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
		"应用中文":    filepath.Join(ManagedRepoBase, "----"), // 非 ASCII 字母数字→"-"（按 rune 计数，4 字符 = 4 dashes）
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
		"中文":       "--",  // 2 个 rune 各替换为 1 个 -
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

// withTempRepoBase 临时把 ManagedRepoBase 指向 t.TempDir()，返回还原函数。
func withTempRepoBase(t *testing.T) func() {
	t.Helper()
	old := ManagedRepoBase
	base := t.TempDir()
	ManagedRepoBase = base
	return func() { ManagedRepoBase = old }
}

// makeLocalGitRepo 在 dir 建一个含一个文件的本地 git 仓（作 clone 源）。
func makeLocalGitRepo(t *testing.T, dir string) {
	t.Helper()
	_ = os.MkdirAll(dir, 0755)
	_ = os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi"), 0644)
	runGit(context.Background(), dir, "init", "-q")
	runGit(context.Background(), dir, "config", "user.email", "t@t")
	runGit(context.Background(), dir, "config", "user.name", "t")
	runGit(context.Background(), dir, "add", "-A")
	runGit(context.Background(), dir, "commit", "-q", "-m", "init")
}

// TestImportFromGit_LocalClone clone 本地源仓到 ManagedRepoDir(name)，含 .git 与文件。
func TestImportFromGit_LocalClone(t *testing.T) {
	restore := withTempRepoBase(t)
	defer restore()
	src := filepath.Join(t.TempDir(), "src")
	makeLocalGitRepo(t, src)

	repoDir, err := ImportFromGit(context.Background(), "legacy", src, "")
	if err != nil {
		t.Fatalf("ImportFromGit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err != nil {
		t.Fatalf("clone 后应有 .git: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(repoDir, "hello.txt"))
	if err != nil || string(b) != "hi" {
		t.Fatalf("clone 文件内容应 hi，得到 %q err=%v", string(b), err)
	}
}

// TestImportFromGit_TargetExists 目标已存在则拒绝（防覆盖）。
func TestImportFromGit_TargetExists(t *testing.T) {
	restore := withTempRepoBase(t)
	defer restore()
	name := "dup"
	_ = os.MkdirAll(ManagedRepoDir(name), 0755)
	_, err := ImportFromGit(context.Background(), name, "/nonexistent/src", "")
	if err == nil {
		t.Fatalf("目标已存在应报错")
	}
}

// TestBuildCloneArgs_Token HTTPS+token 时首参为 -c extraHeader（注入认证，不落 URL/config）。
func TestBuildCloneArgs_Token(t *testing.T) {
	args := buildCloneArgs("https://gitlab.com/x/y.git", "tok123", "/data/repos/y")
	// 期望: ["-c", "http.extraHeader=Authorization: Bearer tok123", "clone", "--progress", url, target]
	if len(args) < 2 || args[0] != "-c" || !strings.Contains(args[1], "Bearer tok123") {
		t.Fatalf("HTTPS+token 应注入 extraHeader，得到 %v", args)
	}
}

// TestBuildCloneArgs_SSH git@ 开头时不注入 extraHeader（走部署机 key）。
func TestBuildCloneArgs_SSH(t *testing.T) {
	args := buildCloneArgs("git@github.com:x/y.git", "tok123", "/data/repos/y")
	for _, a := range args {
		if strings.Contains(a, "extraHeader") {
			t.Fatalf("SSH 仓不应注入 extraHeader，得到 %v", args)
		}
	}
}

// writeZip 构造一个内存 zip（files: name→content），返回 bytes 与大小。
func writeZip(t *testing.T, files map[string]string) ([]byte, int64) {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		_, _ = f.Write([]byte(content))
	}
	_ = w.Close()
	return buf.Bytes(), int64(buf.Len())
}

// TestImportFromZip_Happy 解压 zip + 无 .git 时 git init + 初始提交。
func TestImportFromZip_Happy(t *testing.T) {
	restore := withTempRepoBase(t)
	defer restore()
	data, size := writeZip(t, map[string]string{"a.txt": "A", "sub/b.txt": "B"})

	repoDir, err := ImportFromZip(context.Background(), "zapp", bytes.NewReader(data), size)
	if err != nil {
		t.Fatalf("ImportFromZip: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(repoDir, "a.txt"))
	if string(b) != "A" {
		t.Fatalf("a.txt 应 A，得到 %q", string(b))
	}
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err != nil {
		t.Fatalf("无 .git 的 zip 应 git init: %v", err)
	}
}

// TestImportFromZip_Slip 含 ../ 路径的 entry 须被拒（防 zip slip）。
func TestImportFromZip_Slip(t *testing.T) {
	restore := withTempRepoBase(t)
	defer restore()
	data, size := writeZip(t, map[string]string{"../evil.txt": "x"})

	_, err := ImportFromZip(context.Background(), "slipapp", bytes.NewReader(data), size)
	if err == nil {
		t.Fatalf("zip slip 须被拒")
	}
	// 确认未逃逸到父目录
	if _, err := os.Stat(filepath.Join(ManagedRepoBase, "evil.txt")); err == nil {
		t.Fatalf("zip slip 文件不应逃逸到父目录")
	}
}

// TestImportFromZip_TooLarge 超过 MaxZipSize 须被拒（防 bomb 前置）。
func TestImportFromZip_TooLarge(t *testing.T) {
	restore := withTempRepoBase(t)
	defer restore()
	_, err := ImportFromZip(context.Background(), "bigapp", bytes.NewReader([]byte("x")), MaxZipSize+1)
	if err == nil {
		t.Fatalf("超过 MaxZipSize 须被拒")
	}
}

// TestImportFromZip_Bomb zip bomb：头撒谎（压缩后极小）实际解压超限须按实际字节拦截。
// 缩小 MaxZipSize 避免分配 500MB（验"实际字节累计"逻辑，不验具体阈值）；writeZip 用 deflate
// 压缩重复 "a"，故 zip bytes 极小（前置 size 检查放行），而 copyZipFile 返回的 actual 字节超限 → 拦。
// 这正覆盖 C4 的 lying-header 绕累计上限攻击面。
func TestImportFromZip_Bomb(t *testing.T) {
	restore := withTempRepoBase(t)
	defer restore()
	// 缩小上限到 1MB：避免分配 500MB 内存/IO；验证按 actual 字节累计的闭环逻辑
	old := MaxZipSize
	MaxZipSize = 1 * 1024 * 1024
	defer func() { MaxZipSize = old }()

	// 单文件内容 > 1MB（deflate 压缩重复 "a" 后 zip bytes 极小——正是 bomb 形态）
	data, size := writeZip(t, map[string]string{
		"bomb.txt": strings.Repeat("a", int(MaxZipSize)+100),
	})
	// 前置 size 检查应放行（压缩后远小于 1MB），否则测不到实际字节路径
	if size > MaxZipSize {
		t.Fatalf("前置 zip bytes=%d 应小于缩小的 MaxZipSize=%d 以测实际字节路径", size, MaxZipSize)
	}

	_, err := ImportFromZip(context.Background(), "bombapp", bytes.NewReader(data), size)
	if err == nil {
		t.Fatalf("zip bomb（实际解压字节超限）须被拒")
	}
	if !strings.Contains(err.Error(), "超限") && !strings.Contains(err.Error(), "bomb") {
		t.Fatalf("应提示 超限/bomb，得到 %q", err.Error())
	}
	// 拒绝后应清理目标目录（不留半成品）
	if _, err := os.Stat(ManagedRepoDir("bombapp")); err == nil {
		t.Fatalf("bomb 拒绝后应清理目标目录")
	}
}

// TestImportFromDir_Copy 纯目录复制 + git init（源无 .git）。
func TestImportFromDir_Copy(t *testing.T) {
	restore := withTempRepoBase(t)
	defer restore()
	// 临时把白名单加上 t.TempDir() 派生的根
	srcRoot := t.TempDir()
	oldRoots := AllowedDirRoots
	AllowedDirRoots = []string{srcRoot + "/"}
	defer func() { AllowedDirRoots = oldRoots }()

	src := filepath.Join(srcRoot, "proj")
	_ = os.MkdirAll(src, 0755)
	_ = os.WriteFile(filepath.Join(src, "main.go"), []byte("package main"), 0644)

	repoDir, err := ImportFromDir(context.Background(), "fromdir", src)
	if err != nil {
		t.Fatalf("ImportFromDir: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(repoDir, "main.go"))
	if string(b) != "package main" {
		t.Fatalf("main.go 内容不符: %q", string(b))
	}
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err != nil {
		t.Fatalf("无 .git 源应 git init: %v", err)
	}
}

// TestImportFromDir_GitClone 源是 git 仓时本地 clone（保留历史）。
func TestImportFromDir_GitClone(t *testing.T) {
	restore := withTempRepoBase(t)
	defer restore()
	srcRoot := t.TempDir()
	oldRoots := AllowedDirRoots
	AllowedDirRoots = []string{srcRoot + "/"}
	defer func() { AllowedDirRoots = oldRoots }()

	src := filepath.Join(srcRoot, "grepo")
	makeLocalGitRepo(t, src)

	repoDir, err := ImportFromDir(context.Background(), "fromgit", src)
	if err != nil {
		t.Fatalf("ImportFromDir git: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(repoDir, "hello.txt"))
	if string(b) != "hi" {
		t.Fatalf("git clone 内容不符: %q", string(b))
	}
}

// TestImportFromDir_Traversal 非白名单路径须被拒。
func TestImportFromDir_Traversal(t *testing.T) {
	restore := withTempRepoBase(t)
	defer restore()
	_, err := ImportFromDir(context.Background(), "evil", "/etc/passwd")
	if err == nil {
		t.Fatalf("非白名单路径须被拒")
	}

	// 真正的遍历攻击：在白名单根下开始，用 ../ 逃逸出去。
	// 用字符串拼接构造（不用 filepath.Join，避免 Join 折叠 ..），确保 Clean 前是真逃逸路径，
	// 验证 isUnderAllowedRoot 的 Clean+HasPrefix+ToSlash 能拦截。
	srcRoot := t.TempDir()
	oldRoots := AllowedDirRoots
	AllowedDirRoots = []string{srcRoot + "/"}
	defer func() { AllowedDirRoots = oldRoots }()

	traverse := srcRoot + "/proj/../../etc/passwd"
	if _, err := ImportFromDir(context.Background(), "evil2", traverse); err == nil {
		t.Fatalf("../ 逃逸路径须被拒: %s", traverse)
	}
}

// TestStripNestedGit 递归删子目录 .git，保留根 .git 与普通文件。
func TestStripNestedGit(t *testing.T) {
	dir, _ := os.MkdirTemp("", "strip-nested")
	defer os.RemoveAll(dir)

	// 根 .git + 普通文件 + 子目录嵌套 .git（含 objects）
	os.MkdirAll(filepath.Join(dir, ".git", "objects"), 0o755)
	os.WriteFile(filepath.Join(dir, ".git", "objects", "abc"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("r"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub", ".git", "refs"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", ".git", "refs", "x"), []byte("y"), 0o644)
	os.WriteFile(filepath.Join(dir, "sub", "code.go"), []byte("package sub"), 0o644)
	os.MkdirAll(filepath.Join(dir, "deep", "nested", ".git"), 0o755)
	os.WriteFile(filepath.Join(dir, "deep", "nested", ".git", "HEAD"), []byte("ref"), 0o644)

	stripNestedGit(dir)

	// 根 .git 保留
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("根 .git 应保留: %v", err)
	}
	// 子目录 .git 全删（含深层）
	for _, p := range []string{"sub/.git", "deep/nested/.git"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err == nil {
			t.Fatalf("子目录 %s 的 .git 应被删除", p)
		}
	}
	// 普通文件不受影响
	if b, _ := os.ReadFile(filepath.Join(dir, "sub", "code.go")); string(b) != "package sub" {
		t.Fatalf("普通文件内容应不变，得到 %q", string(b))
	}
}

// TestImportFromZip_NestedGit zip 含子目录 .git（gitlink）时，导入后内容被主仓跟踪（非空）。
// 复现 ncc_deploy 场景：嵌套 .git 导致 worktree checkout 子目录为空。
func TestImportFromZip_NestedGit(t *testing.T) {
	restore := withTempRepoBase(t)
	defer restore()
	// zip 含根文件 + 一个带 .git 的子目录（模拟从 GitHub clone 的项目作为子目录上传）
	data, size := writeZip(t, map[string]string{
		"Dockerfile":      "FROM node",
		"app/server.js":   "console.log(1)",
		"app/.git/HEAD":   "ref: refs/heads/main",
		"app/.git/config": "[core]",
	})

	repoDir, err := ImportFromZip(context.Background(), "nested", bytes.NewReader(data), size)
	if err != nil {
		t.Fatalf("ImportFromZip nested: %v", err)
	}
	// 子目录 .git 应被清除
	if _, err := os.Stat(filepath.Join(repoDir, "app", ".git")); err == nil {
		t.Fatalf("导入后子目录 .git 应被清除（否则主仓记成 gitlink）")
	}
	// 子目录内容应被主仓 git 跟踪（git ls-files 含 app/server.js）
	out, _ := runGit(context.Background(), repoDir, "ls-files", "app/")
	if !strings.Contains(out, "app/server.js") {
		t.Fatalf("嵌套目录内容应被主仓跟踪，ls-files=%q", out)
	}
}

// TestStatusFiles 工作区改动文件级列表：修改→M、新增未跟踪→U、中文路径正斜杠。
func TestStatusFiles(t *testing.T) {
	dir := t.TempDir()
	makeLocalGitRepo(t, dir) // init + 提交 hello.txt="hi"
	// 修改已跟踪文件
	_ = os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("changed"), 0o644)
	// 新增未跟踪
	_ = os.WriteFile(filepath.Join(dir, "new.txt"), []byte("n"), 0o644)
	// 中文路径未跟踪
	_ = os.MkdirAll(filepath.Join(dir, "中文目录"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "中文目录", "文件.md"), []byte("c"), 0o644)

	changes, err := StatusFiles(context.Background(), dir)
	if err != nil {
		t.Fatalf("StatusFiles: %v", err)
	}
	got := map[string]string{}
	for _, c := range changes {
		got[c.Path] = c.Status
	}
	if got["hello.txt"] != "M" {
		t.Fatalf("hello.txt 应 M，得到 %q", got["hello.txt"])
	}
	if got["new.txt"] != "U" {
		t.Fatalf("new.txt 应 U，得到 %q", got["new.txt"])
	}
	if got["中文目录/文件.md"] != "U" {
		t.Fatalf("中文未跟踪文件应 U 且路径正斜杠，得到 %q", got["中文目录/文件.md"])
	}
}

// TestCommitFiles 某次提交改动的文件列表：首提交含 hello.txt(A)，二提交含 a.txt(A)+hello.txt(M)。
func TestCommitFiles(t *testing.T) {
	dir := t.TempDir()
	makeLocalGitRepo(t, dir) // commit1: hello.txt
	// commit2: 新增 a.txt + 修改 hello.txt
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("changed"), 0o644)
	runGit(context.Background(), dir, "add", "-A")
	runGit(context.Background(), dir, "commit", "-q", "-m", "two")
	// 取两次提交的 sha（log 按 new→old 顺序）
	logOut, _ := runGit(context.Background(), dir, "log", "--pretty=%h|%s")
	lines := strings.Split(strings.TrimSpace(logOut), "\n")
	if len(lines) < 2 {
		t.Fatalf("应至少 2 条提交，log=%q", logOut)
	}
	shaTwo := strings.SplitN(lines[0], "|", 2)[0]
	shaOne := strings.SplitN(lines[1], "|", 2)[0]

	// 首提交：hello.txt 新增
	files1, err := CommitFiles(context.Background(), dir, shaOne)
	if err != nil {
		t.Fatalf("CommitFiles c1: %v", err)
	}
	if len(files1) != 1 || files1[0].Path != "hello.txt" || files1[0].Status != "A" {
		t.Fatalf("commit1 应 hello.txt(A)，得到 %+v", files1)
	}
	// 二提交：a.txt(A) + hello.txt(M)
	files2, _ := CommitFiles(context.Background(), dir, shaTwo)
	got := map[string]string{}
	for _, f := range files2 {
		got[f.Path] = f.Status
	}
	if got["a.txt"] != "A" || got["hello.txt"] != "M" {
		t.Fatalf("commit2 应 a.txt=A hello.txt=M，得到 %+v", got)
	}
	// 空 sha 返回 nil 不报错
	if f, err := CommitFiles(context.Background(), dir, ""); err != nil || f != nil {
		t.Fatalf("空 sha 应返回 nil 无错，得到 %v err=%v", f, err)
	}
}

// TestFileDiff 工作区 diff（无 sha）+ 历史提交 diff（带 sha）。
func TestFileDiff(t *testing.T) {
	dir := t.TempDir()
	makeLocalGitRepo(t, dir) // hello.txt="hi" 已提交
	// 修改 → 工作区 diff 应含 +changed
	_ = os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("changed"), 0o644)
	d, err := FileDiff(context.Background(), dir, "hello.txt", "")
	if err != nil {
		t.Fatalf("FileDiff worktree: %v", err)
	}
	if !strings.Contains(d, "-hi") || !strings.Contains(d, "+changed") {
		t.Fatalf("工作区 diff 应含 -hi/+changed，得到 %q", d)
	}
	// 路径越权必须拒（防 ../ 逃逸）
	if _, err := FileDiff(context.Background(), dir, "../escape.txt", ""); err == nil {
		t.Fatal("../ 越权路径必须被拒")
	}

	// 历史提交 diff：取首提交 sha，对 hello.txt 查应含 +hi
	logOut, _ := runGit(context.Background(), dir, "log", "--pretty=%h")
	sha := strings.TrimSpace(logOut)
	if sha == "" {
		t.Fatal("取不到提交 sha")
	}
	// 首提交无父：diff sha^..sha 会失败，须降级 git show
	d2, err := FileDiff(context.Background(), dir, "hello.txt", sha)
	if err != nil {
		t.Fatalf("FileDiff commit(首提交降级): %v", err)
	}
	if !strings.Contains(d2, "+hi") {
		t.Fatalf("首提交 diff 应含 +hi，得到 %q", d2)
	}
}

// TestLog_Author Log 返回的 CommitInfo 含作者字段。
func TestLog_Author(t *testing.T) {
	dir := t.TempDir()
	makeLocalGitRepo(t, dir) // author="t"（makeLocalGitRepo 设 user.name=t）
	list, err := Log(context.Background(), dir, 5)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("应至少 1 条提交")
	}
	c := list[0]
	if c.Author != "t" {
		t.Fatalf("Author 应为 t，得到 %q", c.Author)
	}
	if c.SHA == "" || c.Message == "" || c.Date == "" {
		t.Fatalf("SHA/Message/Date 不应为空，得到 %+v", c)
	}
}

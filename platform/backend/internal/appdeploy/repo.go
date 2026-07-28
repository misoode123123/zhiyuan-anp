package appdeploy

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// 应用代码仓库托管模型：每个应用在 /data/repos/<应用名> 拥有一个 git 仓库，
// opencode 编码即提交到此；版本 = commit；发布构建 HEAD。
// "应用代码在哪" 由此明确：app.repo_dir 指向其托管 git 仓库，平台全权管理。

// ManagedRepoBase 托管仓库根目录（后端容器内路径，挂载自宿主 /opt/anp/data/repos）。
// var（非 const）：测试可用 t.TempDir() 覆盖，避免本机无 /data/repos。
var ManagedRepoBase = "/data/repos"

// ManagedRepoDir 应用名的确定性托管仓库路径。
func ManagedRepoDir(appName string) string {
	return filepath.Join(ManagedRepoBase, sanitizeName(appName))
}

// EnsureRepo 确保仓库存在并完成 git init（幂等）。返回仓库路径。
func EnsureRepo(ctx context.Context, repoDir string) error {
	if _, err := runGit(ctx, repoDir, "init", "-q"); err != nil {
		// init 前需目录存在
		if e := runMkdir(ctx, repoDir); e != nil {
			return e
		}
		if _, err := runGit(ctx, repoDir, "init", "-q"); err != nil {
			return err
		}
	}
	// 设默认身份（避免 commit 失败）
	_, _ = runGit(ctx, repoDir, "config", "user.email", "anp@platform")
	_, _ = runGit(ctx, repoDir, "config", "user.name", "ANP Platform")
	// 允许初始提交（无 -u 主分支名差异）
	// 标准开发结构:README + docs/(需求/设计/开发日志),幂等不覆盖已有内容
	appName := filepath.Base(repoDir)
	ensureFile(repoDir, "README.md", "# "+appName+"\n\n> 项目说明:用途、技术栈、运行方式。\n\n## 结构\n- 代码文件\n- `docs/` — 开发文档(需求/设计/开发日志)\n")
	ensureFile(repoDir, "docs/需求.md", "# 需求\n\n> 本应用需求(与平台需求关联)。\n\n")
	ensureFile(repoDir, "docs/设计.md", "# 设计\n\n> 架构 / 模块 / 接口设计。\n\n")
	ensureFile(repoDir, "docs/开发日志.md", "# 开发日志\n\n> 每次变更的记录(登记变更时自动追加)。\n\n")
	return nil
}

// ensureFile 若文件不存在则创建(含目录),幂等不覆盖已有内容。
func ensureFile(repoDir, rel, content string) {
	abs := filepath.Join(repoDir, rel)
	if _, err := os.Stat(abs); err == nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(abs), 0755)
	_ = os.WriteFile(abs, []byte(content), 0644)
}

// appendFile 追加内容到文件(不存在则创建),把变更/需求记录写到 repo docs/,随代码版本管理。
func appendFile(repoDir, rel, content string) {
	abs := filepath.Join(repoDir, rel)
	_ = os.MkdirAll(filepath.Dir(abs), 0755)
	f, err := os.OpenFile(abs, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(content)
}

// Commit 把仓库工作区全部变更提交（编码产出落地为版本）。
func Commit(ctx context.Context, repoDir, message string) (string, error) {
	if _, err := runGit(ctx, repoDir, "add", "-A"); err != nil {
		return "", err
	}
	// 无变更时 commit 会失败，先判断
	out, _ := runGit(ctx, repoDir, "status", "--porcelain")
	if strings.TrimSpace(out) == "" {
		return "(无变更)", nil
	}
	return runGit(ctx, repoDir, "commit", "-q", "-m", message)
}

// CountUncommitted 统计工作区未提交的文件数（git status --porcelain 的行数）。
// 用于「构建部署」前检测开发者 dev-<user> 分支是否有未提交改动（>0 即需提示提交后再部署）。
func CountUncommitted(ctx context.Context, repoDir string) (int, error) {
	out, err := runGit(ctx, repoDir, "status", "--porcelain")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n, nil
}

// Checkout 切到指定 commit（版本化部署/回滚）。返回原分支名以便恢复。
func Checkout(ctx context.Context, repoDir, sha string) (string, error) {
	if sha == "" {
		return "", nil
	}
	// 记录当前分支
	branch, _ := runGit(ctx, repoDir, "rev-parse", "--abbrev-ref", "HEAD")
	branch = strings.TrimSpace(branch)
	if _, err := runGit(ctx, repoDir, "checkout", "-q", sha); err != nil {
		return branch, err
	}
	return branch, nil
}

// Restore 切回原分支（版本化部署后恢复工作区，避免游离 HEAD 影响后续编码）。
func Restore(ctx context.Context, repoDir, branch string) {
	if branch == "" || branch == "HEAD" {
		// 游离 HEAD 状态（首次提交无分支），尝试切到 main/master
		for _, b := range []string{"main", "master"} {
			if _, err := runGit(ctx, repoDir, "checkout", "-q", b); err == nil {
				return
			}
		}
		return
	}
	_, _ = runGit(ctx, repoDir, "checkout", "-q", branch)
}

// Log 最近的提交（= 应用版本历史）。
func Log(ctx context.Context, repoDir string, n int) ([]CommitInfo, error) {
	if n <= 0 {
		n = 10
	}
	out, err := runGit(ctx, repoDir, "log", fmt.Sprintf("-%d", n), "--pretty=%h|%s|%ci")
	if err != nil {
		return nil, nil // 无提交时返回空
	}
	var list []CommitInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		c := CommitInfo{SHA: parts[0]}
		if len(parts) > 1 {
			c.Message = parts[1]
		}
		if len(parts) > 2 {
			c.Date = parts[2]
		}
		list = append(list, c)
	}
	return list, nil
}

// Diff 返回最近 n 次提交的代码差异(git log -p),展示"实际改了什么"(文件 + 行级改动)。
func Diff(ctx context.Context, repoDir string, n int) string {
	if n <= 0 {
		n = 3
	}
	out, err := runGit(ctx, repoDir, "log", "-p", fmt.Sprintf("-%d", n), "--pretty=commit %h %s")
	if err != nil {
		return ""
	}
	return out
}

// CommitInfo 提交（版本）信息。
type CommitInfo struct {
	SHA     string `json:"sha"`
	Message string `json:"message"`
	Date    string `json:"date"`
}

// DocEntry repo 内文档条目(README/.md),供编码时查阅项目文档。
type DocEntry struct {
	Path string `json:"path"` // 相对 repo 的路径(正斜杠)
	Name string `json:"name"` // 文件名
}

// ScanDocs 扫描 repo 内全部文件(代码 + 文档),排除 .git/依赖/隐藏,供编码时看项目文件结构。
func ScanDocs(repoDir string) ([]DocEntry, error) {
	var docs []DocEntry
	skipDir := func(base string) bool {
		return strings.HasPrefix(base, ".") || base == "node_modules" || base == ".next" || base == "__pycache__" || base == "dist" || base == "target" || base == "build"
	}
	_ = filepath.Walk(repoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		rel, _ := filepath.Rel(repoDir, path)
		if rel == "." {
			return nil
		}
		base := filepath.Base(rel)
		if info.IsDir() {
			if skipDir(base) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(base, ".") {
			return nil
		}
		docs = append(docs, DocEntry{Path: filepath.ToSlash(rel), Name: base})
		return nil
	})
	return docs, nil
}

// ReadRepoFile 读 repo 内相对路径文件内容(防 path traversal 越权)。
func ReadRepoFile(repoDir, rel string) (string, error) {
	cleanRoot := filepath.Clean(repoDir)
	abs := filepath.Clean(filepath.Join(cleanRoot, rel))
	if !strings.HasPrefix(abs, cleanRoot) {
		return "", fmt.Errorf("非法路径")
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

func runMkdir(ctx context.Context, dir string) error {
	return exec.CommandContext(ctx, "mkdir", "-p", dir).Run()
}

// sanitizeID 同 codews,转 git 友好分支名(与 worktree 的 dev-<user> 分支一致)。
func sanitizeID(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "dev"
	}
	return b.String()
}

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)

func sanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = unsafeName.ReplaceAllString(s, "-")
	if s == "" {
		s = "app"
	}
	return s
}

// buildCloneArgs 构造 git clone 参数。HTTPS 私有仓用 -c http.extraHeader 注入 token（不落 URL/config）；
// SSH 仓（git@）走部署机 ~/.ssh，不注入。
func buildCloneArgs(gitURL, authToken, target string) []string {
	args := []string{"clone", "--progress", gitURL, target}
	if authToken != "" && !strings.HasPrefix(gitURL, "git@") {
		args = append([]string{"-c", "http.extraHeader=Authorization: Bearer " + authToken}, args...)
	}
	return args
}

// ImportFromGit 把远程仓库 clone 到 ManagedRepoDir(name)。authToken 仅此处用，不落库。
// clone 在 ManagedRepoBase（父目录）执行，target 作为最后参数（target 此刻不存在）。
func ImportFromGit(ctx context.Context, name, gitURL, authToken string) (string, error) {
	target := ManagedRepoDir(name)
	if _, err := os.Stat(target); err == nil {
		return "", fmt.Errorf("目标目录已存在: %s（应用名冲突）", target)
	}
	args := buildCloneArgs(gitURL, authToken, target)
	if _, err := runGit(ctx, ManagedRepoBase, args...); err != nil {
		_ = os.RemoveAll(target)
		return "", fmt.Errorf("git clone 失败: %w（检查 URL/认证/网络）", err)
	}
	_, _ = runGit(ctx, target, "config", "user.email", "anp@platform")
	_, _ = runGit(ctx, target, "config", "user.name", "ANP Platform")
	return target, nil
}

// zip 上限（防 bomb）。
// MaxZipSize 为 var（非 const）：测试可缩小到 KB 级，避免构造 500MB bomb 的内存/IO 开销；
// 生产路径只读，行为与 const 等价。
var (
	MaxZipSize  int64 = 500 * 1024 * 1024 // 单次上传/解压体积上限
	MaxZipFiles int   = 10000             // 解压文件数上限
)

// ImportFromZip 把上传 zip 解压到 ManagedRepoDir(name)。防 zip slip（目标须在根下）+ bomb（体积/文件数）。
// 无 .git → git init + 初始提交；有 .git → 保留历史。
func ImportFromZip(ctx context.Context, name string, r io.ReaderAt, size int64) (string, error) {
	target := ManagedRepoDir(name)
	if _, err := os.Stat(target); err == nil {
		return "", fmt.Errorf("目标目录已存在: %s", target)
	}
	if size > MaxZipSize {
		return "", fmt.Errorf("zip 过大: %d > %d", size, MaxZipSize)
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return "", fmt.Errorf("zip 读取失败: %w", err)
	}
	if err := os.MkdirAll(target, 0755); err != nil {
		return "", err
	}
	cleanRoot := filepath.Clean(target)
	var totalSize int64
	var fileCount int
	for _, f := range zr.File {
		fileCount++
		if fileCount > MaxZipFiles {
			_ = os.RemoveAll(target)
			return "", fmt.Errorf("zip 文件数超限 %d", MaxZipFiles)
		}
		if f.Mode()&os.ModeSymlink != 0 {
			continue // 跳过符号链接 entry（防 zip symlink 攻击）
		}
		dest := filepath.Clean(filepath.Join(target, f.Name))
		// 防 zip slip：解压目标必须严格在 target 之下
		if dest != cleanRoot && !strings.HasPrefix(dest, cleanRoot+string(os.PathSeparator)) {
			_ = os.RemoveAll(target)
			return "", fmt.Errorf("zip slip 非法路径: %s", f.Name)
		}
		if dest == cleanRoot {
			continue // 跳过 "." 或空名 entry（dest 落到根，slip 检查旁路）
		}
		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(dest, 0755)
			continue
		}
		// C4：按实际解压字节累计（不依赖 zip 头声明的 UncompressedSize64）。
		// 恶意 zip 可头声明小、实际解压 GB 级；copyZipFile 用 LimitReader 兜底单文件至多 MaxZipSize+1，
		// 返回实际拷贝字节数，循环按 actual 累计判断——头撒谎也拦得住。
		actual, err := copyZipFile(dest, f)
		if err != nil {
			_ = os.RemoveAll(target)
			return "", fmt.Errorf("解压 %s 失败: %w", f.Name, err)
		}
		totalSize += actual
		if totalSize > MaxZipSize {
			_ = os.RemoveAll(target)
			return "", fmt.Errorf("zip 解压体积超限(bomb)")
		}
	}
	// 无 .git → 建仓 + 初始提交（不写模板，保留导入内容原样）
	if _, err := os.Stat(filepath.Join(target, ".git")); err != nil {
		stripNestedGit(target) // 上传项目常带子目录 .git（gitlink），清掉让全部内容进主仓
		_, _ = runGit(ctx, target, "init", "-q")
		_, _ = runGit(ctx, target, "config", "user.email", "anp@platform")
		_, _ = runGit(ctx, target, "config", "user.name", "ANP Platform")
		_, _ = runGit(ctx, target, "add", "-A")
		_, _ = runGit(ctx, target, "commit", "-q", "-m", "import: 初始导入")
	} else {
		// 有根 .git（保留历史）：仍可能带嵌套子目录 .git，清掉让主仓跟踪全部内容。
		stripNestedGit(target)
		_, _ = runGit(ctx, target, "add", "-A")
	}
	return target, nil
}

// copyZipFile 解压单个 zip 文件到 dest（先建父目录），返回实际拷贝字节数。
// 用 io.LimitReader(rc, MaxZipSize+1) 兜底：单文件至多解压 MaxZipSize+1 字节，
// 防恶意 zip 单条 entry 解压成 GB 级（头撒谎绕累计上限）。调用方按返回的 actual 累计。
func copyZipFile(dest string, f *zip.File) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return 0, err
	}
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	// LimitReader 兜底：即便头声明小、实际解压流无限，也只写 MaxZipSize+1 到磁盘
	return io.Copy(out, io.LimitReader(rc, MaxZipSize+1))
}

// stripNestedGit 递归删除 repoDir 下所有子目录的 .git（保留根 .git）。
// 上传/导入的项目常带嵌套 git 仓（如从 GitHub clone 的子目录），主仓会把子目录记成 gitlink、
// 不跟踪其内容 → worktree checkout 时子目录为空 → 编码产出/部署都拿不到代码。
// 解压后、git add 前调此函数，把嵌套 .git 清掉，让全部内容成为主仓 git 跟踪的一部分。
func stripNestedGit(repoDir string) {
	rootGit := filepath.Join(repoDir, ".git")
	_ = filepath.Walk(repoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || !info.IsDir() {
			return nil
		}
		if path == rootGit {
			return filepath.SkipDir // 保留主仓根 .git
		}
		if filepath.Base(path) == ".git" {
			_ = os.RemoveAll(path)
			return filepath.SkipDir // 删完不再下钻
		}
		return nil
	})
}

// AllowedDirRoots 服务器目录导入白名单根（可配置）。
var AllowedDirRoots = []string{"/data/", "/opt/legacy/"}

// isUnderAllowedRoot 路径是否在某白名单根下（path 经 Clean+ToSlash；root 仅 ToSlash 保留尾斜杠，
// 跨平台一致：白名单根统一用正斜杠 /data/ /opt/legacy/）。
func isUnderAllowedRoot(p string) bool {
	p = filepath.ToSlash(filepath.Clean(p))
	for _, root := range AllowedDirRoots {
		if strings.HasPrefix(p, filepath.ToSlash(root)) {
			return true
		}
	}
	return false
}

// ImportFromDir 把服务器已有目录导入托管仓。srcPath 须在白名单下（防 ../ 穿越）；
// 源含 .git → 本地 clone 保留历史；否则 cp -r + git init。
func ImportFromDir(ctx context.Context, name, srcPath string) (string, error) {
	cleanSrc := filepath.Clean(srcPath)
	if !isUnderAllowedRoot(cleanSrc) {
		return "", fmt.Errorf("目录不在允许的根目录下: %s", srcPath)
	}
	if _, err := os.Stat(cleanSrc); err != nil {
		return "", fmt.Errorf("源目录不存在: %s", srcPath)
	}
	target := ManagedRepoDir(name)
	if _, err := os.Stat(target); err == nil {
		return "", fmt.Errorf("目标目录已存在: %s", target)
	}
	if _, err := os.Stat(filepath.Join(cleanSrc, ".git")); err == nil {
		if _, err := runGit(ctx, ManagedRepoBase, "clone", cleanSrc, target); err != nil {
			_ = os.RemoveAll(target)
			return "", fmt.Errorf("本地 clone 失败: %w", err)
		}
		// clone 的源可能含嵌套子目录 .git，清掉让主仓跟踪全部内容。
		stripNestedGit(target)
		_, _ = runGit(ctx, target, "add", "-A")
	} else {
		if err := exec.CommandContext(ctx, "cp", "-r", cleanSrc, target).Run(); err != nil {
			_ = os.RemoveAll(target)
			return "", fmt.Errorf("复制目录失败: %w", err)
		}
		stripNestedGit(target) // cp 进来的可能含嵌套 .git
		_, _ = runGit(ctx, target, "init", "-q")
		_, _ = runGit(ctx, target, "config", "user.email", "anp@platform")
		_, _ = runGit(ctx, target, "config", "user.name", "ANP Platform")
		_, _ = runGit(ctx, target, "add", "-A")
		_, _ = runGit(ctx, target, "commit", "-q", "-m", "import: 初始导入")
	}
	return target, nil
}

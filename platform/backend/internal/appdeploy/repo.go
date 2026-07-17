package appdeploy

import (
	"bytes"
	"context"
	"fmt"
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
const ManagedRepoBase = "/data/repos"

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

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)

func sanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = unsafeName.ReplaceAllString(s, "-")
	if s == "" {
		s = "app"
	}
	return s
}

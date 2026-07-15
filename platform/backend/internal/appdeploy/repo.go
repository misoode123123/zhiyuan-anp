package appdeploy

import (
	"bytes"
	"context"
	"fmt"
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
	return nil
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

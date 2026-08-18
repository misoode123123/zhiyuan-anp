package appdeploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NativeDeployer 按部署描述生成脚本、经 RemoteExecutor 执行（非容器原生部署）。
type NativeDeployer struct{}

// NativeDeployResult 原生（非容器）部署执行结果。
// 注：原名 DeployResult，AI 直接部署引擎（ai_brief.go）引入 AI 回报模型后让出该名。
type NativeDeployResult struct {
	Log      string `json:"log"`
	ExitCode int    `json:"exit_code"`
}

// Deploy 渲染脚本 + 传文件 + 执行 + 健康检查。
func (d *NativeDeployer) Deploy(ctx context.Context, app *Application, node *DeployNode, exec RemoteExecutor, desc *DeployDesc) (NativeDeployResult, error) {
	// 0. 预创建 transfer 目标目录（PutFile 写文件前父目录须存在，否则 Windows WriteAllBytes
	//    / Linux base64 重定向因路径缺失失败）。run 脚本里的 New-Item/mkdir 在 PutFile 之后，
	//    故这里先建。
	if err := d.ensureTransferDirs(ctx, node, exec, desc); err != nil {
		return NativeDeployResult{}, fmt.Errorf("create transfer dirs: %w", err)
	}
	// 1. 逐 transfer 步 PutFile 产物
	for _, s := range desc.Steps {
		if s.Transfer == nil {
			continue
		}
		// From 支持相对 RepoDir 的 glob 模式（如 "./*" 或 "dist/*"）；也允许已是绝对路径
		pattern := s.Transfer.From
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(app.RepoDir, pattern)
		}
		files, err := filepath.Glob(pattern)
		if err != nil {
			return NativeDeployResult{}, fmt.Errorf("glob %s: %w", pattern, err)
		}
		for _, f := range files {
			remote := joinRemotePath(s.Transfer.To, filepath.Base(f), node.OSType)
			if err := exec.PutFile(ctx, f, remote); err != nil {
				return NativeDeployResult{}, fmt.Errorf("put %s: %w", f, err)
			}
		}
	}
	// 2. 渲染脚本 + 执行
	script, err := RenderScript(node, desc)
	if err != nil {
		return NativeDeployResult{}, fmt.Errorf("render script: %w", err)
	}
	out, _, exit, err := exec.Run(ctx, script)
	if err != nil {
		return NativeDeployResult{Log: out, ExitCode: exit}, err
	}
	// 3. healthcheck 已在脚本里，由 exit 判定
	return NativeDeployResult{Log: out, ExitCode: exit}, nil
}

// ensureTransferDirs 在 PutFile 前预创建所有 transfer 步的目标目录。
// Windows 走 PowerShell New-Item（经 SSHExecutor 自动包成 EncodedCommand），
// Linux 走 mkdir -p。目录已存在不报错（-Force / -p）。
func (d *NativeDeployer) ensureTransferDirs(ctx context.Context, node *DeployNode, exec RemoteExecutor, desc *DeployDesc) error {
	var dirs []string
	for _, s := range desc.Steps {
		if s.Transfer != nil && strings.TrimSpace(s.Transfer.To) != "" {
			dirs = append(dirs, s.Transfer.To)
		}
	}
	if len(dirs) == 0 {
		return nil
	}
	var cmd string
	if node != nil && node.OSType == "windows" {
		parts := make([]string, 0, len(dirs))
		for _, dir := range dirs {
			parts = append(parts, fmt.Sprintf("New-Item -ItemType Directory -Force -Path '%s' | Out-Null", psQuote(dir)))
		}
		cmd = strings.Join(parts, "; ")
	} else {
		quoted := make([]string, 0, len(dirs))
		for _, dir := range dirs {
			quoted = append(quoted, sshQuote(dir))
		}
		cmd = "mkdir -p " + strings.Join(quoted, " ")
	}
	_, _, _, err := exec.Run(ctx, cmd)
	return err
}

// loadDeployDesc 从应用 RepoDir 读 deploy.yaml，无则返回 nil（应用可不走原生部署）。
func loadDeployDesc(repoDir string) (*DeployDesc, error) {
	p := filepath.Join(repoDir, "deploy.yaml")
	b, err := os.ReadFile(p)
	if err != nil {
		// 无文件视为不启用原生部署
		return nil, nil
	}
	return ParseDeployDesc(b)
}

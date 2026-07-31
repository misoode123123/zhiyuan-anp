package appdeploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// NativeDeployer 按部署描述生成脚本、经 RemoteExecutor 执行（非容器原生部署）。
type NativeDeployer struct{}

// DeployResult 部署执行结果。
type DeployResult struct {
	Log      string `json:"log"`
	ExitCode int    `json:"exit_code"`
}

// Deploy 渲染脚本 + 传文件 + 执行 + 健康检查。
func (d *NativeDeployer) Deploy(ctx context.Context, app *Application, node *DeployNode, exec RemoteExecutor, desc *DeployDesc) (DeployResult, error) {
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
			return DeployResult{}, fmt.Errorf("glob %s: %w", pattern, err)
		}
		for _, f := range files {
			remote := joinRemotePath(s.Transfer.To, filepath.Base(f), node.OSType)
			if err := exec.PutFile(ctx, f, remote); err != nil {
				return DeployResult{}, fmt.Errorf("put %s: %w", f, err)
			}
		}
	}
	// 2. 渲染脚本 + 执行
	script, err := RenderScript(node, desc)
	if err != nil {
		return DeployResult{}, fmt.Errorf("render script: %w", err)
	}
	out, _, exit, err := exec.Run(ctx, script)
	if err != nil {
		return DeployResult{Log: out, ExitCode: exit}, err
	}
	// 3. healthcheck 已在脚本里，由 exit 判定
	return DeployResult{Log: out, ExitCode: exit}, nil
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

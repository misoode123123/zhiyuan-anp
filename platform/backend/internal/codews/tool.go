package codews

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

// Tool 通用 AI 编码工具后端接口（opencode / Claude Code / Codex / ...）。
// 每个实现把工具启动为可交互的 web 服务，开发者浏览器访问即得其原生编码体验。
// 新接入第三方工具只需实现此接口并 Manager.Register。
type Tool interface {
	Name() string
	// Start 在 repoDir 启动工具的交互式 web 服务（监听 port），返回已启动的进程。
	Start(repoDir string, port int) (*exec.Cmd, error)
}

// OpenCodeTool opencode（已接入）：opencode serve 自带官方 web UI。
type OpenCodeTool struct{}

func (OpenCodeTool) Name() string { return "opencode" }
func (OpenCodeTool) Start(repoDir string, port int) (*exec.Cmd, error) {
	cmd := exec.Command("opencode", "serve", "--port", strconv.Itoa(port), "--hostname", "0.0.0.0")
	cmd.Dir = repoDir
	cmd.Env = os.Environ() // 继承容器 env（OPENCODE_CONFIG 等）
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 opencode serve: %w", err)
	}
	return cmd, nil
}

// ClaudeTool Claude Code（接口预留，待接入其 web/headless 服务模式）。
type ClaudeTool struct{}

func (ClaudeTool) Name() string { return "claude" }
func (ClaudeTool) Start(repoDir string, port int) (*exec.Cmd, error) {
	return nil, fmt.Errorf("claude 工具尚未接入（Tool 接口已预留，待实现 Start）")
}

// CodexTool OpenAI Codex（接口预留）。
type CodexTool struct{}

func (CodexTool) Name() string { return "codex" }
func (CodexTool) Start(repoDir string, port int) (*exec.Cmd, error) {
	return nil, fmt.Errorf("codex 工具尚未接入（Tool 接口已预留，待实现 Start）")
}

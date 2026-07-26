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
	// env 为额外注入子进程的环境变量（"KEY=VAL" 形式），工具自取所需（如智谱兼容端点 key/base_url）。
	Start(repoDir string, port int, env []string) (*exec.Cmd, error)
}

// OpenCodeTool opencode（已接入）：opencode serve 自带官方 web UI。
type OpenCodeTool struct{}

func (OpenCodeTool) Name() string { return "opencode" }
func (OpenCodeTool) Start(repoDir string, port int, env []string) (*exec.Cmd, error) {
	// opencode serve 只读默认路径 $HOME/.config/opencode/opencode.json，不读 OPENCODE_CONFIG env；
	// backend 启动时已把平台配置复制到该默认路径（见 cmd/server/main.go）。继承 env 仅为透传其他变量。
	cmd := exec.Command("opencode", "serve", "--port", strconv.Itoa(port), "--hostname", "0.0.0.0")
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(), env...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 opencode serve: %w", err)
	}
	return cmd, nil
}

// ClaudeTool Claude Code（ttyd web 终端接入，走智谱 anthropic 兼容端点）。
// claude/codex 无 opencode 那样的自带 web UI，用 ttyd 把 CLI 暴露为 web 终端，浏览器 iframe 嵌入。
type ClaudeTool struct{}

func (ClaudeTool) Name() string { return "claude" }
func (ClaudeTool) Start(repoDir string, port int, env []string) (*exec.Cmd, error) {
	// ttyd 把 claude CLI 暴露为 web 终端；--writable 允许浏览器输入；claude --continue 恢复该目录最近会话。
	// 智谱兼容 env（ANTHROPIC_BASE_URL/AUTH_TOKEN/MODEL）由 Manager 经 env 注入。
	cmd := exec.Command("ttyd", "-p", strconv.Itoa(port), "--writable", "claude", "--continue")
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(), env...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 claude(ttyd): %w", err)
	}
	return cmd, nil
}

// CodexTool OpenAI Codex（留下阶段：claude MVP 验证 ttyd 模式后对称接入）。
type CodexTool struct{}

func (CodexTool) Name() string { return "codex" }
func (CodexTool) Start(repoDir string, port int, env []string) (*exec.Cmd, error) {
	return nil, fmt.Errorf("codex 工具留下阶段（claude MVP 验证后对称接入 ttyd 模式）")
}

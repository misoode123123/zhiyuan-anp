package appdeploy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AppContextPrompt 开发者【自主发起】编码 session（无需求绑定）时注入的应用上下文。
// 与需求驱动的 prompt（buildReqPrompt）互补：需求驱动注入「要做什么」，自主发起注入
// 「这是什么应用」——让 AI 一上来就掌握应用名/形态/类型、仓库结构、依赖中间件、当前部署态。
//
// 公司开发规范不在此处注入：Workspace handler 已调 RefreshAgentsMD 把规范写进 worktree 的
// AGENTS.md（opencode 自动加载）。本 prompt 只负责应用上下文 + 指引去读 AGENTS.md / .anp/deploy.yaml。
//
// 触发条件（handler）：RequirementID 为空 且 ForceNew（新会话） 且 无显式 Prompt。
// 复用会话不注入（避免重复）。
func AppContextPrompt(a *Application) string {
	if a == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "你正在 ANP 平台上为应用「%s」编码。\n\n", a.Name)

	// 应用元信息
	kind := a.AppKind
	if kind == "" {
		kind = "web"
	}
	mode := "自管（managed）"
	if a.DeployMode == "external" {
		mode = "纳管外部应用（external）"
	}
	b.WriteString("应用信息：\n")
	fmt.Fprintf(&b, "- 名称：%s\n", a.Name)
	fmt.Fprintf(&b, "- 形态：%s\n", kind)
	fmt.Fprintf(&b, "- 类型：%s\n", mode)
	fmt.Fprintf(&b, "- 当前版本：v%d（部署态：%s）\n", a.Version, a.Status)
	if a.InternalPort > 0 {
		fmt.Fprintf(&b, "- 监听端口：%d\n", a.InternalPort)
	}

	// 仓库结构概览（readRepoCode 已截断文件数/字符数；再兜一层防止过长撑爆 prompt）
	if a.RepoDir != "" {
		if code := readRepoCode(a.RepoDir); code != "" {
			fmt.Fprintf(&b, "\n仓库结构概览（自动扫描，已截断）：\n%s\n", truncateStr(code, 4000))
		}
		// 声明的中间件依赖（.anp/deps.yaml，readRepoCode 跳过隐藏文件故单独读）
		if deps := readDepsSummary(a.RepoDir); deps != "" {
			fmt.Fprintf(&b, "\n声明的中间件依赖（.anp/deps.yaml）：\n%s\n", deps)
		}
	}

	// 开发指引：规范与部署规格的去向
	b.WriteString("\n开发指引：\n")
	b.WriteString("- 严格遵循仓库根 AGENTS.md 的「ANP 部署适配规范」与编码规范")
	b.WriteString("（配置优先读环境变量、禁硬编码中间件地址、多阶段 Dockerfile 等）。\n")
	b.WriteString("- 部署运行规格见 .anp/deploy.yaml（挂载/env/端口/命令）；改了部署需求请同步更新它。\n")
	b.WriteString("\n开始吧：告诉我你要做什么，或直接动手。")
	return b.String()
}

// readDepsSummary 读仓库根 .anp/deps.yaml 原文（中间件依赖声明），供应用上下文 prompt 展示。
// 不存在/读失败返回空串（无中间件依赖或尚未声明）。
func readDepsSummary(repoDir string) string {
	b, err := os.ReadFile(filepath.Join(repoDir, ".anp", "deps.yaml"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

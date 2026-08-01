package appdeploy

// AdaptPrompt 导入后让 opencode 把应用适配成可在 ANP 部署运行的指令。
// 结合仓库内 AGENTS.md（含「ANP 部署适配规范」段，由 RefreshAgentsMD 刷新）一起喂给 opencode。
func AdaptPrompt(appName string) string {
	return "把本仓库的应用「" + appName + "」适配成能在 ANP 平台部署运行。" +
		"严格按仓库根 AGENTS.md 的「ANP 部署适配规范」改：" +
		"配置改为优先读环境变量（禁硬编码 127.0.0.1/localhost 访问中间件）、" +
		"确保仓库根有可构建的多阶段 Dockerfile 并 EXPOSE 监听端口、" +
		"所需中间件不要写死地址（由 ANP 经环境变量注入连接信息）。" +
		"若部署机缺某依赖服务，在变更说明里报明缺什么（kind/原因）。" +
		"改完提交，提交说明里列出改了哪些文件、为什么。"
}

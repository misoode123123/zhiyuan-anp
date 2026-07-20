// Package dev 是「研发」限界上下文 —— AI 协同编码主战场。
// 编码引擎：opencode（开源、headless、多模型），由 Go 后端调度。
package dev

// CodeTask 编码任务。
type CodeTask struct {
	ID             string `json:"id"`
	ProjectSpaceID string `json:"project_space_id"`
	Prompt         string `json:"prompt"`
	RepoDir        string `json:"repo_dir"`
	Model          string `json:"model,omitempty"`
	Status         string `json:"status"`
	Output         string `json:"output,omitempty"`
}

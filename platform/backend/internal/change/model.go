// Package change 是「变更闸门」限界上下文 —— 🚪G3 代码闸门审批流。
// AI 编码产出登记为待审批变更，人工批准/拒绝后才算"合入"。
package change

import "time"

// ChangeRequest 变更审批单。
type ChangeRequest struct {
	ID             string     `json:"id" db:"id"`
	ProjectSpaceID string     `json:"project_space_id" db:"project_space_id"`
	Kind           string     `json:"kind" db:"kind"` // code / dispatch
	SourceID       string     `json:"source_id" db:"source_id"`
	RepoDir        string     `json:"repo_dir" db:"repo_dir"`
	Prompt         string     `json:"prompt" db:"prompt"`
	Model          string     `json:"model" db:"model"`
	Output         string     `json:"output" db:"output"`
	Status         string     `json:"status" db:"status"` // pending / approved / rejected
	Reviewer       *string    `json:"reviewer" db:"reviewer"`
	ReviewedAt     *time.Time `json:"reviewed_at" db:"reviewed_at"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
}

// Package requirement 是「需求」限界上下文 —— 业务侧主入口。
package requirement

import "time"

// Requirement 需求（含 AI 生成的结构化规格）。
type Requirement struct {
	ID                 string    `json:"id" db:"id"`
	ProjectSpaceID     string    `json:"project_space_id" db:"project_space_id"`
	ApplicationID      string    `json:"application_id,omitempty" db:"application_id"` // 归属应用（一等公民：需求挂在应用下）
	Title              string    `json:"title" db:"title"`
	Description        string    `json:"description" db:"description"`
	UserStory          string    `json:"user_story" db:"user_story"`
	AcceptanceCriteria string    `json:"acceptance_criteria" db:"acceptance_criteria"` // JSON 数组字符串
	Status             string    `json:"status" db:"status"`
	Priority           string    `json:"priority" db:"priority"`           // P0紧急/P1常规/P2待定,默认 P1
	FixedVersion       string    `json:"fixed_version" db:"fixed_version"` // 计划版本,空=未排期
	Tasks              string    `json:"tasks" db:"tasks"`                 // JSON 子任务清单 [{text,done}]
	CreatedAt          time.Time `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time `json:"updated_at" db:"updated_at"`
}

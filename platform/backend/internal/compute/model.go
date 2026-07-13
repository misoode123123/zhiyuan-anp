// Package compute 是「算力与资源」限界上下文 —— 企业"只投入算力"的落点。
package compute

import "time"

// UsageRecord 一次 AI 调用的用量记录。
type UsageRecord struct {
	ID               string    `json:"id" db:"id"`
	ProjectSpaceID   string    `json:"project_space_id" db:"project_space_id"`
	Model            string    `json:"model" db:"model"`
	Kind             string    `json:"kind" db:"kind"` // chat/spec/test/code
	PromptTokens     int       `json:"prompt_tokens" db:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens" db:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens" db:"total_tokens"`
	CreatedAt        time.Time `json:"created_at" db:"created_at"`
}

// Stats 用量统计。
type Stats struct {
	TotalTokens int         `json:"total_tokens"`
	TotalCalls  int         `json:"total_calls"`
	ByModel     []ModelStat `json:"by_model"`
}

// ModelStat 按模型聚合。
type ModelStat struct {
	Model string `json:"model" db:"model"`
	Tokens int   `json:"tokens" db:"tokens"`
	Calls  int   `json:"calls" db:"calls"`
}

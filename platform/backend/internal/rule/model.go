// Package rule 是「规则治理」限界上下文 —— 平台心脏。
// 制度/红线结构化为可执行规则（RaC），约束所有 AI 行为。
package rule

import "time"

// Rule 可执行规则。
type Rule struct {
	ID             string    `json:"id" db:"id"`
	Name           string    `json:"name" db:"name"`
	Category       string    `json:"category" db:"category"`
	Type           string    `json:"type" db:"type"` // mandatory / should / reference
	Condition      string    `json:"condition" db:"condition"`
	ConditionField string    `json:"condition_field" db:"condition_field"` // prompt / output / code_path
	Action         string    `json:"action" db:"action"`                   // block / warn / require_approval
	Scope          string    `json:"scope" db:"scope"`                     // dev / requirement / all
	Enabled        bool      `json:"enabled" db:"enabled"`
	Description    string    `json:"description" db:"description"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}

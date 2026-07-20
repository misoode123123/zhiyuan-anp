// Package codetask 是异步编码任务记录（解决同步阻塞：编码后台跑，HTTP 立即返回）。
package codetask

import "time"

// Task 异步编码任务。
type Task struct {
	ID             string    `json:"id" db:"id"`
	ProjectSpaceID string    `json:"project_space_id" db:"project_space_id"`
	Kind           string    `json:"kind" db:"kind"` // code / dispatch
	SourceID       string    `json:"source_id" db:"source_id"`
	RepoDir        string    `json:"repo_dir" db:"repo_dir"`
	Prompt         string    `json:"prompt" db:"prompt"`
	Model          string    `json:"model" db:"model"`
	Status         string    `json:"status" db:"status"` // running/completed/failed
	Output         *string   `json:"output" db:"output"`
	ChangeID       *string   `json:"change_id" db:"change_id"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
	ReqTitle       string    `json:"req_title" db:"req_title"` // 派生:source_id=requirement_id 时 JOIN 出的需求标题
	AppName        string    `json:"app_name" db:"app_name"`   // 派生:change_id→change→app 的应用名
}

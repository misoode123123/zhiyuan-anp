// Package release 是「发布」限界上下文 —— 🚪G5 上线闸门后的发布。
package release

import "time"

// Release 发布记录。
type Release struct {
	ID             string    `json:"id" db:"id"`
	ProjectSpaceID string    `json:"project_space_id" db:"project_space_id"`
	ChangeID       string    `json:"change_id" db:"change_id"`
	Version        string    `json:"version" db:"version"`
	Status         string    `json:"status" db:"status"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	AppName        string    `json:"app_name" db:"app_name"` // 派生:release→change→source→app
	Reviewer       string    `json:"reviewer" db:"reviewer"` // 派生:审批/提交人(change.reviewer)
	Prompt         string    `json:"prompt" db:"prompt"`     // 派生:变更说明(change.prompt)
	Output         string    `json:"output" db:"output"`     // 派生:变更产出(change.output,含【总结】)
}

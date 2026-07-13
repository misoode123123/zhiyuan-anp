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
}

// Package qa 是「测试与质量」限界上下文（包名 qa 避开 Go 标准库 testing）。
package qa

import "time"

// TestCase 测试用例。
type TestCase struct {
	ID             string    `json:"id" db:"id"`
	ProjectSpaceID string    `json:"project_space_id" db:"project_space_id"`
	RequirementID  string    `json:"requirement_id" db:"requirement_id"`
	Title          string    `json:"title" db:"title"`
	Steps          string    `json:"steps" db:"steps"` // JSON 数组字符串
	Expected       string    `json:"expected" db:"expected"`
	Status         string    `json:"status" db:"status"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
}

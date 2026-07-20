// Package qa 是「测试与质量」限界上下文（包名 qa 避开 Go 标准库 testing）。
package qa

import "time"

// TestCase 测试用例。
// status: draft(未运行) / passed / failed / manual(无 HTTP 断言，需人工验证)。
type TestCase struct {
	ID             string `json:"id" db:"id"`
	ProjectSpaceID string `json:"project_space_id" db:"project_space_id"`
	RequirementID  string `json:"requirement_id" db:"requirement_id"`
	Title          string `json:"title" db:"title"`
	Steps          string `json:"steps" db:"steps"` // JSON 数组字符串
	Expected       string `json:"expected" db:"expected"`
	Status         string `json:"status" db:"status"`
	// 可执行 HTTP 检查（AI 生成时对 Web 服务类需求一并产出）。
	Method         string `json:"method,omitempty" db:"method"`                   // GET/POST/...；空按 GET
	Path           string `json:"path,omitempty" db:"path"`                       // 如 /；空按 /
	ExpectedStatus int    `json:"expected_status,omitempty" db:"expected_status"` // 期望状态码，如 200
	ExpectedBody   string `json:"expected_body,omitempty" db:"expected_body"`     // 期望响应体包含的文本
	// 运行后回写。
	ActualStatus int        `json:"actual_status,omitempty" db:"actual_status"`
	ActualBody   string     `json:"actual_body,omitempty" db:"actual_body"`
	RunAt        *time.Time `json:"run_at,omitempty" db:"run_at"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
}

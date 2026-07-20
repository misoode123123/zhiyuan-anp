// Package ops 是「运维中心」限界上下文（板块07）—— 平台稳定性的守门人。
//
// M1 切片（愿景中的 AI 巡检/根因/自愈需 Python LangGraph + Prometheus 基建，列为 P2/P3）：
//   - 真实健康检查：DB / agent-runtime / opencode 可达性
//   - 运营看板：从既有表（requirement/code_task/change_request/release_record/usage_record）聚合
//   - 活动流：跨表最近事件
//   - 告警：手动录入 + 巡检自动产生（健康检查失败即告警）
//   - SOP 预案库：运维标准操作流程 CRUD
//
// 多租户：所有数据带 project_space_id。
package ops

import "time"

// Alert 告警事件（精简版：去重指纹 + 严重度 + 状态机）。
type Alert struct {
	ID             string     `json:"id" db:"id"`
	ProjectSpaceID string     `json:"project_space_id" db:"project_space_id"`
	Source         string     `json:"source" db:"source"`           // patrol/prometheus/loki/k8s/custom
	Severity       string     `json:"severity" db:"severity"`       // critical/warning/info
	Status         string     `json:"status" db:"status"`           // firing/resolved/suppressed
	Fingerprint    string     `json:"fingerprint" db:"fingerprint"` // 去重指纹（source+title 哈希前缀）
	Title          string     `json:"title" db:"title"`
	Description    string     `json:"description" db:"description"`
	FiredAt        time.Time  `json:"fired_at" db:"fired_at"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty" db:"resolved_at"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
}

// SOP 自愈预案 / 运维标准操作流程。
type SOP struct {
	ID               string    `json:"id" db:"id"`
	ProjectSpaceID   string    `json:"project_space_id" db:"project_space_id"`
	Code             string    `json:"code" db:"code"` // 唯一编码
	Name             string    `json:"name" db:"name"`
	Description      string    `json:"description" db:"description"`
	Category         string    `json:"category" db:"category"`     // restart/scale/cache/traffic/data
	RiskLevel        string    `json:"risk_level" db:"risk_level"` // low/medium/high
	Steps            string    `json:"steps" db:"steps"`           // 执行步骤（自然语言/Markdown）
	Rollback         string    `json:"rollback" db:"rollback"`     // 回滚预案
	RequiresApproval bool      `json:"requires_approval" db:"requires_approval"`
	Status           string    `json:"status" db:"status"` // draft/active/deprecated
	CreatedAt        time.Time `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time `json:"updated_at" db:"updated_at"`
}

// ComponentHealth 单个组件的健康检查结果。
type ComponentHealth struct {
	Name    string `json:"name"`   // db / agent-runtime / opencode
	Status  string `json:"status"` // healthy/degraded/down
	Detail  string `json:"detail"` // 状态详情/错误信息
	Latency int64  `json:"latency_ms,omitempty"`
}

// Dashboard 运维总览看板。
type Dashboard struct {
	OverallHealth string            `json:"overall_health"` // healthy/degraded/down
	Components    []ComponentHealth `json:"components"`
	Stats         Stats             `json:"stats"`
	Usage         UsageSummary      `json:"usage"`
	Activity      []ActivityItem    `json:"activity"`
	OpenAlerts    int               `json:"open_alerts"`
}

// Stats 各业务表的计数统计（运营快照）。
type Stats struct {
	Requirements map[string]int `json:"requirements"` // by status
	CodeTasks    map[string]int `json:"code_tasks"`   // by status
	Changes      map[string]int `json:"changes"`      // by status
	Releases     int            `json:"releases"`
	ActiveAlerts int            `json:"active_alerts"`
	ActiveSOPs   int            `json:"active_sops"`
}

// UsageSummary 算力用量摘要（联动 compute 数据）。
type UsageSummary struct {
	TotalTokens int `json:"total_tokens"`
	TotalCalls  int `json:"total_calls"`
}

// ActivityItem 活动流条目（跨表归并）。
type ActivityItem struct {
	Time   time.Time `json:"time" db:"time"`
	Kind   string    `json:"kind" db:"kind"`     // requirement/code_task/change/release
	Action string    `json:"action" db:"action"` // 状态/动作
	Title  string    `json:"title" db:"title"`
	RefID  string    `json:"ref_id" db:"ref_id"`
}

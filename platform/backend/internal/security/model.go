// Package security 是「安全与合规中心」限界上下文（板块05）——
// 守住"不泄密 / 不违规 / 不带病上线"，安全扫描嵌入流程而非事后审计。
//
// M1 切片（愿景中的 Semgrep/Trivy/Gitleaks + Python LangGraph + gRPC 列为后续阶段）：
//   - Go 原生正则扫描引擎：密钥泄露(RULE-SEC-001)/SAST-lite/提示注入(RULE-SEC-010)
//   - 外部扫描工具抽象为 Scanner 接口，默认 Go 正则实现，后续可插 Semgrep 等
//   - 发现(Finding)存储 + 误报抑制
//   - 安全门判定：critical/high 未修复则阻断（供板块06 发布消费）
//   - 数据分级 CRUD（public/internal/confidential/restricted）
//   - 安全审计日志
//
// 多租户：所有数据带 project_space_id。
package security

import "time"

// ScanResult 一次安全扫描的聚合记录。
type ScanResult struct {
	ID             string    `json:"id" db:"id"`
	ProjectSpaceID string    `json:"project_space_id" db:"project_space_id"`
	ScanType       string    `json:"scan_type" db:"scan_type"` // secret/sast/prompt/full
	RiskLevel      string    `json:"risk_level" db:"risk_level"` // critical/high/medium/low/clean
	TotalFindings  int       `json:"total_findings" db:"total_findings"`
	CriticalCount  int       `json:"critical_count" db:"critical_count"`
	HighCount      int       `json:"high_count" db:"high_count"`
	MediumCount    int       `json:"medium_count" db:"medium_count"`
	LowCount       int       `json:"low_count" db:"low_count"`
	ContentPreview string    `json:"content_preview" db:"content_preview"` // 扫描输入的前 200 字符（追溯）
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
}

// Finding 单条安全发现（漏洞/密钥泄露/注入）。
type Finding struct {
	ID             string     `json:"id" db:"id"`
	ProjectSpaceID string     `json:"project_space_id" db:"project_space_id"`
	ScanResultID   string     `json:"scan_result_id" db:"scan_result_id"`
	Category       string     `json:"category" db:"category"` // secret/sast/prompt
	RuleID         string     `json:"rule_id" db:"rule_id"`   // RULE-SEC-xxx
	Severity       string     `json:"severity" db:"severity"` // critical/high/medium/low
	Title          string     `json:"title" db:"title"`
	Description    string     `json:"description" db:"description"`
	LineNumber     *int       `json:"line_number,omitempty" db:"line_number"`
	CodeSnippet    string     `json:"code_snippet" db:"code_snippet"`
	Remediation    string     `json:"remediation" db:"remediation"`
	Confidence     float64    `json:"confidence" db:"confidence"`
	Status         string     `json:"status" db:"status"` // open/suppressed/fixed
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	SuppressedAt   *time.Time `json:"suppressed_at,omitempty" db:"suppressed_at"`
}

// DataClassification 敏感数据分级登记。
type DataClassification struct {
	ID               string    `json:"id" db:"id"`
	ProjectSpaceID   string    `json:"project_space_id" db:"project_space_id"`
	FieldName        string    `json:"field_name" db:"field_name"`
	TableRef         string    `json:"table_ref" db:"table_ref"`
	SensitivityLevel string    `json:"sensitivity_level" db:"sensitivity_level"` // public/internal/confidential/restricted
	DataType         string    `json:"data_type" db:"data_type"`                 // pii/pci/phi/secret/ip/personal
	MaskingStrategy  string    `json:"masking_strategy" db:"masking_strategy"`   // mask/hash/replace/suppress/synthetic
	Status           string    `json:"status" db:"status"`                       // draft/confirmed
	CreatedAt        time.Time `json:"created_at" db:"created_at"`
}

// AuditLog 安全视角的审计记录（AI 行为/安全决策留痕）。
type AuditLog struct {
	ID             string    `json:"id" db:"id"`
	ProjectSpaceID string    `json:"project_space_id" db:"project_space_id"`
	ActorType      string    `json:"actor_type" db:"actor_type"` // agent/human/system
	ActorID        string    `json:"actor_id" db:"actor_id"`
	Action         string    `json:"action" db:"action"`         // scan/suppress/gate/leak_blocked
	ResourceType   string    `json:"resource_type" db:"resource_type"`
	Detail         string    `json:"detail" db:"detail"`
	PolicyDecision string    `json:"policy_decision" db:"policy_decision"` // allow/deny/mask
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
}

// Gate 安全门判定结果（实时计算，供发布消费）。
type Gate struct {
	OverallRiskLevel string `json:"overall_risk_level"` // critical/high/medium/low/clean
	GatePassed       bool   `json:"gate_passed"`
	CriticalOpen     int    `json:"critical_open"`
	HighOpen         int    `json:"high_open"`
	BlockingReason   string `json:"blocking_reason,omitempty"`
}

// Package capability 是「AI 能力市场」限界上下文（板块09）——
// 平台 AI 能力的统一对外窗口：技能注册/上架/调用 + APIKey + 用量 + 领域 Agent。
//
// M1 切片（愿景中的 Copilot SDK npm 包 + LangGraph 多技能编排 + LiteLLM 列为 P3）：
//   - 技能注册表：CRUD + 生命周期(draft→pending_review→active→offline)
//   - APIKey：真实生成 + SHA256 哈希存储 + 前缀展示 + 吊销
//   - 统一调用网关：APIKey 鉴权 → 校验技能 active → 调 agent-runtime /v1/chat → 记用量
//   - 用量看板（按技能聚合）
//   - 领域 Agent 注册表（CRUD）
//
// 多租户：管理类数据带 project_space_id；invoke 调用按 APIKey 归属空间计费。
package capability

import "time"

// Skill 可复用 AI 技能。
type Skill struct {
	ID              string    `json:"id" db:"id"`
	ProjectSpaceID  string    `json:"project_space_id" db:"project_space_id"`
	Code            string    `json:"code" db:"code"` // 唯一编码 data-qa / report-gen
	Name            string    `json:"name" db:"name"`
	Description     string    `json:"description" db:"description"`
	Category        string    `json:"category" db:"category"`               // requirement/doc_gen/data_qa/approval/report/code/assistant
	PromptTemplate  string    `json:"prompt_template" db:"prompt_template"` // 提示模板（{input} 占位）
	Version         string    `json:"version" db:"version"`
	Status          string    `json:"status" db:"status"`         // draft/pending_review/active/offline
	RiskLevel       string    `json:"risk_level" db:"risk_level"` // low/medium/high
	IsPublic        bool      `json:"is_public" db:"is_public"`
	DataAccessScope string    `json:"data_access_scope" db:"data_access_scope"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
}

// APIKey 调用凭证（哈希存储，明文仅创建时返回一次）。
type APIKey struct {
	ID             string     `json:"id" db:"id"`
	ProjectSpaceID string     `json:"project_space_id" db:"project_space_id"`
	AppName        string     `json:"app_name" db:"app_name"`
	KeyHash        string     `json:"-" db:"key_hash"`                    // SHA256，不序列化
	KeyPrefix      string     `json:"key_prefix" db:"key_prefix"`         // 展示用前缀
	AllowedSkills  string     `json:"allowed_skills" db:"allowed_skills"` // 逗号分隔 skill code，空=全部
	Scope          string     `json:"scope" db:"scope"`                   // read/write/admin
	Status         string     `json:"status" db:"status"`                 // active/revoked
	ExpiresAt      *time.Time `json:"expires_at,omitempty" db:"expires_at"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
}

// CapabilityUsage 能力调用用量记录。
type CapabilityUsage struct {
	ID             string    `json:"id" db:"id"`
	ProjectSpaceID string    `json:"project_space_id" db:"project_space_id"`
	APIKeyID       string    `json:"api_key_id" db:"api_key_id"`
	CallerApp      string    `json:"caller_app" db:"caller_app"`
	SkillID        string    `json:"skill_id" db:"skill_id"`
	InputTokens    int       `json:"input_tokens" db:"input_tokens"`
	OutputTokens   int       `json:"output_tokens" db:"output_tokens"`
	Success        bool      `json:"success" db:"success"`
	LatencyMS      int       `json:"latency_ms" db:"latency_ms"`
	RenderHint     string    `json:"render_hint" db:"render_hint"`
	TraceID        string    `json:"trace_id" db:"trace_id"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
}

// DomainAgent 领域 Agent（复合能力注册）。
type DomainAgent struct {
	ID             string    `json:"id" db:"id"`
	ProjectSpaceID string    `json:"project_space_id" db:"project_space_id"`
	Code           string    `json:"code" db:"code"`
	Name           string    `json:"name" db:"name"`
	Domain         string    `json:"domain" db:"domain"`                   // finance/hr/customer_service/operations/legal/custom
	ComposedSkills string    `json:"composed_skills" db:"composed_skills"` // 逗号分隔
	Status         string    `json:"status" db:"status"`                   // draft/active/offline
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}

// SkillUsageStat 按技能聚合的用量。
type SkillUsageStat struct {
	SkillID      string `json:"skill_id" db:"skill_id"`
	Calls        int    `json:"calls" db:"calls"`
	InputTokens  int    `json:"input_tokens" db:"input_tokens"`
	OutputTokens int    `json:"output_tokens" db:"output_tokens"`
	SuccessCount int    `json:"success_count" db:"success_count"`
}

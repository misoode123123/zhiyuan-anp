// Package quota 是「项目配额」限界上下文 ——
// 4 维度强制（应用数 / 库数 / 库总大小 / 每日 AI 调用），建资源前 check，超限返回友好错误。
//
// 设计：独立模块，不导入 appdeploy/pgsupply/capability（避免循环依赖）。
// 通过 InstanceLookup / PGSizeChecker 接口注入 pgsupply 的能力（取实例 admin_url + 查库大小）。
// 计数直接 SQL 查各业务表（appdeploy_application/appdeploy_database/capability_usage）。
package quota

import "time"

// 默认配额（迁移 000003 定义；GetOrCreate 不存在时建默认用此）。
// P4（迁移 000035）加 max_dedicated_instances：dedicated 实例数默认 5/项目空间。
const (
	DefaultMaxApps                  = 20
	DefaultMaxDatabases             = 20
	DefaultMaxTotalDBMb             = 10240
	DefaultMaxCapabilityCallsPerDay = 10000
	DefaultMaxDedicatedInstances    = 5
)

// Quota 项目配额（5 维度上限）。
type Quota struct {
	ProjectSpaceID           string    `json:"project_space_id" db:"project_space_id"`
	MaxApps                  int       `json:"max_apps" db:"max_apps"`
	MaxDatabases             int       `json:"max_databases" db:"max_databases"`
	MaxTotalDBMb             int       `json:"max_total_db_mb" db:"max_total_db_mb"`
	MaxCapabilityCallsPerDay int       `json:"max_capability_calls_per_day" db:"max_capability_calls_per_day"`
	MaxDedicatedInstances    int       `json:"max_dedicated_instances" db:"max_dedicated_instances"`
	UpdatedAt                time.Time `json:"updated_at" db:"updated_at"`
}

// Usage 当前用量（limit + used 一起返回，前端直接渲染「X / Y」）。
type Usage struct {
	Quota                 Quota `json:"quota"`
	UsedApps              int   `json:"used_apps"`
	UsedDatabases         int   `json:"used_databases"`
	UsedDBSizeMb          int   `json:"used_db_size_mb"`
	UsedCapabilityToday   int   `json:"used_capability_today"`
	UsedDedicatedInstances int  `json:"used_dedicated_instances"`
}

// ---------------- 3c 用量趋势 ----------------

// AITrendPoint AI 调用单日聚合（capability_usage by day）。
type AITrendPoint struct {
	Day          time.Time `json:"day" db:"day"` // 当日 00:00 (created_at::date)
	Calls        int       `json:"calls" db:"calls"`
	InputTokens  int       `json:"input_tokens" db:"input_tokens"`
	OutputTokens int       `json:"output_tokens" db:"output_tokens"`
	AvgLatencyMs int       `json:"avg_latency_ms" db:"avg_latency_ms"`
	SuccessRate  float64   `json:"success_rate" db:"success_rate"` // 0-1
}

// TokensTotal 输入+输出 tokens 合计（前端摘要直接调）。
func (p AITrendPoint) TokensTotal() int { return p.InputTokens + p.OutputTokens }

// APITrendPoint 应用 API 调用单日聚合（appgw_access_log by day）。
type APITrendPoint struct {
	Day          time.Time `json:"day" db:"day"`
	Calls        int       `json:"calls" db:"calls"`
	AvgLatencyMs int       `json:"avg_latency_ms" db:"avg_latency_ms"`
	SuccessRate  float64   `json:"success_rate" db:"success_rate"` // status<400 占比
	ErrorCount   int       `json:"error_count" db:"error_count"`   // status>=400 次数
}

// DBSizeTrendPoint 库总大小单日趋势（db_size_snapshot 当日末值折 MB）。
type DBSizeTrendPoint struct {
	Day       time.Time `json:"day" db:"day"`
	SizeBytes int64     `json:"size_bytes" db:"size_bytes"`
	SizeMB    int       `json:"size_mb" db:"size_mb"` // 向上取整 MB
}

// UsageTrend 用量趋势响应（3c 看板数据源）：AI/API/库大小 三条日级趋势 + 当前用量。
type UsageTrend struct {
	Days            int                `json:"days"`
	AITrend         []AITrendPoint     `json:"ai_trend"`
	APITrend        []APITrendPoint    `json:"api_trend"`
	DBSizeTrend     []DBSizeTrendPoint `json:"db_size_trend"`
	DBSizeCurrentMB int                `json:"db_size_current_mb"` // 当前总大小（MB）
	Usage           *Usage             `json:"usage"`              // 复用 3a 当前用量（4 维度）
}

// 配额维度常量（ErrQuotaExceeded.Dimension 用）。
const (
	DimensionApps            = "apps"
	DimensionDatabases       = "databases"
	DimensionCapabilityToday = "capability_today"
	DimensionDBSize          = "db_size"
	DimensionDedicated       = "dedicated"
)

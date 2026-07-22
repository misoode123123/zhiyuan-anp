// Package quota 是「项目配额」限界上下文 ——
// 4 维度强制（应用数 / 库数 / 库总大小 / 每日 AI 调用），建资源前 check，超限返回友好错误。
//
// 设计：独立模块，不导入 appdeploy/pgsupply/capability（避免循环依赖）。
// 通过 InstanceLookup / PGSizeChecker 接口注入 pgsupply 的能力（取实例 admin_url + 查库大小）。
// 计数直接 SQL 查各业务表（appdeploy_application/appdeploy_database/capability_usage）。
package quota

import "time"

// 默认配额（迁移 000003 定义；GetOrCreate 不存在时建默认用此）。
const (
	DefaultMaxApps                  = 20
	DefaultMaxDatabases             = 20
	DefaultMaxTotalDBMb             = 10240
	DefaultMaxCapabilityCallsPerDay = 10000
)

// Quota 项目配额（4 维度上限）。
type Quota struct {
	ProjectSpaceID           string    `json:"project_space_id" db:"project_space_id"`
	MaxApps                  int       `json:"max_apps" db:"max_apps"`
	MaxDatabases             int       `json:"max_databases" db:"max_databases"`
	MaxTotalDBMb             int       `json:"max_total_db_mb" db:"max_total_db_mb"`
	MaxCapabilityCallsPerDay int       `json:"max_capability_calls_per_day" db:"max_capability_calls_per_day"`
	UpdatedAt                time.Time `json:"updated_at" db:"updated_at"`
}

// Usage 当前用量（limit + used 一起返回，前端直接渲染「X / Y」）。
type Usage struct {
	Quota               Quota `json:"quota"`
	UsedApps            int   `json:"used_apps"`
	UsedDatabases       int   `json:"used_databases"`
	UsedDBSizeMb        int   `json:"used_db_size_mb"`
	UsedCapabilityToday int   `json:"used_capability_today"`
}

// 配额维度常量（ErrQuotaExceeded.Dimension 用）。
const (
	DimensionApps            = "apps"
	DimensionDatabases       = "databases"
	DimensionCapabilityToday = "capability_today"
	DimensionDBSize          = "db_size"
)

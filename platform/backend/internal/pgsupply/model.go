// Package pgsupply 是「应用数据库供给」限界上下文 ——
// 每个项目空间一个独立 PG 实例，平台纳管；为每个应用在其上建独立 database + 专用 role，
// 生成直连 DATABASE_URL（无 pgbouncer）注入应用容器。
//
// 工作模型：InstanceManager 取/建项目 PG 实例（managed:docker 起容器 / external:纳管远程）
// → Provisioner 在实例上建库+role → 写 appdeploy_database + DATABASE_URL env。
package pgsupply

import "time"

// 部署模式。
const (
	DeployManaged  = "managed"  // 平台 docker 起容器
	DeployExternal = "external" // 纳管远程已有 PG
)

// 实例/库状态。
const (
	StatusActive       = "active"       // 实例可用
	StatusDraining     = "draining"     // 实例下线中（不建新库）
	StatusProvisioning = "provisioning" // 库供给中
	StatusReady        = "ready"        // 库就绪
	StatusFailed       = "failed"       // 供给失败
	StatusDeleted      = "deleted"      // 已删除
)

// 端口段（PG 容器宿主映射；避开 app test/prod 与 opencode 段）。
const (
	pgPortMin      = 9500
	pgPortMax      = 9599
	pgImage        = "pgvector/pgvector:pg16"
	pgInternalPort = 5432
)

// PGInstance 一个项目的独立 PG 实例（默认每项目 1 个）。
type PGInstance struct {
	ID             string    `json:"id" db:"id"`
	ProjectSpaceID string    `json:"project_space_id" db:"project_space_id"`
	Host           string    `json:"host" db:"host"`
	Port           int       `json:"port" db:"port"`
	AdminURLRef    string    `json:"-" db:"admin_url_ref"` // superuser 连接串(含密码)，不序列化
	DeployMode     string    `json:"deploy_mode" db:"deploy_mode"`
	Status         string    `json:"status" db:"status"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
}

// AppDatabase 应用库供给记录（库元数据 + 生命周期）。
type AppDatabase struct {
	ID             string     `json:"id" db:"id"`
	AppID          string     `json:"app_id" db:"app_id"`
	ProjectSpaceID string     `json:"project_space_id" db:"project_space_id"`
	DBName         string     `json:"db_name" db:"db_name"`
	DBRole         string     `json:"db_role" db:"db_role"`
	PGInstanceID   string     `json:"pg_instance_id" db:"pg_instance_id"`
	DBHost         string     `json:"db_host" db:"db_host"`
	DBPort         int        `json:"db_port" db:"db_port"`
	Status         string     `json:"status" db:"status"`
	LastError      string     `json:"last_error,omitempty" db:"last_error"`
	BackupEnabled  bool       `json:"backup_enabled" db:"backup_enabled"`
	LastBackupAt   *time.Time `json:"last_backup_at,omitempty" db:"last_backup_at"`
	SchemaVersion  string     `json:"schema_version,omitempty" db:"schema_version"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at" db:"updated_at"`
}

package pgsupply

import (
	"context"
	"database/sql"
	"time"

	"github.com/jmoiron/sqlx"
)

// Store pgsupply 数据访问（pg_instance + appdeploy_database）。
type Store struct{ db *sqlx.DB }

// NewStore 构造。
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

// instanceCols 显式列（COALESCE 处理可空；container_name 迁移 000005 加，历史行可能 NULL → 空串）。
const instanceCols = `id, project_space_id, host, port, admin_url_ref, deploy_mode, status,
	COALESCE(container_name,'') AS container_name, created_at`

// CreateInstance 写一条实例记录。partial unique index uq_pginstance_ps_active
// 在同项目已有 active 实例时冲突（错误码 23505）——上层 InstanceManager.GetOrCreate 捕获后重查复用。
func (s *Store) CreateInstance(ctx context.Context, ins *PGInstance) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO pg_instance (id, project_space_id, host, port, admin_url_ref, deploy_mode, status, container_name)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		ins.ID, ins.ProjectSpaceID, ins.Host, ins.Port, ins.AdminURLRef, ins.DeployMode, ins.Status, ins.ContainerName)
	return err
}

// ListInstancesByProject 列某项目的全部实例（任意状态，按创建倒序）。
// 删项目级联清理用：TeardownForProject 遍历逐个 docker rm + 删记录。
func (s *Store) ListInstancesByProject(ctx context.Context, psID string) ([]PGInstance, error) {
	var list []PGInstance
	err := s.db.SelectContext(ctx, &list,
		`SELECT `+instanceCols+` FROM pg_instance WHERE project_space_id=$1 ORDER BY created_at DESC`,
		psID)
	return list, err
}

// DeleteInstance 按 id 删实例记录（级联删 appdeploy_database 由 FK ON DELETE CASCADE 兜底；本方法只删 pg_instance）。
func (s *Store) DeleteInstance(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM pg_instance WHERE id=$1`, id)
	return err
}

// GetInstanceByProject 取项目首个 active 实例（每项目默认 1 个）。无则返回 nil,nil。
func (s *Store) GetInstanceByProject(ctx context.Context, psID string) (*PGInstance, error) {
	var ins PGInstance
	err := s.db.GetContext(ctx, &ins,
		`SELECT `+instanceCols+` FROM pg_instance WHERE project_space_id=$1 AND status=$2 LIMIT 1`,
		psID, StatusActive)
	if err != nil {
		return nil, err // sql.ErrNoRows 向上传递
	}
	return &ins, nil
}

// GetInstance 按 id 取（任意状态）。
func (s *Store) GetInstance(ctx context.Context, id string) (*PGInstance, error) {
	var ins PGInstance
	err := s.db.GetContext(ctx, &ins,
		`SELECT `+instanceCols+` FROM pg_instance WHERE id=$1`, id)
	if err != nil {
		return nil, err
	}
	return &ins, nil
}

// appDBCols 显式列。
const appDBCols = `id, app_id, project_space_id, db_name, db_role, pg_instance_id, db_host, db_port, status,
	COALESCE(last_error,'') AS last_error, backup_enabled, last_backup_at,
	COALESCE(schema_version,'') AS schema_version, size_bytes, created_at, updated_at`

// CreateAppDB 写一条应用库记录。
func (s *Store) CreateAppDB(ctx context.Context, ad *AppDatabase) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO appdeploy_database
		 (id, app_id, project_space_id, db_name, db_role, pg_instance_id, db_host, db_port, status, backup_enabled)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		ad.ID, ad.AppID, ad.ProjectSpaceID, ad.DBName, ad.DBRole, ad.PGInstanceID,
		ad.DBHost, ad.DBPort, ad.Status, ad.BackupEnabled)
	return err
}

// GetAppDBByApp 按应用 id 取库记录。无则返回 nil,nil。
func (s *Store) GetAppDBByApp(ctx context.Context, appID string) (*AppDatabase, error) {
	var ad AppDatabase
	err := s.db.GetContext(ctx, &ad,
		`SELECT `+appDBCols+` FROM appdeploy_database WHERE app_id=$1 AND status<>$2`,
		appID, StatusDeleted)
	if err != nil {
		return nil, err
	}
	return &ad, nil
}

// SetAppDBStatus 更新库状态 + 错误。
func (s *Store) SetAppDBStatus(ctx context.Context, id, status, lastErr string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE appdeploy_database SET status=$1, last_error=$2, updated_at=CURRENT_TIMESTAMP WHERE id=$3`,
		status, lastErr, id)
	return err
}

// DeleteAppDB 标记删除（status=deleted，保留记录便审计）。
func (s *Store) DeleteAppDB(ctx context.Context, appID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE appdeploy_database SET status=$1, updated_at=CURRENT_TIMESTAMP WHERE app_id=$2 AND status<>$1`,
		StatusDeleted, appID)
	return err
}

// SetAppDBBackup 记录最近备份时间。
func (s *Store) SetAppDBBackup(ctx context.Context, id string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE appdeploy_database SET last_backup_at=$1, updated_at=CURRENT_TIMESTAMP WHERE id=$2`,
		at, id)
	return err
}

// ListInstances 列所有 PG 实例（按创建时间倒序）。
func (s *Store) ListInstances(ctx context.Context) ([]PGInstance, error) {
	var list []PGInstance
	err := s.db.SelectContext(ctx, &list,
		`SELECT `+instanceCols+` FROM pg_instance ORDER BY created_at DESC`)
	return list, err
}

// ListAppDBs 列应用库；psID 非空则按项目过滤，否则全部（排除已删除，按创建时间倒序）。
func (s *Store) ListAppDBs(ctx context.Context, psID string) ([]AppDatabase, error) {
	var list []AppDatabase
	if psID == "" {
		err := s.db.SelectContext(ctx, &list,
			`SELECT `+appDBCols+` FROM appdeploy_database WHERE status<>$1 ORDER BY created_at DESC`,
			StatusDeleted)
		return list, err
	}
	err := s.db.SelectContext(ctx, &list,
		`SELECT `+appDBCols+` FROM appdeploy_database WHERE project_space_id=$1 AND status<>$2 ORDER BY created_at DESC`,
		psID, StatusDeleted)
	return list, err
}

// ListAppDBsByInstance 按实例列应用库（排除已删除）。CollectDBSizes 按实例分组查 size 用。
func (s *Store) ListAppDBsByInstance(ctx context.Context, instanceID string) ([]AppDatabase, error) {
	var list []AppDatabase
	err := s.db.SelectContext(ctx, &list,
		`SELECT `+appDBCols+` FROM appdeploy_database WHERE pg_instance_id=$1 AND status<>$2 ORDER BY created_at DESC`,
		instanceID, StatusDeleted)
	return list, err
}

// UpdateAppDBSize 更新库大小字节（CollectDBSizes 每 tick 调）。
func (s *Store) UpdateAppDBSize(ctx context.Context, id string, sizeBytes int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE appdeploy_database SET size_bytes=$1, updated_at=CURRENT_TIMESTAMP WHERE id=$2`,
		sizeBytes, id)
	return err
}

// ListActiveInstances 列所有 active 实例（CollectDBSizes 遍历用）。
func (s *Store) ListActiveInstances(ctx context.Context) ([]PGInstance, error) {
	var list []PGInstance
	err := s.db.SelectContext(ctx, &list,
		`SELECT `+instanceCols+` FROM pg_instance WHERE status=$1 ORDER BY created_at DESC`,
		StatusActive)
	return list, err
}

// ProjectTotalSizeMb 按项目聚合所有非 deleted 库的 size_bytes 总和（折 MB 向上取整）。
// CollectDBSizes 告警用：直接读 size_bytes 列（已采），不连实例。
func (s *Store) ProjectTotalSizeMb(ctx context.Context, psID string) (int64, error) {
	var bytes int64
	err := s.db.GetContext(ctx, &bytes,
		`SELECT COALESCE(SUM(size_bytes),0) FROM appdeploy_database WHERE project_space_id=$1 AND status<>$2`,
		psID, StatusDeleted)
	if err != nil {
		return 0, err
	}
	const mb = 1024 * 1024
	return (bytes + mb - 1) / mb, nil
}

// MaxTotalDBMb 取项目的库总大小上限。行不存在 → 0（调用方按「无配额」处理，不告警）。
func (s *Store) MaxTotalDBMb(ctx context.Context, psID string) (int, error) {
	var mb int
	err := s.db.GetContext(ctx, &mb,
		`SELECT max_total_db_mb FROM project_quota WHERE project_space_id=$1`, psID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return mb, err
}

// actionLogCols 显式列（COALESCE 处理可空 error/trace_id）。
const actionLogCols = `id, project_space_id, app_id, db_name, actor, action_type, statement, row_count, status,
	COALESCE(error,'') AS error, COALESCE(trace_id,'') AS trace_id, created_at`

// actionLogStmtMax 单条 SQL 审计记录最大长度（截断防超长 TEXT 也防日志爆）。
const actionLogStmtMax = 5000

// CreateActionLog 写一条数据库操作审计日志。id 由调用方传（"dal_"+uuid）；
// statement 超过 5000 字符截断。
func (s *Store) CreateActionLog(ctx context.Context, al *ActionLog) error {
	stmt := al.Statement
	if len(stmt) > actionLogStmtMax {
		stmt = stmt[:actionLogStmtMax]
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO db_action_log (id, project_space_id, app_id, db_name, actor, action_type, statement, row_count, status, error, trace_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		al.ID, al.ProjectSpaceID, al.AppID, al.DBName, al.Actor, al.ActionType, stmt,
		al.RowCount, al.Status, al.Error, al.TraceID)
	return err
}

// ListActionLogs 列某应用最近的 SQL 审计日志（按时间倒序）。
// limit<=0 或 >200 取 50（防止前端一次拉爆）。
func (s *Store) ListActionLogs(ctx context.Context, appID string, limit int) ([]ActionLog, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var list []ActionLog
	err := s.db.SelectContext(ctx, &list,
		`SELECT `+actionLogCols+` FROM db_action_log WHERE app_id=$1 ORDER BY created_at DESC LIMIT $2`,
		appID, limit)
	return list, err
}

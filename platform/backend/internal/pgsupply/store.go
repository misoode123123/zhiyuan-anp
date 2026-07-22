package pgsupply

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
)

// Store pgsupply 数据访问（pg_instance + appdeploy_database）。
type Store struct{ db *sqlx.DB }

// NewStore 构造。
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

// instanceCols 显式列（COALESCE 处理可空）。
const instanceCols = `id, project_space_id, host, port, admin_url_ref, deploy_mode, status, created_at`

// CreateInstance 写一条实例记录。
func (s *Store) CreateInstance(ctx context.Context, ins *PGInstance) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO pg_instance (id, project_space_id, host, port, admin_url_ref, deploy_mode, status)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		ins.ID, ins.ProjectSpaceID, ins.Host, ins.Port, ins.AdminURLRef, ins.DeployMode, ins.Status)
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
	COALESCE(schema_version,'') AS schema_version, created_at, updated_at`

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

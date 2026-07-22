package quota

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jmoiron/sqlx"
)

// Store project_quota CRUD。
type Store struct {
	db *sqlx.DB
}

// NewStore 构造。
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

// quotaCols 显式列。
const quotaCols = `project_space_id, max_apps, max_databases, max_total_db_mb, max_capability_calls_per_day, updated_at`

// Get 按项目取配额。不存在返回 nil,nil（语义：调用方决定是 GetOrCreate 还是新建默认）。
func (s *Store) Get(ctx context.Context, psID string) (*Quota, error) {
	var q Quota
	err := s.db.GetContext(ctx, &q,
		`SELECT `+quotaCols+` FROM project_quota WHERE project_space_id=$1`, psID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &q, nil
}

// GetOrCreate 取配额，不存在则建默认（应用数 20 / 库数 20 / 10GB / 10000 次每日）。
// 并发兜底：用 INSERT ... ON CONFLICT DO NOTHING；冲突时另一 goroutine 已建 → 重查返回。
func (s *Store) GetOrCreate(ctx context.Context, psID string) (*Quota, error) {
	if q, err := s.Get(ctx, psID); err != nil {
		return nil, err
	} else if q != nil {
		return q, nil
	}
	// 建默认（不存在）。ON CONFLICT 兜底并发：两 goroutine 同时进此分支时，后到者冲突 → returning 拿到先到者建的行
	var q Quota
	err := s.db.QueryRowxContext(ctx,
		`INSERT INTO project_quota (project_space_id, max_apps, max_databases, max_total_db_mb, max_capability_calls_per_day)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (project_space_id) DO UPDATE SET project_space_id=EXCLUDED.project_space_id
		 RETURNING `+quotaCols,
		psID, DefaultMaxApps, DefaultMaxDatabases, DefaultMaxTotalDBMb, DefaultMaxCapabilityCallsPerDay).StructScan(&q)
	if err != nil {
		return nil, err
	}
	return &q, nil
}

// Set 更新配额（4 个 max_*）。project_space_id 不存在 → ErrNotExists（调用方先 GetOrCreate）。
func (s *Store) Set(ctx context.Context, psID string, maxApps, maxDatabases, maxTotalDBMb, maxCapabilityCallsPerDay int) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE project_quota
		 SET max_apps=$1, max_databases=$2, max_total_db_mb=$3, max_capability_calls_per_day=$4, updated_at=CURRENT_TIMESTAMP
		 WHERE project_space_id=$5`,
		maxApps, maxDatabases, maxTotalDBMb, maxCapabilityCallsPerDay, psID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotExists
	}
	return nil
}

// ErrNotExists 配额行不存在（需先 GetOrCreate）。
var ErrNotExists = errors.New("project_quota 不存在，请先 GetOrCreate")

package compute

import (
	"context"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Store 用量数据访问。
type Store struct {
	db *sqlx.DB
}

// NewStore 构造 Store。
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

// Create 记录一次用量。
func (s *Store) Create(ctx context.Context, u *UsageRecord) error {
	u.ID = "usg_" + uuid.NewString()[:20]
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO usage_record (id, project_space_id, model, kind, prompt_tokens, completion_tokens, total_tokens)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		u.ID, u.ProjectSpaceID, u.Model, u.Kind, u.PromptTokens, u.CompletionTokens, u.TotalTokens)
	return err
}

// List 列出项目空间的用量记录。
func (s *Store) List(ctx context.Context, projectSpaceID string) ([]UsageRecord, error) {
	var list []UsageRecord
	err := s.db.SelectContext(ctx, &list,
		`SELECT id, project_space_id, model, kind, prompt_tokens, completion_tokens, total_tokens, created_at
		 FROM usage_record WHERE project_space_id = $1 ORDER BY created_at DESC LIMIT 200`, projectSpaceID)
	return list, err
}

// Stats 聚合统计（总 token + 按模型）。
func (s *Store) Stats(ctx context.Context, projectSpaceID string) (*Stats, error) {
	st := &Stats{}
	var tot struct {
		T int `db:"t"`
		C int `db:"c"`
	}
	if err := s.db.GetContext(ctx, &tot,
		`SELECT COALESCE(SUM(total_tokens),0) AS t, COUNT(*) AS c FROM usage_record WHERE project_space_id = $1`,
		projectSpaceID); err != nil {
		return nil, err
	}
	st.TotalTokens = tot.T
	st.TotalCalls = tot.C
	var ms []ModelStat
	if err := s.db.SelectContext(ctx, &ms,
		`SELECT model, COALESCE(SUM(total_tokens),0) AS tokens, COUNT(*) AS calls
		 FROM usage_record WHERE project_space_id = $1 GROUP BY model`, projectSpaceID); err != nil {
		return nil, err
	}
	st.ByModel = ms
	return st, nil
}

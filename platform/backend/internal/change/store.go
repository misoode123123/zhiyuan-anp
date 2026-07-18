package change

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Store 变更审批数据访问。
type Store struct {
	db *sqlx.DB
}

// NewStore 构造 Store。
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

// chgCols 显式列（可空文本列 COALESCE 防 NULL→string 扫描错误）。
const chgCols = `id, project_space_id, COALESCE(kind,'') AS kind, COALESCE(source_id,'') AS source_id, COALESCE(repo_dir,'') AS repo_dir, COALESCE(prompt,'') AS prompt, COALESCE(model,'') AS model, COALESCE(output,'') AS output, status, reviewer, reviewed_at, created_at`

// Create 登记一条待审批变更。
func (s *Store) Create(ctx context.Context, c *ChangeRequest) error {
	c.ID = "chg_" + uuid.NewString()[:20]
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO change_request (id, project_space_id, kind, source_id, repo_dir, prompt, model, output, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending')`,
		c.ID, c.ProjectSpaceID, c.Kind, c.SourceID, c.RepoDir, c.Prompt, c.Model, c.Output)
	return err
}

// Get 读取单条变更。
func (s *Store) Get(ctx context.Context, id string) (*ChangeRequest, error) {
	var c ChangeRequest
	err := s.db.GetContext(ctx, &c, `SELECT `+chgCols+` FROM change_request WHERE id = ?`, id)
	return &c, err
}

// List 列出变更（status 为空则全部）。
func (s *Store) List(ctx context.Context, status string) ([]ChangeRequest, error) {
	var list []ChangeRequest
	q := `SELECT ` + chgCols + ` FROM change_request`
	args := []interface{}{}
	if status != "" {
		q += ` WHERE status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC`
	err := s.db.SelectContext(ctx, &list, q, args...)
	return list, err
}

// HasAny 该 source（应用/需求）是否登记过变更——grandfather：未登记过的不受 promote 闸门约束。
func (s *Store) HasAny(ctx context.Context, sourceID string) (bool, error) {
	var c int
	err := s.db.GetContext(ctx, &c, `SELECT COUNT(*) FROM change_request WHERE source_id = ?`, sourceID)
	return c > 0, err
}

// HasApproved 该 source 是否有已批准变更（promote 闸门放行条件）。
func (s *Store) HasApproved(ctx context.Context, sourceID string) (bool, error) {
	var c int
	err := s.db.GetContext(ctx, &c, `SELECT COUNT(*) FROM change_request WHERE source_id = ? AND status = 'approved'`, sourceID)
	return c > 0, err
}

// Decide 审批决定（approved / rejected）。
func (s *Store) Decide(ctx context.Context, id, decision, reviewer string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE change_request SET status = ?, reviewer = ?, reviewed_at = ? WHERE id = ? AND status = 'pending'`,
		decision, reviewer, time.Now(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errNotPending
	}
	return nil
}

// MarkReleased 把某应用( source_id)的所有 approved 变更标记为 released(已上线,从待上线消失)。
func (s *Store) MarkReleased(ctx context.Context, sourceID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE change_request SET status = 'released' WHERE source_id = ? AND status = 'approved'`,
		sourceID)
	return err
}

// errNotPending 非 pending 状态不可审批。
var errNotPending = errorString("变更非待审状态，不可审批")

type errorString string

func (e errorString) Error() string { return string(e) }

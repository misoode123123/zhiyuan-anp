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
// c. 前缀:LEFT JOIN 后列名需消歧;app_name 派生自 source_id→app 或 source_id→requirement→app。
const chgCols = `c.id, c.project_space_id, COALESCE(c.kind,'') AS kind, COALESCE(c.source_id,'') AS source_id, COALESCE(c.repo_dir,'') AS repo_dir, COALESCE(c.prompt,'') AS prompt, COALESCE(c.model,'') AS model, COALESCE(c.output,'') AS output, c.status, c.reviewer, c.reviewed_at, c.created_at, COALESCE(a.name,'') AS app_name`

// chgFrom change_request LEFT JOIN appdeploy_application(双路径:source_id 直接是 app_id,或经 requirement.application_id)。
const chgFrom = ` FROM change_request c
 LEFT JOIN appdeploy_application a
   ON a.id = c.source_id
   OR a.id IN (SELECT application_id FROM requirement WHERE id = c.source_id)`

// Create 登记一条待审批变更。
func (s *Store) Create(ctx context.Context, c *ChangeRequest) error {
	c.ID = "chg_" + uuid.NewString()[:20]
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO change_request (id, project_space_id, kind, source_id, repo_dir, prompt, model, output, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending')`,
		c.ID, c.ProjectSpaceID, c.Kind, c.SourceID, c.RepoDir, c.Prompt, c.Model, c.Output)
	return err
}

// Get 读取单条变更。
func (s *Store) Get(ctx context.Context, id string) (*ChangeRequest, error) {
	var c ChangeRequest
	err := s.db.GetContext(ctx, &c, `SELECT `+chgCols+chgFrom+` WHERE c.id = $1`, id)
	return &c, err
}

// List 列出变更（status 为空则全部）。
func (s *Store) List(ctx context.Context, status string) ([]ChangeRequest, error) {
	var list []ChangeRequest
	q := `SELECT ` + chgCols + chgFrom
	args := []interface{}{}
	if status != "" {
		q += ` WHERE c.status = $1`
		args = append(args, status)
	}
	q += ` ORDER BY c.created_at DESC`
	err := s.db.SelectContext(ctx, &list, q, args...)
	return list, err
}

// HasAny 该 source（应用/需求）是否登记过变更——grandfather：未登记过的不受 promote 闸门约束。
func (s *Store) HasAny(ctx context.Context, sourceID string) (bool, error) {
	var c int
	err := s.db.GetContext(ctx, &c, `SELECT COUNT(*) FROM change_request WHERE source_id = $1`, sourceID)
	return c > 0, err
}

// HasApproved 该 source 是否有已批准变更（promote 闸门放行条件）。
func (s *Store) HasApproved(ctx context.Context, sourceID string) (bool, error) {
	var c int
	err := s.db.GetContext(ctx, &c, `SELECT COUNT(*) FROM change_request WHERE source_id = $1 AND status = 'approved'`, sourceID)
	return c > 0, err
}

// Decide 审批决定（approved / rejected）。
func (s *Store) Decide(ctx context.Context, id, decision, reviewer string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE change_request SET status = $1, reviewer = $2, reviewed_at = $3 WHERE id = $4 AND status = 'pending'`,
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
		`UPDATE change_request SET status = 'released' WHERE source_id = $1 AND status = 'approved'`,
		sourceID)
	return err
}

// errNotPending 非 pending 状态不可审批。
var errNotPending = errorString("变更非待审状态，不可审批")

type errorString string

func (e errorString) Error() string { return string(e) }

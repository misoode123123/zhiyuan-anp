package requirement

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// Repository 需求数据访问。
type Repository struct {
	db *sqlx.DB
}

// NewRepository 构造 Repository。
func NewRepository(db *sqlx.DB) *Repository { return &Repository{db: db} }

// reqCols 显式列（application_id 可空，用 COALESCE 避免 NULL→string 扫描错误）。
const reqCols = `id, project_space_id, COALESCE(application_id,'') AS application_id, title, COALESCE(description,'') AS description, COALESCE(user_story,'') AS user_story, COALESCE(acceptance_criteria,'') AS acceptance_criteria, status, COALESCE(priority,'') AS priority, COALESCE(fixed_version,'') AS fixed_version, COALESCE(tasks,'') AS tasks, COALESCE(assignee,'') AS assignee, assigned_at, created_at, updated_at`

// UpdateTasks 更新需求的子任务清单(JSON)。
func (r *Repository) UpdateTasks(ctx context.Context, id, tasks string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE requirement SET tasks = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2`, tasks, id)
	return err
}

// Assign 认领需求(互斥):已被他人认赖则返回错误(含当前认领人)。
func (r *Repository) Assign(ctx context.Context, id, user string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE requirement SET assignee = $1, assigned_at = CURRENT_TIMESTAMP, status = 'developing', updated_at = CURRENT_TIMESTAMP
		 WHERE id = $2 AND (assignee IS NULL OR assignee = '' OR assignee = $3)`,
		user, id, user)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var cur string
		_ = r.db.GetContext(ctx, &cur, `SELECT COALESCE(assignee,'') FROM requirement WHERE id = $1`, id)
		return fmt.Errorf("需求已被 %s 认领", cur)
	}
	return nil
}

// Release 释放认领(合并/完成/手动)。
func (r *Repository) Release(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE requirement SET assignee = '', assigned_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = $1`, id)
	return err
}

func (r *Repository) Create(ctx context.Context, req *Requirement) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO requirement (id, project_space_id, application_id, title, description, user_story, acceptance_criteria, status, priority, fixed_version)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		req.ID, req.ProjectSpaceID, req.ApplicationID, req.Title, req.Description, req.UserStory, req.AcceptanceCriteria, req.Status, req.Priority, req.FixedVersion)
	return err
}

func (r *Repository) List(ctx context.Context, projectSpaceID string) ([]Requirement, error) {
	var list []Requirement
	err := r.db.SelectContext(ctx, &list,
		`SELECT `+reqCols+` FROM requirement WHERE project_space_id = $1 ORDER BY created_at DESC`, projectSpaceID)
	return list, err
}

// ListByApp 列出某应用下的需求（应用一等公民：应用拥有需求池）。
func (r *Repository) ListByApp(ctx context.Context, appID string) ([]Requirement, error) {
	var list []Requirement
	err := r.db.SelectContext(ctx, &list,
		`SELECT `+reqCols+` FROM requirement WHERE application_id = $1 ORDER BY created_at DESC`, appID)
	return list, err
}

func (r *Repository) Get(ctx context.Context, id string) (*Requirement, error) {
	var req Requirement
	err := r.db.GetContext(ctx, &req, `SELECT `+reqCols+` FROM requirement WHERE id = $1`, id)
	return &req, err
}

// UpdateStatus 更新需求状态（发布后→delivered，闭环需求生命周期）。
// 返回受影响行数：调用方（发布回写）据此判断 source_id 是否真解析到需求，
// 避免匹配 0 行时静默无效却谎报「已交付」（见 PRD 2026-07-26 主线闭环收敛 3.3）。
func (r *Repository) UpdateStatus(ctx context.Context, id, status string) (int, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE requirement SET status = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2`, status, id)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// HasUnDeliveredApprovedByApp 该 app 是否存在「approved 变更但来源需求未 delivered」的情形。
// promote 闸门(AC7)据此拒绝「跳过 release 直接上线」。
//
// SQL 要点:
//   - JOIN requirement r ON r.id=c.source_id:source_id 为旧 appID 路径时 JOIN 不到(r=NULL),
//     r.status<>delivered 不命中 → 天然 grandfather(对称 release 回写)。
//   - appSourceCond 双路径:c.source_id=appID 或 c.source_id 是该 app 的需求(requirement.application_id=appID)。
//   - 多个 approved 变更取并集,任一来源需求未 delivered 即 true(EXISTS 天然语义)。
func (r *Repository) HasUnDeliveredApprovedByApp(ctx context.Context, appID string) (bool, error) {
	var exists bool
	const q = `SELECT EXISTS (
		SELECT 1 FROM change_request c
		JOIN requirement r ON r.id = c.source_id
		WHERE (c.source_id = $1 OR c.source_id IN (SELECT id FROM requirement WHERE application_id = $1))
		  AND c.status = 'approved'
		  AND r.status <> 'delivered')`
	err := r.db.GetContext(ctx, &exists, q, appID)
	return exists, err
}

// SetApplication 把需求归属到某应用（发布自动部署后回填 application_id）。
func (r *Repository) SetApplication(ctx context.Context, id, appID string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE requirement SET application_id = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2`, appID, id)
	return err
}

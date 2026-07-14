package ops

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Store 运维中心数据访问（告警/SOP + 跨表看板聚合）。
type Store struct {
	db *sqlx.DB
}

// NewStore 构造 Store。
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

// fingerprint 由来源+标题生成去重指纹（同源同类聚合）。
func fingerprint(source, title string) string {
	h := sha1.Sum([]byte(source + "|" + title))
	return "fp_" + hex.EncodeToString(h[:])[:12]
}

// ---------------- 告警 ----------------

// CreateAlert 新建告警（自动算 fingerprint）。
func (s *Store) CreateAlert(ctx context.Context, a *Alert) error {
	a.ID = "alt_" + uuid.NewString()[:20]
	a.Fingerprint = fingerprint(a.Source, a.Title)
	if a.Status == "" {
		a.Status = "firing"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ops_alert (id, project_space_id, source, severity, status, fingerprint, title, description, fired_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		a.ID, a.ProjectSpaceID, a.Source, a.Severity, a.Status, a.Fingerprint, a.Title, a.Description)
	return err
}

// ListAlerts 告警列表（severity/status 可选过滤）。
func (s *Store) ListAlerts(ctx context.Context, psID, severity, status string) ([]Alert, error) {
	q := `SELECT id, project_space_id, source, severity, status, fingerprint, title, description, fired_at, resolved_at, created_at
	      FROM ops_alert WHERE project_space_id = ?`
	args := []interface{}{psID}
	if severity != "" {
		q += ` AND severity = ?`
		args = append(args, severity)
	}
	if status != "" {
		q += ` AND status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY fired_at DESC LIMIT 200`
	var list []Alert
	err := s.db.SelectContext(ctx, &list, q, args...)
	return list, err
}

// ResolveAlert 标记告警已恢复。
func (s *Store) ResolveAlert(ctx context.Context, psID, id string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE ops_alert SET status='resolved', resolved_at=CURRENT_TIMESTAMP
		 WHERE id=? AND project_space_id=?`, id, psID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("告警 %s 不存在", id)
	}
	return nil
}

// HasFiringFingerprint 是否已存在同指纹的 firing 告警（巡检去重用）。
func (s *Store) HasFiringFingerprint(ctx context.Context, fp string) (bool, error) {
	var n int
	err := s.db.GetContext(ctx, &n, `SELECT COUNT(*) FROM ops_alert WHERE fingerprint=? AND status='firing'`, fp)
	return n > 0, err
}

// CountOpenAlerts 未恢复告警数。
func (s *Store) CountOpenAlerts(ctx context.Context, psID string) (int, error) {
	var n int
	err := s.db.GetContext(ctx, &n,
		`SELECT COUNT(*) FROM ops_alert WHERE project_space_id=? AND status='firing'`, psID)
	return n, err
}

// ---------------- SOP 预案 ----------------

func sopCols() string {
	return `id, project_space_id, code, name, description, category, risk_level, steps, rollback, requires_approval, status, created_at, updated_at`
}

// CreateSOP 新建 SOP。
func (s *Store) CreateSOP(ctx context.Context, sop *SOP) error {
	sop.ID = "sop_" + uuid.NewString()[:20]
	if sop.Status == "" {
		sop.Status = "draft"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ops_sop (id, project_space_id, code, name, description, category, risk_level, steps, rollback, requires_approval, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sop.ID, sop.ProjectSpaceID, sop.Code, sop.Name, sop.Description, sop.Category,
		sop.RiskLevel, sop.Steps, sop.Rollback, sop.RequiresApproval, sop.Status)
	return err
}

// ListSOPs SOP 列表（status 可选）。
func (s *Store) ListSOPs(ctx context.Context, psID, status string) ([]SOP, error) {
	q := `SELECT ` + sopCols() + ` FROM ops_sop WHERE project_space_id = ?`
	args := []interface{}{psID}
	if status != "" {
		q += ` AND status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC`
	var list []SOP
	err := s.db.SelectContext(ctx, &list, q, args...)
	return list, err
}

// GetSOP 取单条。
func (s *Store) GetSOP(ctx context.Context, psID, id string) (*SOP, error) {
	var sop SOP
	err := s.db.GetContext(ctx, &sop, `SELECT `+sopCols()+` FROM ops_sop WHERE id=? AND project_space_id=?`, id, psID)
	return &sop, err
}

// UpdateSOP 更新 SOP。
func (s *Store) UpdateSOP(ctx context.Context, sop *SOP) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE ops_sop SET code=?, name=?, description=?, category=?, risk_level=?, steps=?, rollback=?, requires_approval=?, status=?, updated_at=CURRENT_TIMESTAMP
		 WHERE id=? AND project_space_id=?`,
		sop.Code, sop.Name, sop.Description, sop.Category, sop.RiskLevel, sop.Steps, sop.Rollback, sop.RequiresApproval, sop.Status, sop.ID, sop.ProjectSpaceID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("SOP %s 不存在", sop.ID)
	}
	return nil
}

// DeleteSOP 删除 SOP。
func (s *Store) DeleteSOP(ctx context.Context, psID, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM ops_sop WHERE id=? AND project_space_id=?`, id, psID)
	return err
}

// CountActiveSOPs 激活态 SOP 数。
func (s *Store) CountActiveSOPs(ctx context.Context, psID string) (int, error) {
	var n int
	err := s.db.GetContext(ctx, &n, `SELECT COUNT(*) FROM ops_sop WHERE project_space_id=? AND status='active'`, psID)
	return n, err
}

// ---------------- 看板聚合 ----------------

func (s *Store) countByStatus(ctx context.Context, table, psID string) (map[string]int, error) {
	q := fmt.Sprintf(`SELECT status, COUNT(*) c FROM %s WHERE project_space_id=? GROUP BY status`, table)
	rows := []struct {
		Status string `db:"status"`
		C      int    `db:"c"`
	}{}
	if err := s.db.SelectContext(ctx, &rows, q, psID); err != nil {
		return nil, err
	}
	m := make(map[string]int, len(rows))
	for _, r := range rows {
		m[r.Status] = r.C
	}
	return m, nil
}

// Stats 运营计数统计。
func (s *Store) Stats(ctx context.Context, psID string) (Stats, error) {
	var st Stats
	reqs, err := s.countByStatus(ctx, "requirement", psID)
	if err != nil {
		return st, err
	}
	tasks, err := s.countByStatus(ctx, "code_task", psID)
	if err != nil {
		return st, err
	}
	changes, err := s.countByStatus(ctx, "change_request", psID)
	if err != nil {
		return st, err
	}
	st.Requirements, st.CodeTasks, st.Changes = reqs, tasks, changes
	if err := s.db.GetContext(ctx, &st.Releases,
		`SELECT COUNT(*) FROM release_record WHERE project_space_id=?`, psID); err != nil {
		return st, err
	}
	st.ActiveAlerts, err = s.CountOpenAlerts(ctx, psID)
	if err != nil {
		return st, err
	}
	st.ActiveSOPs, err = s.CountActiveSOPs(ctx, psID)
	return st, err
}

// Usage 算力用量摘要。
func (s *Store) Usage(ctx context.Context, psID string) (UsageSummary, error) {
	var u UsageSummary
	var agg struct {
		T int `db:"t"`
		C int `db:"c"`
	}
	err := s.db.GetContext(ctx, &agg,
		`SELECT COALESCE(SUM(total_tokens),0) t, COUNT(*) c FROM usage_record WHERE project_space_id=?`, psID)
	if err != nil {
		return u, err
	}
	u.TotalTokens, u.TotalCalls = agg.T, agg.C
	return u, nil
}

// Activity 跨表活动流（最近 20 条）。
func (s *Store) Activity(ctx context.Context, psID string) ([]ActivityItem, error) {
	q := `SELECT created_at time, 'requirement' kind, status action, COALESCE(title,'无标题') title, id ref_id FROM requirement WHERE project_space_id=?
	      UNION ALL
	      SELECT updated_at, 'code_task', status, COALESCE(SUBSTR(prompt,1,60),'编码任务'), id FROM code_task WHERE project_space_id=?
	      UNION ALL
	      SELECT created_at, 'change', status, COALESCE(SUBSTR(prompt,1,60),'变更'), id FROM change_request WHERE project_space_id=?
	      UNION ALL
	      SELECT created_at, 'release', 'released', version, id FROM release_record WHERE project_space_id=?
	      ORDER BY time DESC LIMIT 20`
	var list []ActivityItem
	err := s.db.SelectContext(ctx, &list, q, psID, psID, psID, psID)
	return list, err
}

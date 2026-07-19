package security

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Store 安全中心数据访问。
type Store struct {
	db       *sqlx.DB
	scanner  Scanner
}

// NewStore 构造 Store（默认 RegexScanner）。
func NewStore(db *sqlx.DB) *Store {
	return &Store{db: db, scanner: RegexScanner{}}
}

// ---------------- 扫描 ----------------

// RunScan 执行扫描并落库（scan_result + findings，事务）。
func (s *Store) RunScan(ctx context.Context, psID, content, scanType string) (*ScanResult, []Finding, error) {
	if scanType == "" {
		scanType = "full"
	}
	findings := s.scanner.Scan(content, scanType)

	res := &ScanResult{
		ID:             "scn_" + uuid.NewString()[:20],
		ProjectSpaceID: psID,
		ScanType:       scanType,
		RiskLevel:      maxSeverity(findings),
		TotalFindings:  len(findings),
		ContentPreview: preview(content),
	}
	for _, f := range findings {
		switch f.Severity {
		case "critical":
			res.CriticalCount++
		case "high":
			res.HighCount++
		case "medium":
			res.MediumCount++
		case "low":
			res.LowCount++
		}
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO security_scan_result (id, project_space_id, scan_type, risk_level, total_findings, critical_count, high_count, medium_count, low_count, content_preview)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		res.ID, res.ProjectSpaceID, res.ScanType, res.RiskLevel, res.TotalFindings,
		res.CriticalCount, res.HighCount, res.MediumCount, res.LowCount, res.ContentPreview); err != nil {
		return nil, nil, err
	}

	for i := range findings {
		f := &findings[i]
		f.ID = "vul_" + uuid.NewString()[:20]
		f.ProjectSpaceID = psID
		f.ScanResultID = res.ID
		f.Status = "open"
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO security_finding (id, project_space_id, scan_result_id, category, rule_id, severity, title, description, line_number, code_snippet, remediation, confidence, status)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
			f.ID, f.ProjectSpaceID, f.ScanResultID, f.Category, f.RuleID, f.Severity,
			f.Title, f.Description, f.LineNumber, f.CodeSnippet, f.Remediation, f.Confidence, f.Status); err != nil {
			return nil, nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return res, findings, nil
}

// ListFindings 发现列表（severity/status 可选）。
func (s *Store) ListFindings(ctx context.Context, psID, severity, status string) ([]Finding, error) {
	q := `SELECT id, project_space_id, scan_result_id, category, rule_id, severity, title, description, line_number, code_snippet, remediation, confidence, status, created_at, suppressed_at
	      FROM security_finding WHERE project_space_id = $1`
	args := []interface{}{psID}
	if severity != "" {
		q += ` AND severity = ?`
		args = append(args, severity)
	}
	if status != "" {
		q += ` AND status = ?`
		args = append(args, status)
	} else {
		q += ` AND status = 'open'` // 默认只看未处理
	}
	q += ` ORDER BY created_at DESC LIMIT 300`
	var list []Finding
	err := s.db.SelectContext(ctx, &list, q, args...)
	return list, err
}

// SuppressFinding 标记误报/抑制。
func (s *Store) SuppressFinding(ctx context.Context, psID, id string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE security_finding SET status='suppressed', suppressed_at=CURRENT_TIMESTAMP
		 WHERE id=$1 AND project_space_id=$2`, id, psID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("发现 %s 不存在", id)
	}
	return nil
}

// ---------------- 安全门 ----------------

// Gate 实时计算安全门（critical/high 未修复 → 阻断）。
func (s *Store) Gate(ctx context.Context, psID string) (Gate, error) {
	var g Gate
	err := s.db.GetContext(ctx, &g.CriticalOpen,
		`SELECT COUNT(*) FROM security_finding WHERE project_space_id=$1 AND status='open' AND severity='critical'`, psID)
	if err != nil {
		return g, err
	}
	if err := s.db.GetContext(ctx, &g.HighOpen,
		`SELECT COUNT(*) FROM security_finding WHERE project_space_id=$1 AND status='open' AND severity='high'`, psID); err != nil {
		return g, err
	}
	switch {
	case g.CriticalOpen > 0:
		g.OverallRiskLevel = "critical"
	case g.HighOpen > 0:
		g.OverallRiskLevel = "high"
	default:
		g.OverallRiskLevel = "clean"
	}
	g.GatePassed = g.CriticalOpen == 0 && g.HighOpen == 0
	if !g.GatePassed {
		g.BlockingReason = fmt.Sprintf("存在 %d 个 critical + %d 个 high 未修复发现", g.CriticalOpen, g.HighOpen)
	}
	return g, nil
}

// ---------------- 数据分级 ----------------

func dcCols() string {
	return `id, project_space_id, field_name, table_ref, sensitivity_level, data_type, masking_strategy, status, created_at`
}

// ListDC 数据分级列表。
func (s *Store) ListDC(ctx context.Context, psID string) ([]DataClassification, error) {
	var list []DataClassification
	err := s.db.SelectContext(ctx, &list,
		`SELECT `+dcCols()+` FROM security_data_classification WHERE project_space_id=$1 ORDER BY created_at DESC`, psID)
	return list, err
}

// CreateDC 新建数据分级。
func (s *Store) CreateDC(ctx context.Context, dc *DataClassification) error {
	dc.ID = "dc_" + uuid.NewString()[:20]
	if dc.Status == "" {
		dc.Status = "draft"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO security_data_classification (id, project_space_id, field_name, table_ref, sensitivity_level, data_type, masking_strategy, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		dc.ID, dc.ProjectSpaceID, dc.FieldName, dc.TableRef, dc.SensitivityLevel, dc.DataType, dc.MaskingStrategy, dc.Status)
	return err
}

// DeleteDC 删除数据分级。
func (s *Store) DeleteDC(ctx context.Context, psID, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM security_data_classification WHERE id=$1 AND project_space_id=$2`, id, psID)
	return err
}

// ---------------- 审计日志 ----------------

// AppendAudit 追加审计记录。
func (s *Store) AppendAudit(ctx context.Context, a *AuditLog) error {
	a.ID = "aud_" + uuid.NewString()[:20]
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO security_audit (id, project_space_id, actor_type, actor_id, action, resource_type, detail, policy_decision)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		a.ID, a.ProjectSpaceID, a.ActorType, a.ActorID, a.Action, a.ResourceType, a.Detail, a.PolicyDecision)
	return err
}

// ListAudit 审计日志列表。
func (s *Store) ListAudit(ctx context.Context, psID string) ([]AuditLog, error) {
	var list []AuditLog
	err := s.db.SelectContext(ctx, &list,
		`SELECT id, project_space_id, actor_type, actor_id, action, resource_type, detail, policy_decision, created_at
		 FROM security_audit WHERE project_space_id=$1 ORDER BY created_at DESC LIMIT 200`, psID)
	return list, err
}

func preview(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

package rule

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Store 规则数据访问。
type Store struct {
	db *sqlx.DB
}

// NewStore 构造 Store。
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

// List 全部规则。
func (s *Store) List(ctx context.Context) ([]Rule, error) {
	var list []Rule
	err := s.db.SelectContext(ctx, &list,
		`SELECT id, name, category, type, condition, condition_field, action, scope, enabled, description, created_at, updated_at
		 FROM rule ORDER BY category, name`)
	return list, err
}

// ListEnabled 生效范围内的启用规则（scope='all' 或 scope=?）。
func (s *Store) ListEnabled(ctx context.Context, scope string) ([]Rule, error) {
	var list []Rule
	err := s.db.SelectContext(ctx, &list,
		`SELECT id, name, category, type, condition, condition_field, action, scope, enabled, description, created_at, updated_at
		 FROM rule WHERE enabled=1 AND (scope='all' OR scope=?)`, scope)
	return list, err
}

// Create 新建规则。
func (s *Store) Create(ctx context.Context, r *Rule) error {
	r.ID = "rule_" + uuid.NewString()[:21]
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO rule (id, name, category, type, condition, condition_field, action, scope, enabled, description)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Name, r.Category, r.Type, r.Condition, r.ConditionField, r.Action, r.Scope, r.Enabled, r.Description)
	return err
}

// Update 更新规则。
func (s *Store) Update(ctx context.Context, r *Rule) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE rule SET name=?, category=?, type=?, condition=?, condition_field=?, action=?, scope=?, enabled=?, description=?, updated_at=CURRENT_TIMESTAMP
		 WHERE id=?`,
		r.Name, r.Category, r.Type, r.Condition, r.ConditionField, r.Action, r.Scope, r.Enabled, r.Description, r.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("规则 %s 不存在", r.ID)
	}
	return nil
}

// SetEnabled 启用/禁用。
func (s *Store) SetEnabled(ctx context.Context, id string, enabled bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE rule SET enabled=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, enabled, id)
	return err
}

// Delete 删除。
func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM rule WHERE id=?`, id)
	return err
}

// SeedDemoRules 若 rule 表为空，播种演示规则（制度/红线 RaC 示例）。
func (s *Store) SeedDemoRules(ctx context.Context) error {
	var n int
	if err := s.db.GetContext(ctx, &n, `SELECT COUNT(*) FROM rule`); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	demos := []Rule{
		{Name: "禁删数据库迁移文件", Category: "coding", Type: "mandatory",
			Condition: "(删除|delete).*(migrate|迁移)|(migrate|迁移).*(删除|delete)", ConditionField: "prompt",
			Action: "block", Scope: "dev", Enabled: true, Description: "禁止删除数据库迁移文件"},
		{Name: "生产操作需审批", Category: "process", Type: "mandatory",
			Condition: "(生产|production|线上|prod).*(部署|删除|变更|deploy)", ConditionField: "prompt",
			Action: "require_approval", Scope: "dev", Enabled: true, Description: "生产环境操作需🚪人工审批"},
		{Name: "产出含密钥需复核", Category: "security", Type: "should",
			Condition: "api[_-]?key|secret|password|token", ConditionField: "output",
			Action: "warn", Scope: "dev", Enabled: true, Description: "AI 产出含疑似密钥需人工复核"},
	}
	for _, r := range demos {
		r.ID = "rule_" + uuid.NewString()[:21]
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO rule (id,name,category,type,condition,condition_field,action,scope,enabled,description)
			 VALUES (?,?,?,?,?,?,?,?,?,?)`,
			r.ID, r.Name, r.Category, r.Type, r.Condition, r.ConditionField, r.Action, r.Scope, r.Enabled, r.Description); err != nil {
			return err
		}
	}
	return nil
}

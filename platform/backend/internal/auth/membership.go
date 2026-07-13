// Package auth 权限管理：RBAC（角色×操作）+ ABAC（项目空间）+ 多租户。
package auth

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Member 成员关系（用户 × 项目空间 × 角色）。
type Member struct {
	UserID         string `json:"user_id" db:"user_id"`
	ProjectSpaceID string `json:"project_space_id" db:"project_space_id"`
	Role           string `json:"role" db:"role"` // business/dev/rule_architect/gatekeeper/admin
}

// Store 成员数据访问。
type Store struct {
	db *sqlx.DB
}

// NewStore 构造 Store。
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

// AddMember 把用户加入项目空间并分配角色。
func (s *Store) AddMember(ctx context.Context, m *Member) error {
	id := "mbr_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO membership (id, project_space_id, user_id, role) VALUES (?, ?, ?, ?)`,
		id, m.ProjectSpaceID, m.UserID, m.Role)
	return err
}

// ListMembers 列出项目空间成员。
func (s *Store) ListMembers(ctx context.Context, projectSpaceID string) ([]Member, error) {
	var list []Member
	err := s.db.SelectContext(ctx, &list,
		`SELECT user_id, project_space_id, role FROM membership WHERE project_space_id = ?`, projectSpaceID)
	return list, err
}

// Roles 查用户在某项目空间下的角色（projectSpaceID 为空则查全部空间）。
func (s *Store) Roles(ctx context.Context, userID, projectSpaceID string) ([]string, error) {
	q := `SELECT role FROM membership WHERE user_id = ?`
	args := []interface{}{userID}
	if projectSpaceID != "" {
		q += ` AND project_space_id = ?`
		args = append(args, projectSpaceID)
	}
	var roles []string
	err := s.db.SelectContext(ctx, &roles, q, args...)
	if err != nil {
		return nil, err
	}
	return roles, nil
}

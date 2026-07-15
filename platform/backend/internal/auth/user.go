package auth

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// User 用户实体（一等公民：用户是有记录、可管理的，不再是裸字符串）。
// M1 仍用 X-User 头模拟登录（后续接 OIDC/SSO），但用户在此有目录记录；
// 用户在某空间的角色由 membership 表表达（用户 × 空间 × 角色）。
type User struct {
	ID        string    `json:"id" db:"id"`
	Name      string    `json:"name" db:"name"`
	Email     string    `json:"email" db:"email"`
	Status    string    `json:"status" db:"status"` // active / disabled
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// CreateUser 新建用户。
func (s *Store) CreateUser(ctx context.Context, u *User) error {
	u.ID = "usr_" + uuid.NewString()[:20]
	if u.Status == "" {
		u.Status = "active"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO "user" (id, name, email, status) VALUES (?, ?, ?, ?)`,
		u.ID, u.Name, u.Email, u.Status)
	return err
}

// ListUsers 用户目录。
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	var list []User
	err := s.db.SelectContext(ctx, &list,
		`SELECT id, name, COALESCE(email,'') AS email, status, created_at FROM "user" ORDER BY created_at`)
	return list, err
}

// GetUser 取单条。
func (s *Store) GetUser(ctx context.Context, id string) (*User, error) {
	var u User
	err := s.db.GetContext(ctx, &u,
		`SELECT id, name, COALESCE(email,'') AS email, status, created_at FROM "user" WHERE id=?`, id)
	return &u, err
}

// GetUserByName 按用户名取（种子/登录查找用）。
func (s *Store) GetUserByName(ctx context.Context, name string) (*User, error) {
	var u User
	err := s.db.GetContext(ctx, &u,
		`SELECT id, name, COALESCE(email,'') AS email, status, created_at FROM "user" WHERE name=?`, name)
	return &u, err
}

// SpacesOf 用户加入的空间+角色（跨空间视图）。
func (s *Store) SpacesOf(ctx context.Context, userID string) ([]Member, error) {
	var list []Member
	err := s.db.SelectContext(ctx, &list,
		`SELECT user_id, project_space_id, role FROM membership WHERE user_id=?`, userID)
	return list, err
}

package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
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

// ErrInvalidCredential 用户名或密码错误。
var ErrInvalidCredential = errors.New("用户名或密码错误")

// SetPasswordByName 设置用户密码（bcrypt 哈希）。
func (s *Store) SetPasswordByName(ctx context.Context, name, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE "user" SET password_hash=? WHERE name=?`, string(hash), name)
	return err
}

// EnsurePassword 若用户无密码则设默认（启动 seed admin 用；已有密码不覆盖，用户不存在跳过）。
func (s *Store) EnsurePassword(ctx context.Context, name, password string) error {
	var hash string
	if err := s.db.GetContext(ctx, &hash, `SELECT COALESCE(password_hash,'') FROM "user" WHERE name=?`, name); err != nil {
		return nil // 用户不存在（no rows）→ skip
	}
	if hash != "" {
		return nil // 已有密码，不覆盖
	}
	return s.SetPasswordByName(ctx, name, password)
}

// Login 校验用户名+密码，签发 token（7 天有效），返回 token + 用户。
func (s *Store) Login(ctx context.Context, name, password string) (string, *User, error) {
	u, err := s.GetUserByName(ctx, name)
	if err != nil || u == nil || u.ID == "" || u.Status != "active" {
		return "", nil, ErrInvalidCredential
	}
	var hash string
	if err := s.db.GetContext(ctx, &hash, `SELECT COALESCE(password_hash,'') FROM "user" WHERE id=?`, u.ID); err != nil || hash == "" {
		return "", nil, ErrInvalidCredential
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return "", nil, ErrInvalidCredential
	}
	token := "tok_" + uuid.NewString()[:24]
	expires := time.Now().Add(7 * 24 * time.Hour)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO auth_session (token, user_id, user_name, expires_at) VALUES (?, ?, ?, ?)`,
		token, u.ID, u.Name, expires); err != nil {
		return "", nil, err
	}
	return token, u, nil
}

// ValidToken 校验 token，返回用户名（CtxUserID 用 name，与 X-User 一致）。
func (s *Store) ValidToken(ctx context.Context, token string) (string, bool) {
	if token == "" {
		return "", false
	}
	var name string
	if err := s.db.GetContext(ctx, &name, `SELECT user_name FROM auth_session WHERE token=? AND expires_at > CURRENT_TIMESTAMP`, token); err != nil || name == "" {
		return "", false
	}
	return name, true
}

// Logout 删除 token（登出）。
func (s *Store) Logout(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM auth_session WHERE token=?`, token)
	return err
}

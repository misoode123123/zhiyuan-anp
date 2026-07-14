package conversation

import (
	"context"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Store 会话/消息数据访问。
type Store struct {
	db *sqlx.DB
}

// NewStore 构造。
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

const convCols = `id, project_space_id, status, title, requirement_id, created_at, updated_at`
const msgCols = `id, conversation_id, role, content, media_kind, created_at`

// CreateConv 新建会话（active）。
func (s *Store) CreateConv(ctx context.Context, c *Conversation) error {
	c.ID = "conv_" + uuid.NewString()[:20]
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO conversation (id, project_space_id, status) VALUES (?, ?, 'active')`, c.ID, c.ProjectSpaceID)
	return err
}

// GetConv 取会话。
func (s *Store) GetConv(ctx context.Context, id string) (*Conversation, error) {
	var c Conversation
	err := s.db.GetContext(ctx, &c, `SELECT `+convCols+` FROM conversation WHERE id=?`, id)
	return &c, err
}

// ListConvByPS 列出项目空间的会话。
func (s *Store) ListConvByPS(ctx context.Context, psID string) ([]Conversation, error) {
	var list []Conversation
	err := s.db.SelectContext(ctx, &list,
		`SELECT `+convCols+` FROM conversation WHERE project_space_id=? ORDER BY created_at DESC`, psID)
	return list, err
}

// SubmitConv 标记已提交：回填 title + requirement_id。
func (s *Store) SubmitConv(ctx context.Context, id, title, reqID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE conversation SET status='submitted', title=?, requirement_id=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		title, reqID, id)
	return err
}

// AddMessage 新增消息。
func (s *Store) AddMessage(ctx context.Context, m *Message) error {
	m.ID = "msg_" + uuid.NewString()[:20]
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO message (id, conversation_id, role, content, media_kind) VALUES (?, ?, ?, ?, ?)`,
		m.ID, m.ConversationID, m.Role, m.Content, m.MediaKind)
	return err
}

// ListMessages 按时间正序列出会话消息。
func (s *Store) ListMessages(ctx context.Context, cid string) ([]Message, error) {
	var list []Message
	err := s.db.SelectContext(ctx, &list,
		`SELECT `+msgCols+` FROM message WHERE conversation_id=? ORDER BY created_at`, cid)
	return list, err
}

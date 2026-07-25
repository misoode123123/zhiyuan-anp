// Package notif 消息通知：审批/编码/发布等事件实时推送。
package notif

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
)

// Notification 通知条目。
type Notification struct {
	ID             int64     `json:"id" db:"id"`
	UserID         *string   `json:"user_id,omitempty" db:"user_id"`
	ProjectSpaceID *string   `json:"project_space_id,omitempty" db:"project_space_id"`
	Type           string    `json:"type" db:"type"`
	Title          string    `json:"title" db:"title"`
	Message        string    `json:"message,omitempty" db:"message"`
	Link           string    `json:"link,omitempty" db:"link"`
	Read           bool      `json:"read" db:"read"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
}

// Store 通知数据访问 + SSE 广播。
type Store struct {
	db        *sqlx.DB
	mu        sync.RWMutex
	listeners map[string][]chan *Notification // user_id → channels
}

// NewStore 构造。
func NewStore(db *sqlx.DB) *Store {
	return &Store{db: db, listeners: map[string][]chan *Notification{}}
}

// Create 写入通知 + 实时广播给该用户的 SSE channel。
func (s *Store) Create(ctx context.Context, n *Notification) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO notification (user_id, project_space_id, type, title, message, link)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		n.UserID, n.ProjectSpaceID, n.Type, n.Title, n.Message, n.Link)
	if err != nil {
		return err
	}
	// 广播
	s.broadcast(n)
	return nil
}

// List 查询用户的通知。
func (s *Store) List(ctx context.Context, userID string, unreadOnly bool, limit int) ([]Notification, error) {
	q := `SELECT id, user_id, project_space_id, type, title, message, link, read, created_at
	      FROM notification WHERE user_id = $1 OR user_id IS NULL`
	args := []interface{}{userID}
	if unreadOnly {
		q += ` AND read = FALSE`
	}
	q += ` ORDER BY created_at DESC`
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	q += fmt.Sprintf(` LIMIT %d`, limit)
	var list []Notification
	err := s.db.SelectContext(ctx, &list, q, args...)
	return list, err
}

// MarkRead 标记已读。
func (s *Store) MarkRead(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE notification SET read = TRUE WHERE id = $1`, id)
	return err
}

// MarkAllRead 标记用户全部已读。
func (s *Store) MarkAllRead(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE notification SET read = TRUE WHERE user_id = $1 AND read = FALSE`, userID)
	return err
}

// UnreadCount 未读数。
func (s *Store) UnreadCount(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.db.GetContext(ctx, &n,
		`SELECT COUNT(*) FROM notification WHERE (user_id = $1 OR user_id IS NULL) AND read = FALSE`, userID)
	return n, err
}

// --- SSE 广播 ---

// Subscribe 订阅用户的通知 channel（SSE 用）。
func (s *Store) Subscribe(userID string) chan *Notification {
	ch := make(chan *Notification, 16)
	s.mu.Lock()
	s.listeners[userID] = append(s.listeners[userID], ch)
	s.mu.Unlock()
	return ch
}

// Unsubscribe 取消订阅。
func (s *Store) Unsubscribe(userID string, ch chan *Notification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	chs := s.listeners[userID]
	for i, c := range chs {
		if c == ch {
			s.listeners[userID] = append(chs[:i], chs[i+1:]...)
			close(ch)
			break
		}
	}
}

// broadcast 广播通知给该用户的所有 channel。
func (s *Store) broadcast(n *Notification) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// 定向用户
	if n.UserID != nil {
		for _, ch := range s.listeners[*n.UserID] {
			select {
			case ch <- n:
			default: // channel 满了跳过（不阻塞）
			}
		}
	}
	// 广播通知（user_id IS NULL）→ 所有人
	if n.UserID == nil {
		for _, chs := range s.listeners {
			for _, ch := range chs {
				select {
				case ch <- n:
				default:
				}
			}
		}
	}
}

// MarshalJSON for SSE（data: JSON\n\n）
func (n *Notification) SSEData() string {
	buf, _ := json.Marshal(n)
	return fmt.Sprintf("data: %s\n\n", string(buf))
}

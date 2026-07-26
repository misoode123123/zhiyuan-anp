package codews

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// SessionRecord 一次开发者×应用×工具的编码工作台会话（落库供绩效/互动统计）。
type SessionRecord struct {
	ID             string     `json:"id" db:"id"`
	ProjectSpaceID string     `json:"project_space_id" db:"project_space_id"`
	AppID          string     `json:"app_id" db:"app_id"`
	UserID         string     `json:"user_id,omitempty" db:"user_id"`
	Tool           string     `json:"tool" db:"tool"`
	RepoDir        string     `json:"repo_dir" db:"repo_dir"`
	Port           int        `json:"port" db:"port"`
	SessionID      string     `json:"session_id,omitempty" db:"session_id"` // 工具原生会话 id（opencode 有；claude/codex 按 repo_dir 解析）
	StartedAt      time.Time  `json:"started_at" db:"started_at"`
	EndedAt        *time.Time `json:"ended_at,omitempty" db:"ended_at"`
	PromptCount    int        `json:"prompt_count" db:"prompt_count"`
	MessageCount   int        `json:"message_count" db:"message_count"`
}

// SessionCounts 会话结束时回填的交互计数（由 TranscriptReader 统计）。
type SessionCounts struct{ PromptCount, MessageCount int }

// SessionStore codews 会话持久化（落库供绩效/互动统计；nil=纯内存兼容，测试/未启用）。
type SessionStore interface {
	StartSession(ctx context.Context, s *SessionRecord) error                  // Ensure 启动后调
	FinishSession(ctx context.Context, id string, counts SessionCounts) error  // 进程退出时调
}

type pgSessionStore struct{ db *sqlx.DB }

// NewPGSessionStore PG 实现。
func NewPGSessionStore(db *sqlx.DB) SessionStore { return &pgSessionStore{db: db} }

func nullableStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func (p *pgSessionStore) StartSession(ctx context.Context, s *SessionRecord) error {
	s.ID = "cws_" + uuid.NewString()[:20]
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO codews_session (id, project_space_id, app_id, user_id, tool, repo_dir, port, session_id, started_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,CURRENT_TIMESTAMP)`,
		s.ID, s.ProjectSpaceID, s.AppID, nullableStr(s.UserID), s.Tool, s.RepoDir, s.Port, nullableStr(s.SessionID))
	return err
}

func (p *pgSessionStore) FinishSession(ctx context.Context, id string, c SessionCounts) error {
	_, err := p.db.ExecContext(ctx,
		`UPDATE codews_session SET ended_at=CURRENT_TIMESTAMP, prompt_count=$2, message_count=$3 WHERE id=$1`,
		id, c.PromptCount, c.MessageCount)
	return err
}

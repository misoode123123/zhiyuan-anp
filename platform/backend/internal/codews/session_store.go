package codews

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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
	SessionID      string     `json:"session_id,omitempty" db:"session_id"`         // 工具原生会话 id（opencode 有；claude/codex 按 repo_dir 解析）
	RequirementID  string     `json:"requirement_id,omitempty" db:"requirement_id"` // 绑定的需求（工作直播按此查；空=application 页老入口）
	StartedAt      time.Time  `json:"started_at" db:"started_at"`
	EndedAt        *time.Time `json:"ended_at,omitempty" db:"ended_at"`
	PromptCount    int        `json:"prompt_count" db:"prompt_count"`
	MessageCount   int        `json:"message_count" db:"message_count"`
}

// SessionCounts 会话结束时回填的交互计数（由 TranscriptReader 统计）。
type SessionCounts struct{ PromptCount, MessageCount int }

// SessionStore codews 会话持久化（落库供绩效/互动统计；nil=纯内存兼容，测试/未启用）。
type SessionStore interface {
	StartSession(ctx context.Context, s *SessionRecord) error                                      // Ensure 启动后调
	FinishSession(ctx context.Context, id string, counts SessionCounts) error                      // 进程退出时调
	LastSession(ctx context.Context, projectSpaceID, appID, userID string) (*SessionRecord, error) // 最近一行；无历史返 (nil,nil)
	// SaveMessages 幂等落库某会话的对话消息（后台快照 ticker 调）：全量 upsert，
	// (session_id,seq) 冲突跳过；content 超 100KB 截断。sessionID=codews_session.id。
	SaveMessages(ctx context.Context, sessionID string, msgs []TranscriptMsg) error
	// Messages 按 seq 升序读某会话全部消息（绩效历史对话；进程死后亦可读）。
	Messages(ctx context.Context, sessionID string) ([]TranscriptMsg, error)
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
		`INSERT INTO codews_session (id, project_space_id, app_id, user_id, tool, repo_dir, port, session_id, requirement_id, started_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,CURRENT_TIMESTAMP)`,
		s.ID, s.ProjectSpaceID, s.AppID, nullableStr(s.UserID), s.Tool, s.RepoDir, s.Port, nullableStr(s.SessionID), nullableStr(s.RequirementID))
	return err
}

func (p *pgSessionStore) FinishSession(ctx context.Context, id string, c SessionCounts) error {
	_, err := p.db.ExecContext(ctx,
		`UPDATE codews_session SET ended_at=CURRENT_TIMESTAMP, prompt_count=$2, message_count=$3 WHERE id=$1`,
		id, c.PromptCount, c.MessageCount)
	return err
}

// LastSession 返回某开发者×应用最近一次落库的编码会话（started_at 倒序首条）。
// 用于后端重启后(old==nil)判定上次会话绑的需求：若与本次不同则强制新建，
// 避免 ensureSession 按 workDir 把上个需求的会话捞回（跨需求串台 + token 浪费）。
// 无历史行 → (nil, nil)（非错误）。userID 经 nullableStr：与 StartSession 写入口径一致。
// user_id/session_id/requirement_id 列可空（见迁移 000018/000020），COALESCE 兜空串
// 以匹配 SessionRecord 的 string 字段（否则 NULL→string 扫描报错）。
func (p *pgSessionStore) LastSession(ctx context.Context, projectSpaceID, appID, userID string) (*SessionRecord, error) {
	var r SessionRecord
	err := p.db.GetContext(ctx, &r,
		`SELECT id, project_space_id, app_id, COALESCE(user_id,'') AS user_id, tool, repo_dir, port,
		        COALESCE(session_id,'') AS session_id, COALESCE(requirement_id,'') AS requirement_id,
		        started_at, ended_at, prompt_count, message_count
		 FROM codews_session
		 WHERE project_space_id=$1 AND app_id=$2 AND user_id=$3
		 ORDER BY started_at DESC LIMIT 1`,
		projectSpaceID, appID, nullableStr(userID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// maxMessageContent 单条消息内容截断阈值（100KB），防超大代码块撑爆库。
const maxMessageContent = 100 * 1024

// SaveMessages 全量幂等 upsert 某会话对话消息（后台快照 ticker 调）。
// seq=msgs 下标（LiveTranscript 返回顺序即消息时序，追加式稳定）；(session_id,seq) 唯一，
// ON CONFLICT DO NOTHING 使重复快照只追加新增消息。content 超 maxMessageContent 截断。
func (p *pgSessionStore) SaveMessages(ctx context.Context, sessionID string, msgs []TranscriptMsg) error {
	if len(msgs) == 0 {
		return nil
	}
	var (
		sb   strings.Builder
		args []interface{}
	)
	sb.WriteString("INSERT INTO codews_message (id, session_id, role, content, seq) VALUES ")
	for i, mm := range msgs {
		content := mm.Content
		if len(content) > maxMessageContent {
			content = content[:maxMessageContent] + "\n…(已截断)"
		}
		if i > 0 {
			sb.WriteString(",")
		}
		base := i * 5
		fmt.Fprintf(&sb, "($%d,$%d,$%d,$%d,$%d)", base+1, base+2, base+3, base+4, base+5)
		args = append(args, "cwm_"+uuid.NewString()[:20], sessionID, mm.Role, content, i)
	}
	sb.WriteString(" ON CONFLICT (session_id, seq) DO NOTHING")
	_, err := p.db.ExecContext(ctx, sb.String(), args...)
	return err
}

// Messages 按 seq 升序读某会话全部消息（绩效历史对话下钻）。
func (p *pgSessionStore) Messages(ctx context.Context, sessionID string) ([]TranscriptMsg, error) {
	var rows []struct {
		Role    string `db:"role"`
		Content string `db:"content"`
	}
	if err := p.db.SelectContext(ctx, &rows,
		`SELECT role, content FROM codews_message WHERE session_id=$1 ORDER BY seq`, sessionID); err != nil {
		return nil, err
	}
	out := make([]TranscriptMsg, len(rows))
	for i, r := range rows {
		out[i] = TranscriptMsg{Role: r.Role, Content: r.Content}
	}
	return out, nil
}

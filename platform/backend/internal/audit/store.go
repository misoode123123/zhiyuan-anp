// Package audit 操作审计：记录关键操作（谁何时对什么做了什么）。
// actor 支持 user/agent/system，为"智能体自动运维"铺路——智能体执行操作时同样被审计。
package audit

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/jmoiron/sqlx"
)

// LogEntry 操作审计条目。
type LogEntry struct {
	ID             int64     `json:"id" db:"id"`
	Timestamp      time.Time `json:"timestamp" db:"timestamp"`
	ActorType      string    `json:"actor_type" db:"actor_type"` // user / agent / system
	ActorID        string    `json:"actor_id" db:"actor_id"`     // 用户 id / agent id / "system"
	Action         string    `json:"action" db:"action"`         // app.deploy / change.approve / ...
	ResourceType   string    `json:"resource_type,omitempty" db:"resource_type"`
	ResourceID     string    `json:"resource_id,omitempty" db:"resource_id"`
	ProjectSpaceID string    `json:"project_space_id,omitempty" db:"project_space_id"`
	TraceID        string    `json:"trace_id,omitempty" db:"trace_id"`
	Detail         *string   `json:"detail,omitempty" db:"detail"` // JSONB
	Status         string    `json:"status" db:"status"`           // success / failed
	Error          string    `json:"error,omitempty" db:"error"`
}

// Store 操作审计数据访问。
type Store struct {
	db *sqlx.DB
}

// NewStore 构造。
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

// Create 写一条审计。
func (s *Store) Create(ctx context.Context, e *LogEntry) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO operation_log (actor_type, actor_id, action, resource_type, resource_id, project_space_id, trace_id, detail, status, error)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		e.ActorType, e.ActorID, e.Action, e.ResourceType, e.ResourceID, e.ProjectSpaceID, e.TraceID, e.Detail, e.Status, e.Error)
	return err
}

// CreateDetail 带 detail map 的便捷写（自动 JSON 编码）。
func (s *Store) CreateDetail(ctx context.Context, actorType, actorID, action, resourceType, resourceID, psID, traceID, status, errMsg string, detail map[string]interface{}) error {
	e := &LogEntry{
		ActorType:      actorType,
		ActorID:        actorID,
		Action:         action,
		ResourceType:   resourceType,
		ResourceID:     resourceID,
		ProjectSpaceID: psID,
		TraceID:        traceID,
		Status:         status,
		Error:          errMsg,
	}
	if len(detail) > 0 {
		buf, _ := json.Marshal(detail)
		d := string(buf)
		e.Detail = &d
	}
	return s.Create(ctx, e)
}

// Query 查审计（按 actor/action/resource 筛选 + 分页）。
func (s *Store) Query(ctx context.Context, actorType, actorID, action, resourceType, resourceID string, limit, offset int) ([]LogEntry, error) {
	q := `SELECT id, timestamp, actor_type, actor_id, action, resource_type, resource_id, project_space_id, trace_id, detail, status, error
	      FROM operation_log WHERE 1=1`
	args := []interface{}{}
	i := 1
	add := func(col, val string) {
		if val != "" {
			q += " AND " + col + "=$" + strconv.Itoa(i)
			args = append(args, val)
			i++
		}
	}
	add("actor_type", actorType)
	add("actor_id", actorID)
	add("action", action)
	add("resource_type", resourceType)
	add("resource_id", resourceID)
	q += " ORDER BY timestamp DESC"
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q += " LIMIT $" + strconv.Itoa(i)
	args = append(args, limit)
	i++
	if offset > 0 {
		q += " OFFSET $" + strconv.Itoa(i)
		args = append(args, offset)
	}
	var list []LogEntry
	err := s.db.SelectContext(ctx, &list, q, args...)
	return list, err
}

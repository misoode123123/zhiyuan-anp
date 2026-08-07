// Package logsvc 统一日志服务：跨层（前端/后端/Python）日志统一入库 + 查询。
package logsvc

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/jmoiron/sqlx"
)

// LogEntry 日志条目。
type LogEntry struct {
	ID             int64      `json:"id" db:"id"`
	Timestamp      time.Time  `json:"timestamp" db:"timestamp"`
	Level          string     `json:"level" db:"level"`
	Source         string     `json:"source" db:"source"`
	Module         string     `json:"module,omitempty" db:"module"`
	TraceID        string     `json:"trace_id,omitempty" db:"trace_id"`
	UserID         string     `json:"user_id,omitempty" db:"user_id"`
	ProjectSpaceID string     `json:"project_space_id,omitempty" db:"project_space_id"`
	Message        string     `json:"message" db:"message"`
	StackTrace     string     `json:"stack_trace,omitempty" db:"stack_trace"`
	Context        *string    `json:"context,omitempty" db:"context"`
	Resolved       bool       `json:"resolved" db:"resolved"`
	ResolvedBy     *string    `json:"resolved_by,omitempty" db:"resolved_by"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty" db:"resolved_at"`
}

// Store 日志数据访问。
type Store struct {
	db *sqlx.DB
}

// NewStore 构造。
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

// Create 写入一条日志。
func (s *Store) Create(ctx context.Context, e *LogEntry) error {
	var ctxJSON *string
	if e.Context != nil {
		ctxJSON = e.Context
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO platform_log (level, source, module, trace_id, user_id, project_space_id, message, stack_trace, context)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		e.Level, e.Source, e.Module, e.TraceID, e.UserID, e.ProjectSpaceID,
		e.Message, e.StackTrace, ctxJSON)
	return err
}

// CreateFromJSON 从前端/Python 回传的 JSON 快速写日志。
func (s *Store) CreateFromJSON(ctx context.Context, level, source, msg string, fields map[string]interface{}) error {
	e := &LogEntry{Level: level, Source: source, Message: msg}
	if v, ok := fields["module"].(string); ok {
		e.Module = v
	}
	if v, ok := fields["trace_id"].(string); ok {
		e.TraceID = v
	}
	if v, ok := fields["user_id"].(string); ok {
		e.UserID = v
	}
	if v, ok := fields["project_space_id"].(string); ok {
		e.ProjectSpaceID = v
	}
	if v, ok := fields["stack"].(string); ok {
		e.StackTrace = v
	}
	// 剩余字段存 context
	ctxFiltered := map[string]interface{}{}
	for k, v := range fields {
		switch k {
		case "module", "trace_id", "user_id", "project_space_id", "stack":
		default:
			ctxFiltered[k] = v
		}
	}
	if len(ctxFiltered) > 0 {
		buf, _ := json.Marshal(ctxFiltered)
		s := string(buf)
		e.Context = &s
	}
	return s.Create(ctx, e)
}

// QueryFilter 日志查询过滤（M5 增强：trace_id 精确、q message 关键词、时间窗）。
type QueryFilter struct {
	Level, Source, Module string
	TraceID, Q            string // Q: message ILIKE 关键词
	Since, Until          string // 时间范围（ISO8601 字符串）
	Limit, Offset         int
}

// Query 按 QueryFilter 查日志（M5）。
func (s *Store) Query(ctx context.Context, f QueryFilter) ([]LogEntry, error) {
	q := `SELECT id, timestamp, level, source, module, trace_id, user_id, project_space_id, message, stack_trace, context, resolved, resolved_by, resolved_at
	      FROM platform_log WHERE 1=1`
	args := []interface{}{}
	i := 1
	add := func(col, val, op string) {
		if val != "" {
			q += " AND " + col + " " + op + " $" + itoa(i)
			args = append(args, val)
			i++
		}
	}
	add("level", f.Level, "=")
	add("source", f.Source, "=")
	add("module", f.Module, "=")
	add("trace_id", f.TraceID, "=")
	if f.Q != "" {
		q += " AND message ILIKE $" + itoa(i)
		args = append(args, "%"+f.Q+"%")
		i++
	}
	add("timestamp", f.Since, ">=")
	add("timestamp", f.Until, "<=")
	q += " ORDER BY timestamp DESC"
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	q += " LIMIT $" + itoa(i)
	args = append(args, f.Limit)
	i++
	if f.Offset > 0 {
		q += " OFFSET $" + itoa(i)
		args = append(args, f.Offset)
	}
	var list []LogEntry
	err := s.db.SelectContext(ctx, &list, q, args...)
	return list, err
}

// Stats 统计（总 ERROR 数 / 各来源占比 / 今日）。
func (s *Store) Stats(ctx context.Context) (map[string]interface{}, error) {
	var totalLogs int
	_ = s.db.GetContext(ctx, &totalLogs, `SELECT COUNT(*) FROM platform_log`)

	var unresolvedErrors int
	_ = s.db.GetContext(ctx, &unresolvedErrors,
		`SELECT COUNT(*) FROM platform_log WHERE resolved=FALSE AND level IN ('ERROR','FATAL')`)

	var todayErrors int
	_ = s.db.GetContext(ctx, &todayErrors,
		`SELECT COUNT(*) FROM platform_log WHERE level IN ('ERROR','FATAL') AND timestamp >= CURRENT_DATE`)

	type srcCount struct {
		Source string `db:"source"`
		N      int    `db:"n"`
	}
	var bySource []srcCount
	_ = s.db.SelectContext(ctx, &bySource,
		`SELECT source, COUNT(*) as n FROM platform_log WHERE level IN ('ERROR','FATAL') GROUP BY source`)

	sourceMap := map[string]int{}
	for _, sc := range bySource {
		sourceMap[sc.Source] = sc.N
	}

	return map[string]interface{}{
		"total_logs":        totalLogs,
		"unresolved_errors": unresolvedErrors,
		"today_errors":      todayErrors,
		"by_source":         sourceMap,
	}, nil
}

// Resolve 标记已处理。
func (s *Store) Resolve(ctx context.Context, id int64, resolvedBy string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE platform_log SET resolved=TRUE, resolved_by=$1, resolved_at=NOW() WHERE id=$2`,
		resolvedBy, id)
	return err
}

// Trend 查近 N 天每天的 level 计数（趋势图用）。
func (s *Store) Trend(ctx context.Context, days int) ([]TrendPoint, error) {
	if days <= 0 {
		days = 7
	}
	var list []TrendPoint
	err := s.db.SelectContext(ctx, &list,
		`SELECT
		   d::date AS date,
		   COALESCE(SUM(CASE WHEN level IN ('ERROR','FATAL') THEN 1 ELSE 0 END), 0) AS errors,
		   COALESCE(SUM(CASE WHEN level = 'WARN' THEN 1 ELSE 0 END), 0) AS warns
		 FROM generate_series(CURRENT_DATE - ($1::int - 1), CURRENT_DATE, '1 day') AS d
		 LEFT JOIN platform_log ON platform_log.timestamp::date = d::date
		 GROUP BY d::date ORDER BY d::date`, days)
	return list, err
}

// TrendPoint 趋势数据点。
type TrendPoint struct {
	Date   string `json:"date" db:"date"`
	Errors int    `json:"errors" db:"errors"`
	Warns  int    `json:"warns" db:"warns"`
}

// SourceBreakdown 按来源+级别统计（饼图用）。
func (s *Store) SourceBreakdown(ctx context.Context) ([]SourceCount, error) {
	var list []SourceCount
	err := s.db.SelectContext(ctx, &list,
		`SELECT source, level, COUNT(*) as count
		 FROM platform_log
		 WHERE timestamp >= NOW() - INTERVAL '7 days'
		 GROUP BY source, level ORDER BY count DESC`)
	return list, err
}

// SourceCount 来源统计项。
type SourceCount struct {
	Source string `json:"source" db:"source"`
	Level  string `json:"level" db:"level"`
	Count  int    `json:"count" db:"count"`
}

// itoa 被 Query 用于动态拼 $N 占位符（参数数 ≤ 9，够用）。
func itoa(i int) string {
	return strconv.Itoa(i)
}

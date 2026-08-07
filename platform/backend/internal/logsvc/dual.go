// Package logsvc 统一日志 — P1: Go 后端 ERROR 双写（zap + DB）。
package logsvc

import (
	"context"

	"go.uber.org/zap"
)

// DualLogger 双写日志器：zap（本地文件/stdout）+ platform_log DB（查询）。
// ERROR/FATAL 入库（查得到）；INFO/WARN 只 zap（省 DB）。
type DualLogger struct {
	Zap   *zap.Logger
	Store *Store
}

// NewDualLogger 构造。
func NewDualLogger(zapLogger *zap.Logger, store *Store) *DualLogger {
	return &DualLogger{Zap: zapLogger, Store: store}
}

// LogEntryInput 日志入参（各模块调用）。
type LogEntryInput struct {
	Level   string // ERROR / WARN / INFO
	Module  string // requirement / dev / compute / ...
	TraceID string
	UserID  string
	Msg     string
	Fields  map[string]interface{}
	Stack   string
}

// Log 统一日志入口：zap 记全量，DB 只记 ERROR/FATAL。
func (dl *DualLogger) Log(in LogEntryInput) {
	// zap（全量）
	fields := []zap.Field{
		zap.String("module", in.Module),
		zap.String("source", "backend"),
	}
	if in.TraceID != "" {
		fields = append(fields, zap.String("trace_id", in.TraceID))
	}
	for k, v := range in.Fields {
		fields = append(fields, zap.Any(k, v))
	}
	switch in.Level {
	case "ERROR":
		dl.Zap.Error(in.Msg, fields...)
	case "WARN":
		dl.Zap.Warn(in.Msg, fields...)
	case "INFO":
		dl.Zap.Info(in.Msg, fields...)
	default:
		dl.Zap.Info(in.Msg, fields...)
	}

	// DB 记 WARN 及以上（查询用）；INFO/DEBUG 只 zap，省 DB
	if in.Level != "WARN" && in.Level != "ERROR" && in.Level != "FATAL" {
		return
	}
	if dl.Store == nil {
		return
	}
	ctx := context.Background()
	_ = dl.Store.CreateFromJSON(ctx, in.Level, "backend", in.Msg, mergeFields(in))
}

func mergeFields(in LogEntryInput) map[string]interface{} {
	out := map[string]interface{}{
		"module":   in.Module,
		"trace_id": in.TraceID,
		"user_id":  in.UserID,
	}
	if in.Stack != "" {
		out["stack"] = in.Stack
	}
	for k, v := range in.Fields {
		out[k] = v
	}
	return out
}

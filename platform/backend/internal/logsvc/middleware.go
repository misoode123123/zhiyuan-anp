package logsvc

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

const CtxDualLogger = "dual_logger"

// DualLoggerMiddleware 注入 DualLogger 到 context + 捕获 5xx 自动入库。
func DualLoggerMiddleware(dl *DualLogger) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(CtxDualLogger, dl)
		start := time.Now()
		c.Next()

		// 5xx → ERROR；4xx → WARN（业务失败也可查可统计）
		status := c.Writer.Status()
		switch {
		case status >= 500:
			dl.Log(LogEntryInput{
				Level:   "ERROR",
				Module:  "http",
				TraceID: c.GetString("trace_id"),
				UserID:  c.GetString("user_id"),
				Msg:     c.Request.Method + " " + c.Request.URL.Path + " → " + strconv.Itoa(status),
				Fields: map[string]interface{}{
					"method":     c.Request.Method,
					"path":       c.Request.URL.Path,
					"status":     status,
					"latency_ms": time.Since(start).Milliseconds(),
				},
			})
		case status >= 400:
			dl.Log(LogEntryInput{
				Level:   "WARN",
				Module:  "http",
				TraceID: c.GetString("trace_id"),
				UserID:  c.GetString("user_id"),
				Msg:     c.Request.Method + " " + c.Request.URL.Path + " → " + strconv.Itoa(status),
				Fields: map[string]interface{}{
					"method":     c.Request.Method,
					"path":       c.Request.URL.Path,
					"status":     status,
					"latency_ms": time.Since(start).Milliseconds(),
				},
			})
		}
	}
}

// FromContext 从 gin.Context 取 DualLogger（各 handler 可用）。
func FromContext(c *gin.Context) *DualLogger {
	if dl, ok := c.Get(CtxDualLogger); ok {
		return dl.(*DualLogger)
	}
	return nil
}

// LogFromCtx 快捷方法：从 context 取 logger 并记日志。
func LogFromCtx(c *gin.Context, level, module, msg string, fields map[string]interface{}) {
	dl := FromContext(c)
	if dl == nil {
		return
	}
	dl.Log(LogEntryInput{
		Level:   level,
		Module:  module,
		TraceID: c.GetString("trace_id"),
		UserID:  c.GetString("user_id"),
		Msg:     msg,
		Fields:  fields,
	})
}

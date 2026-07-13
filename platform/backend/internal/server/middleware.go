package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	// 请求头与 context key
	HeaderTraceID        = "X-Trace-Id"
	CtxTraceID           = "trace_id"
	HeaderProjectSpaceID = "X-Project-Space-Id"
	CtxProjectSpaceID    = "project_space_id"
)

// Recovery 捕获 panic，避免进程崩溃。
func Recovery(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("panic recovered",
					zap.Any("panic", rec),
					zap.String("path", c.Request.URL.Path),
				)
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"code":     500,
					"message":  "internal error",
					"trace_id": c.GetString(CtxTraceID),
				})
			}
		}()
		c.Next()
	}
}

// Trace 为每个请求生成/透传 traceId（跨 Go/Python 链路追踪的基础）。
func Trace() gin.HandlerFunc {
	return func(c *gin.Context) {
		tid := c.GetHeader(HeaderTraceID)
		if tid == "" {
			tid = strconv.FormatInt(time.Now().UnixNano(), 36)
		}
		c.Set(CtxTraceID, tid)
		c.Header(HeaderTraceID, tid)
		c.Next()
	}
}

// RequestLogger 记录每个请求的方法/路径/状态/耗时。
func RequestLogger(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.Info("http",
			zap.String("trace_id", c.GetString(CtxTraceID)),
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
		)
	}
}

// CORS 放行前端来源（M0 简版；生产应由 API 网关统一）。
func CORS(origins []string) gin.HandlerFunc {
	allow := strings.Join(origins, ",")
	return func(c *gin.Context) {
		h := c.Writer.Header()
		if allow != "" {
			h.Set("Access-Control-Allow-Origin", allow)
		}
		h.Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,PATCH,OPTIONS")
		h.Set("Access-Control-Allow-Headers",
			"Content-Type,Authorization,"+HeaderProjectSpaceID+","+HeaderTraceID)
		h.Set("Access-Control-Allow-Credentials", "true")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// ProjectSpaceInjector 从请求头读取项目空间 ID 注入 context。
// project_space_id 是贯穿所有域的多租户路由键（详见架构设计）。
func ProjectSpaceInjector() gin.HandlerFunc {
	return func(c *gin.Context) {
		if ps := c.GetHeader(HeaderProjectSpaceID); ps != "" {
			c.Set(CtxProjectSpaceID, ps)
		}
		c.Next()
	}
}

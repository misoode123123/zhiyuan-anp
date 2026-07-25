// Package httpx 提供统一的 HTTP 响应封装与错误码约定。
package httpx

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Response 是统一响应体。
type Response struct {
	Code    int         `json:"code"` // 0 表示成功，其余为业务错误码
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
	TraceID string      `json:"trace_id,omitempty"`
}

// OK 返回 200 + 成功数据。
func OK(c *gin.Context, data interface{}) {
	c.JSON(200, Response{Code: 0, Message: "ok", Data: data, TraceID: c.GetString("trace_id")})
}

// Created 返回 201 + 创建结果。
func Created(c *gin.Context, data interface{}) {
	c.JSON(201, Response{Code: 0, Message: "created", Data: data, TraceID: c.GetString("trace_id")})
}

// Err 返回错误状态码 + 业务错误信息，并自动打一条结构化日志（4xx→WARN，5xx→ERROR）。
// 一处改造，所有 handler 的 httpx.Err(...) 调用自动有日志，带 trace_id/user_id/path。
func Err(c *gin.Context, status int, code int, msg string) {
	logErr(c, status, code, msg)
	c.JSON(status, Response{Code: code, Message: msg, TraceID: c.GetString("trace_id")})
}

// logErr 按 http 状态码分级打日志。4xx 业务失败→WARN，5xx→ERROR。
func logErr(c *gin.Context, status int, code int, msg string) {
	fields := []zap.Field{
		zap.String("trace_id", c.GetString("trace_id")),
		zap.String("user_id", c.GetString("user_id")),
		zap.String("method", c.Request.Method),
		zap.String("path", c.Request.URL.Path),
		zap.Int("biz_code", code),
		zap.Int("http_status", status),
	}
	if status >= 500 {
		zap.L().Error(msg, fields...)
		return
	}
	zap.L().Warn(msg, fields...)
}

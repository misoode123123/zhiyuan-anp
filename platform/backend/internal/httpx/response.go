// Package httpx 提供统一的 HTTP 响应封装与错误码约定。
package httpx

import "github.com/gin-gonic/gin"

// Response 是统一响应体。
type Response struct {
	Code    int         `json:"code"`              // 0 表示成功，其余为业务错误码
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

// Err 返回错误状态码 + 业务错误信息。
func Err(c *gin.Context, status int, code int, msg string) {
	c.JSON(status, Response{Code: code, Message: msg, TraceID: c.GetString("trace_id")})
}

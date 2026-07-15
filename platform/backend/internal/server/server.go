// Package server 装配 HTTP 路由与中间件。
package server

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"zhiyuan-anp/platform/backend/internal/config"
)

// New 构造 Gin 引擎，挂载全局中间件与基础路由。
// 认证中间件（auth.AuthUser）在 main 的 /api/v1 组挂载（需要 authStore）。
func New(cfg *config.Config, logger *zap.Logger) *gin.Engine {
	if cfg.Env == "prod" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(
		Recovery(logger),
		Trace(),
		RequestLogger(logger),
		CORS(cfg.CORSOrigins),
		ProjectSpaceInjector(),
	)

	// 健康检查 & 元信息
	r.GET("/healthz", healthz)
	r.GET("/version", version)

	// API v1 —— 各业务模块路由在后续任务接入
	_ = r.Group("/api/v1")

	return r
}

func healthz(c *gin.Context) {
	c.JSON(200, gin.H{"status": "ok"})
}

func version(c *gin.Context) {
	c.JSON(200, gin.H{
		"name":    "zhiyuan-anp-backend",
		"version": "0.1.0",
		"trace_id": c.GetString(CtxTraceID),
	})
}

// 查询 handler：数据库管理只读 API（PG 实例/应用库列表/应用库详情含 DATABASE_URL mask）。
// 写入（供给/清理）仍走 appdeploy.Create/Delete 触发 Provisioner；本 handler 只读。
package pgsupply

import (
	"context"
	"net/url"

	"github.com/gin-gonic/gin"
	"zhiyuan-anp/platform/backend/internal/httpx"
)

// EnvValueReader 读应用 env（appdeploy.Store 实现，避免 pgsupply→appdeploy 循环）。
type EnvValueReader interface {
	GetEnvValue(ctx context.Context, appID, key string) (string, error)
}

// Handler 数据库管理只读查询。
type Handler struct {
	store *Store
	env   EnvValueReader
}

// NewHandler 构造。env 传 appdeploy.Store（满足 EnvValueReader）。
func NewHandler(store *Store, env EnvValueReader) *Handler {
	return &Handler{store: store, env: env}
}

// Register 注册只读查询路由，返回 Handler。
func Register(r gin.IRouter, store *Store, env EnvValueReader) *Handler {
	h := NewHandler(store, env)
	r.GET("/pgsupply/instances", h.ListInstances)
	r.GET("/pgsupply/databases", h.ListDatabases)
	r.GET("/project-spaces/:id/apps/:aid/database", h.GetDatabase)
	return h
}

// ListInstances PG 实例列表（平台级，按创建倒序）。
func (h *Handler) ListInstances(c *gin.Context) {
	list, err := h.store.ListInstances(c.Request.Context())
	if err != nil {
		httpx.Err(c, 500, 50030, err.Error())
		return
	}
	httpx.OK(c, list)
}

// ListDatabases 所有应用库列表（排除已删除，按创建倒序）。
func (h *Handler) ListDatabases(c *gin.Context) {
	list, err := h.store.ListAppDBs(c.Request.Context(), "")
	if err != nil {
		httpx.Err(c, 500, 50030, err.Error())
		return
	}
	httpx.OK(c, list)
}

// GetDatabase 应用库详情（含 DATABASE_URL mask 隐藏密码）。
func (h *Handler) GetDatabase(c *gin.Context) {
	aid := c.Param("aid")
	ad, err := h.store.GetAppDBByApp(c.Request.Context(), aid)
	if err != nil || ad == nil {
		httpx.Err(c, 404, 40430, "应用库不存在")
		return
	}
	dsn, _ := h.env.GetEnvValue(c.Request.Context(), aid, "DATABASE_URL")
	httpx.OK(c, gin.H{
		"database":     ad,
		"database_url": maskDSN(dsn),
	})
}

// maskDSN 隐藏 DSN 密码：postgres://user:pwd@host:port/db → postgres://user:****@host:port/db
// 用 net/url 解析（比正则准确，避免误伤 scheme/user）。
func maskDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	u, err := url.Parse(dsn)
	if err != nil || u.User == nil {
		return dsn
	}
	if _, hasPwd := u.User.Password(); !hasPwd {
		return dsn
	}
	u.User = url.UserPassword(u.User.Username(), "****")
	return u.String()
}

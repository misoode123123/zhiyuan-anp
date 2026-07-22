// 查询 handler：数据库管理 API（PG 实例/应用库列表/应用库详情 + 表/列/SQL 执行 + 操作日志）。
// 写入（供给/清理）仍走 appdeploy.Create/Delete 触发 Provisioner；本 handler 只读 + SQL 执行工具。
package pgsupply

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"zhiyuan-anp/platform/backend/internal/auth"
	"zhiyuan-anp/platform/backend/internal/httpx"
)

// EnvValueReader 读应用 env（appdeploy.Store 实现，避免 pgsupply→appdeploy 循环）。
type EnvValueReader interface {
	GetEnvValue(ctx context.Context, appID, key string) (string, error)
}

// Handler 数据库管理只读查询 + SQL 执行。
type Handler struct {
	store *Store
	env   EnvValueReader
}

// NewHandler 构造。env 传 appdeploy.Store（满足 EnvValueReader）。
func NewHandler(store *Store, env EnvValueReader) *Handler {
	return &Handler{store: store, env: env}
}

// Register 注册数据库管理路由，返回 Handler。
func Register(r gin.IRouter, store *Store, env EnvValueReader) *Handler {
	h := NewHandler(store, env)
	r.GET("/pgsupply/instances", h.ListInstances)
	r.GET("/pgsupply/databases", h.ListDatabases)
	r.GET("/project-spaces/:id/apps/:aid/database", h.GetDatabase)
	// 数据库工具（类 DBeaver）：表/列/SQL 执行/操作日志
	r.GET("/project-spaces/:id/apps/:aid/database/tables", h.ListTables)
	r.GET("/project-spaces/:id/apps/:aid/database/tables/:table/columns", h.ListColumns)
	r.POST("/project-spaces/:id/apps/:aid/database/query", h.ExecSQL)
	r.GET("/project-spaces/:id/apps/:aid/database/actions", h.ListActions)
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

// ListTables 应用库表列表（public schema）。
func (h *Handler) ListTables(c *gin.Context) {
	aid := c.Param("aid")
	dsn, err := h.appDSN(c, aid)
	if err != nil {
		httpx.Err(c, 404, 40431, err.Error())
		return
	}
	list, err := ListTables(c.Request.Context(), dsn)
	if err != nil {
		httpx.Err(c, 500, 50031, "查表失败: "+err.Error())
		return
	}
	httpx.OK(c, list)
}

// ListColumns 应用库某表的列（列名/类型/可空/默认/注释）。
func (h *Handler) ListColumns(c *gin.Context) {
	aid := c.Param("aid")
	table := c.Param("table")
	if table == "" {
		httpx.Err(c, 400, 40031, "表名不能为空")
		return
	}
	dsn, err := h.appDSN(c, aid)
	if err != nil {
		httpx.Err(c, 404, 40431, err.Error())
		return
	}
	list, err := ListColumns(c.Request.Context(), dsn, table)
	if err != nil {
		httpx.Err(c, 500, 50031, "查列失败: "+err.Error())
		return
	}
	httpx.OK(c, list)
}

// ExecSQL 执行任意 SQL：SELECT 返回行+列，DDL/DML 返回影响行数。
// 无论成功失败都记 db_action_log（actor/action_type/statement/row_count/status/error）。
// 应用库连不上也要记日志（用 classifySQL 算 action_type）。
func (h *Handler) ExecSQL(c *gin.Context) {
	psID := c.Param("id")
	aid := c.Param("aid")
	actor := c.GetString(auth.CtxUserID)
	if actor == "" {
		actor = "unknown"
	}
	traceID := c.GetString("trace_id")

	var body struct {
		SQL string `json:"sql"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		httpx.Err(c, 400, 40031, "请求体无效: "+err.Error())
		return
	}
	stmt := strings.TrimSpace(body.SQL)
	if stmt == "" {
		httpx.Err(c, 400, 40031, "sql 不能为空")
		return
	}
	actionType := classifySQL(stmt)

	// 库名（审计用）+ DSN（执行用）；应用库记录不存在时 dbName 空，仍尝试执行（连不上会失败）
	ad, _ := h.store.GetAppDBByApp(c.Request.Context(), aid)
	dbName := ""
	if ad != nil {
		dbName = ad.DBName
	}
	dsn, _ := h.env.GetEnvValue(c.Request.Context(), aid, "DATABASE_URL")

	// 执行
	res, execErr := ExecSQL(c.Request.Context(), dsn, stmt)

	// 审计（无论成败）——记日志失败不阻塞响应（已用 _ 忽略，前端看 actions 时能看到有无记）
	al := &ActionLog{
		ID:             "dal_" + uuid.NewString(),
		ProjectSpaceID: psID,
		AppID:          aid,
		DBName:         dbName,
		Actor:          actor,
		ActionType:     actionType,
		Statement:      stmt,
		TraceID:        traceID,
	}
	if execErr != nil {
		al.Status = "failed"
		al.Error = execErr.Error()
	} else {
		al.Status = "success"
		if res != nil {
			al.RowCount = int(res.RowCount)
		}
	}
	_ = h.store.CreateActionLog(c.Request.Context(), al)

	if execErr != nil {
		httpx.Err(c, 400, 40031, "SQL 执行失败: "+execErr.Error())
		return
	}
	httpx.OK(c, res)
}

// ListActions 应用库最近的 SQL 操作日志（默认 50 条，按时间倒序）。
func (h *Handler) ListActions(c *gin.Context) {
	aid := c.Param("aid")
	list, err := h.store.ListActionLogs(c.Request.Context(), aid, 50)
	if err != nil {
		httpx.Err(c, 500, 50031, "查操作日志失败: "+err.Error())
		return
	}
	httpx.OK(c, list)
}

// appDSN 取应用库 DSN（env.GetEnvValue 读 DATABASE_URL）。空 → 报应用库不存在。
func (h *Handler) appDSN(c *gin.Context, aid string) (string, error) {
	ad, err := h.store.GetAppDBByApp(c.Request.Context(), aid)
	if err != nil || ad == nil {
		return "", fmt.Errorf("应用库不存在")
	}
	dsn, err := h.env.GetEnvValue(c.Request.Context(), aid, "DATABASE_URL")
	if err != nil || dsn == "" {
		return "", fmt.Errorf("应用库 DATABASE_URL 未配置")
	}
	return dsn, nil
}

// maskDSN 隐藏 DSN 密码：postgres://user:pwd@host:port/db → postgres://user:****@host:port/db
// 用 net/url 解析定位密码段，手动重组（不 url-encode ****，避免显示 %2A%2A%2A%2A）。
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
	q := ""
	if u.RawQuery != "" {
		q = "?" + u.RawQuery
	}
	return u.Scheme + "://" + u.User.Username() + ":****@" + u.Host + u.Path + q
}

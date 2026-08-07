package logsvc

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/httpx"
)

// Handler 日志 HTTP 接口。
type Handler struct {
	store *Store
}

// NewHandler 构造。
func NewHandler(store *Store) *Handler { return &Handler{store: store} }

// Register 注册路由（受认证保护：查询/统计/处理）。
func (h *Handler) Register(r gin.IRouter) {
	r.GET("/logs", h.List)
	r.GET("/logs/query", h.QueryLogs) // M5 增强：trace_id 精确 + q 关键词 + 时间窗
	r.GET("/logs/stats", h.Stats)
	r.GET("/logs/trend", h.Trend)
	r.GET("/logs/sources", h.SourceBreakdown)
	r.PATCH("/logs/:id/resolve", h.Resolve)
}

// RegisterPublicPost 注册公开 POST /logs（前端错误回传，不需认证）。
func (h *Handler) RegisterPublicPost(r gin.IRouter) {
	r.POST("/logs", h.Create)
}

// Create 前端/Python 回传日志。
func (h *Handler) Create(c *gin.Context) {
	var body struct {
		Level   string                 `json:"level" binding:"required"`
		Source  string                 `json:"source" binding:"required"`
		Message string                 `json:"message" binding:"required"`
		Fields  map[string]interface{} `json:"fields,omitempty"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		// 日志回传失败不能影响业务，静默丢弃
		c.Status(200)
		return
	}
	// 注入 trace_id / user_id（来自中间件）
	if body.Fields == nil {
		body.Fields = map[string]interface{}{}
	}
	if tid := c.GetString("trace_id"); tid != "" {
		body.Fields["trace_id"] = tid
	}
	if uid := c.GetString("user_id"); uid != "" {
		body.Fields["user_id"] = uid
	}
	if psID := c.GetString("project_space_id"); psID != "" {
		body.Fields["project_space_id"] = psID
	}
	// 静默处理（日志写入失败不影响用户）
	_ = h.store.CreateFromJSON(c.Request.Context(), body.Level, body.Source, body.Message, body.Fields)
	c.Status(200)
}

// List 查询日志。
func (h *Handler) List(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	list, err := h.store.Query(c.Request.Context(), QueryFilter{
		Level: c.Query("level"), Source: c.Query("source"), Module: c.Query("module"),
		Limit: limit, Offset: offset,
	})
	if err != nil {
		httpx.Err(c, 500, 50012, err.Error())
		return
	}
	httpx.OK(c, list)
}

// QueryLogs 增强查询（M5）：trace_id 精确 + q message 关键词 + 时间窗。
func (h *Handler) QueryLogs(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	list, err := h.store.Query(c.Request.Context(), QueryFilter{
		Level: c.Query("level"), Source: c.Query("source"), Module: c.Query("module"),
		TraceID: c.Query("trace_id"), Q: c.Query("q"),
		Since: c.Query("since"), Until: c.Query("until"),
		Limit: limit, Offset: offset,
	})
	if err != nil {
		httpx.Err(c, 500, 50012, err.Error())
		return
	}
	httpx.OK(c, list)
}

// Stats 日志统计。
func (h *Handler) Stats(c *gin.Context) {
	stats, err := h.store.Stats(c.Request.Context())
	if err != nil {
		httpx.Err(c, 500, 50012, err.Error())
		return
	}
	httpx.OK(c, stats)
}

// Resolve 标记已处理。
func (h *Handler) Resolve(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Err(c, 400, 40001, "invalid id")
		return
	}
	reviewer := c.GetString("user_id")
	if reviewer == "" {
		reviewer = "admin"
	}
	if err := h.store.Resolve(c.Request.Context(), id, reviewer); err != nil {
		httpx.Err(c, 500, 50012, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": id, "resolved": true})
}

// Trend 近 7 天 ERROR/WARN 趋势。
func (h *Handler) Trend(c *gin.Context) {
	list, err := h.store.Trend(c.Request.Context(), 7)
	if err != nil {
		httpx.Err(c, 500, 50012, err.Error())
		return
	}
	httpx.OK(c, list)
}

// SourceBreakdown 按来源+级别分布。
func (h *Handler) SourceBreakdown(c *gin.Context) {
	list, err := h.store.SourceBreakdown(c.Request.Context())
	if err != nil {
		httpx.Err(c, 500, 50012, err.Error())
		return
	}
	httpx.OK(c, list)
}

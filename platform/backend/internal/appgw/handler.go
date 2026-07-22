// Handler appgw 管理 API（调用日志查询；3c 看板数据源）。
// 反代本身挂在 root engine 的 /apps/*path（不经 /api/v1 全局鉴权，由 route.auth_required 决定）；
// 本 handler 挂在 /api/v1 下（走全局 AuthUser），仅暴露查询类管理 API。
package appgw

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/httpx"
)

// Register 注册 appgw 管理路由（v1 下，走全局鉴权）。
func Register(r gin.IRouter, store *Store) {
	h := &Handler{store: store}
	r.GET("/project-spaces/:id/apps/:aid/access-logs", h.ListAccessLogs)
}

// Handler appgw 管理 API handler。
type Handler struct {
	store *Store
}

// ListAccessLogs 应用最近的 appgw 调用日志（按时间倒序）。
// query limit（默认 50，上限 500）；按 app_id 过滤。
func (h *Handler) ListAccessLogs(c *gin.Context) {
	aid := c.Param("aid")
	if aid == "" {
		httpx.Err(c, 400, 40010, "app id 不能为空")
		return
	}
	limit := 50
	if s := c.Query("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			limit = n
		}
	}
	list, err := h.store.ListAccessLogs(c.Request.Context(), aid, limit)
	if err != nil {
		httpx.Err(c, 500, 50010, "查 access_log 失败: "+err.Error())
		return
	}
	httpx.OK(c, list)
}

package audit

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/httpx"
)

// Handler 操作审计 HTTP 接口（智能体/管理员查"谁何时做了什么"）。
type Handler struct {
	store *Store
}

// NewHandler 构造。
func NewHandler(store *Store) *Handler { return &Handler{store: store} }

// Register 注册路由（受认证保护）。
func (h *Handler) Register(r gin.IRouter) {
	r.GET("/operation-logs", h.List)
}

// List 查审计（按 actor/action/resource/since 筛选 + 分页）。
func (h *Handler) List(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	list, err := h.store.Query(c.Request.Context(),
		c.Query("actor_type"), c.Query("actor_id"), c.Query("action"),
		c.Query("resource_type"), c.Query("resource_id"), limit, offset)
	if err != nil {
		httpx.Err(c, 500, 50015, err.Error())
		return
	}
	httpx.OK(c, list)
}

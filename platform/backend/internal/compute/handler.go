package compute

import (
	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/httpx"
)

// Handler 算力资源 HTTP 接口。
type Handler struct {
	store *Store
}

// NewHandler 构造 Handler。
func NewHandler(store *Store) *Handler { return &Handler{store: store} }

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	r.GET("/project-spaces/:id/usage", h.List)
	r.GET("/project-spaces/:id/usage/stats", h.Stats)
}

// List 用量明细。
func (h *Handler) List(c *gin.Context) {
	list, err := h.store.List(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50010, err.Error())
		return
	}
	httpx.OK(c, list)
}

// Stats 用量统计看板。
func (h *Handler) Stats(c *gin.Context) {
	st, err := h.store.Stats(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50010, err.Error())
		return
	}
	httpx.OK(c, st)
}

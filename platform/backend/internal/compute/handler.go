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
//
// @Summary      算力用量明细
// @Tags         compute
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Success      200  {object}  map[string]interface{}  "用量明细列表"
// @Failure      500  {object}  map[string]interface{}  "服务端错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/usage [get]
func (h *Handler) List(c *gin.Context) {
	list, err := h.store.List(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50010, err.Error())
		return
	}
	httpx.OK(c, list)
}

// Stats 用量统计看板。
//
// @Summary      算力用量统计看板
// @Tags         compute
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Success      200  {object}  map[string]interface{}  "用量统计"
// @Failure      500  {object}  map[string]interface{}  "服务端错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/usage/stats [get]
func (h *Handler) Stats(c *gin.Context) {
	st, err := h.store.Stats(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50010, err.Error())
		return
	}
	httpx.OK(c, st)
}

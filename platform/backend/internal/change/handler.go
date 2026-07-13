package change

import (
	"errors"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/httpx"
)

// Handler 变更闸门 HTTP 接口。
type Handler struct {
	store *Store
}

// NewHandler 构造 Handler。
func NewHandler(store *Store) *Handler { return &Handler{store: store} }

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	r.GET("/changes", h.List)
	r.POST("/changes/:id/approve", h.Approve)
	r.POST("/changes/:id/reject", h.Reject)
}

// List 列出变更（?status=pending|approved|rejected，默认全部）。
func (h *Handler) List(c *gin.Context) {
	list, err := h.store.List(c.Request.Context(), c.Query("status"))
	if err != nil {
		httpx.Err(c, 500, 50007, err.Error())
		return
	}
	httpx.OK(c, list)
}

// Approve 批准（🚪G3 通过 → 合入）。
func (h *Handler) Approve(c *gin.Context) {
	if err := h.store.Decide(c.Request.Context(), c.Param("id"), "approved", reviewer(c)); err != nil {
		if errors.Is(err, errNotPending) {
			httpx.Err(c, 409, 40901, err.Error())
			return
		}
		httpx.Err(c, 500, 50007, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": c.Param("id"), "status": "approved", "message": "🚪G3 通过，可合入"})
}

// Reject 拒绝（需回滚/重做）。
func (h *Handler) Reject(c *gin.Context) {
	if err := h.store.Decide(c.Request.Context(), c.Param("id"), "rejected", reviewer(c)); err != nil {
		if errors.Is(err, errNotPending) {
			httpx.Err(c, 409, 40901, err.Error())
			return
		}
		httpx.Err(c, 500, 50007, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": c.Param("id"), "status": "rejected", "message": "已拒绝，需回滚或重做"})
}

// reviewer M1 取自请求头 X-User，默认 user。
func reviewer(c *gin.Context) string {
	if u := c.GetHeader("X-User"); u != "" {
		return u
	}
	return "user"
}

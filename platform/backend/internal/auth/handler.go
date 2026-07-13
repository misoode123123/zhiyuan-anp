package auth

import (
	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/httpx"
)

// Handler 成员管理 HTTP 接口。
type Handler struct {
	store *Store
}

// NewHandler 构造 Handler。
func NewHandler(store *Store) *Handler { return &Handler{store: store} }

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	r.GET("/project-spaces/:id/members", h.List)
	r.POST("/project-spaces/:id/members", h.Add)
}

// List 列出项目空间成员。
func (h *Handler) List(c *gin.Context) {
	list, err := h.store.ListMembers(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50011, err.Error())
		return
	}
	httpx.OK(c, list)
}

type addRequest struct {
	UserID string `json:"user_id" binding:"required"`
	Role   string `json:"role" binding:"required"`
}

// Add 把用户加入项目空间并分配角色。
func (h *Handler) Add(c *gin.Context) {
	var in addRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	m := &Member{UserID: in.UserID, ProjectSpaceID: c.Param("id"), Role: in.Role}
	if err := h.store.AddMember(c.Request.Context(), m); err != nil {
		httpx.Err(c, 500, 50011, err.Error())
		return
	}
	httpx.Created(c, m)
}

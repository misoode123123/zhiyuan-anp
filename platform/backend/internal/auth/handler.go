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

	// 用户目录（全局，非空间级）
	r.GET("/users", h.ListUsers)
	r.POST("/users", h.CreateUser)
	r.GET("/users/:uid", h.GetUser)
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

// ---------------- 用户目录 ----------------

// ListUsers 用户目录（含每用户的空间角色）。
func (h *Handler) ListUsers(c *gin.Context) {
	users, err := h.store.ListUsers(c.Request.Context())
	if err != nil {
		httpx.Err(c, 500, 50011, err.Error())
		return
	}
	// 附每用户的空间成员关系
	type userWithSpaces struct {
		User
		Spaces []Member `json:"spaces"`
	}
	out := make([]userWithSpaces, 0, len(users))
	for _, u := range users {
		ms, _ := h.store.SpacesOf(c.Request.Context(), u.Name)
		out = append(out, userWithSpaces{User: u, Spaces: ms})
	}
	httpx.OK(c, out)
}

type createUserBody struct {
	Name  string `json:"name" binding:"required"`
	Email string `json:"email"`
}

// CreateUser 新建用户。
func (h *Handler) CreateUser(c *gin.Context) {
	var in createUserBody
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	u := &User{Name: in.Name, Email: in.Email}
	if err := h.store.CreateUser(c.Request.Context(), u); err != nil {
		httpx.Err(c, 500, 50011, err.Error())
		return
	}
	httpx.Created(c, u)
}

// GetUser 用户详情（含空间角色）。
func (h *Handler) GetUser(c *gin.Context) {
	u, err := h.store.GetUser(c.Request.Context(), c.Param("uid"))
	if err != nil || u == nil || u.ID == "" {
		httpx.Err(c, 404, 40411, "用户不存在")
		return
	}
	ms, _ := h.store.SpacesOf(c.Request.Context(), u.Name)
	httpx.OK(c, gin.H{"user": u, "spaces": ms})
}

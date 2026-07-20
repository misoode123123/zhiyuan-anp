package auth

import (
	"strings"

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

	// 认证（登录/登出/当前用户；login 无需鉴权）
	r.POST("/auth/login", h.Login)
	r.POST("/auth/logout", h.Logout)
	r.GET("/auth/me", h.Me)
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
	Name     string `json:"name" binding:"required"`
	Email    string `json:"email"`
	Password string `json:"password"` // 可选;空=不设密码(用户登不了,需后续重置)
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
	if in.Password != "" {
		if err := h.store.SetPasswordByName(c.Request.Context(), in.Name, in.Password); err != nil {
			httpx.Err(c, 500, 50011, "用户已建但设密码失败: "+err.Error())
			return
		}
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

// ---------------- 认证 ----------------

type loginBody struct {
	Name     string `json:"name" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// Login 用户名+密码登录，返回 token（后续请求带 Authorization: Bearer <token>）。
//
// @Summary      用户登录
// @Tags         认证
// @Accept       json
// @Produce      json
// @Param        body  body      loginBody  true  "登录凭证(name+password)"
// @Success      200   {object}  map[string]interface{}  "code/message/data{token,user}"
// @Failure      400   {object}  map[string]interface{}  "invalid body"
// @Failure      401   {object}  map[string]interface{}  "凭证错误"
// @Router       /auth/login [post]
func (h *Handler) Login(c *gin.Context) {
	var in loginBody
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	token, u, err := h.store.Login(c.Request.Context(), in.Name, in.Password)
	if err != nil {
		httpx.Err(c, 401, 40101, err.Error())
		return
	}
	httpx.OK(c, gin.H{"token": token, "user": u})
}

// Logout 登出（吊销当前 token）。
//
// @Summary      登出
// @Tags         认证
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "logged_out"
// @Router       /auth/logout [post]
func (h *Handler) Logout(c *gin.Context) {
	if auth := c.GetHeader("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		_ = h.store.Logout(c.Request.Context(), strings.TrimPrefix(auth, "Bearer "))
	}
	httpx.OK(c, gin.H{"logged_out": true})
}

// Me 当前登录用户。
//
// @Summary      当前登录用户
// @Tags         认证
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "user"
// @Router       /auth/me [get]
func (h *Handler) Me(c *gin.Context) {
	httpx.OK(c, gin.H{"user": c.GetString(CtxUserID)})
}

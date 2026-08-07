package compute

import (
	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/auth"
	"zhiyuan-anp/platform/backend/internal/httpx"
)

// GrantHandler 用户模型授权管理 HTTP 接口。
// 4 端点：管理员视角的 list/grant/revoke（受 routeOps → config.manage 强制 RBAC）
// + 当前用户视角的 me/models（仅需登录，未登记 routeOps 故 AutoRequire 放行）。
type GrantHandler struct {
	store *Store
}

// NewGrantHandler 构造 GrantHandler。
func NewGrantHandler(store *Store) *GrantHandler { return &GrantHandler{store: store} }

// Register 注册授权路由（挂 v1 组，受 AutoRequire 保护）。
// 注意：/users/me/models 为静态路径，gin 优先匹配静态再匹配 :id，与 /users/:id/models 不冲突。
func (h *GrantHandler) Register(r gin.IRouter) {
	r.GET("/users/:id/models", h.list)
	r.POST("/users/:id/models", h.grant)
	r.DELETE("/users/:id/models/:model_id", h.revoke)
	r.GET("/users/me/models", h.listMine)
}

// list 查某用户已授权模型（管理员视角）。
//
// @Summary      查用户已授权模型
// @Tags         compute,grant
// @Produce      json
// @Param        id   path  string  true  "用户ID（usr_xxx）"
// @Success      200  {object}  httpx.Response{data=[]Model}
// @Failure      500  {object}  httpx.Response
// @Security     BearerAuth
// @Router       /users/{id}/models [get]
func (h *GrantHandler) list(c *gin.Context) {
	list, err := h.store.ListGrants(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50003, err.Error())
		return
	}
	httpx.OK(c, list)
}

type grantReq struct {
	ModelIDs []string `json:"model_ids"`
}

// grant 批量授权（管理员）。granted_by 取当前登录用户（user_db_id），用于审计。
//
// @Summary      批量授权模型给用户
// @Tags         compute,grant
// @Accept       json
// @Produce      json
// @Param        id    path  string  true  "用户ID（usr_xxx）"
// @Param        body  body  grantReq  true  "模型ID列表"
// @Success      200  {object}  httpx.Response{data=object}
// @Failure      400  {object}  httpx.Response
// @Failure      500  {object}  httpx.Response
// @Security     BearerAuth
// @Router       /users/{id}/models [post]
func (h *GrantHandler) grant(c *gin.Context) {
	var body grantReq
	if err := c.ShouldBindJSON(&body); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	grantedBy := c.GetString(auth.CtxUserDBID)
	granted, err := h.store.GrantModels(c.Request.Context(), c.Param("id"), body.ModelIDs, grantedBy)
	if err != nil {
		httpx.Err(c, 500, 50003, err.Error())
		return
	}
	httpx.OK(c, gin.H{"granted": granted})
}

// revoke 收回单个授权（管理员）。
//
// @Summary      收回用户某模型授权
// @Tags         compute,grant
// @Produce      json
// @Param        id         path  string  true  "用户ID（usr_xxx）"
// @Param        model_id   path  string  true  "模型ID（cmd_xxx）"
// @Success      200  {object}  httpx.Response{data=object}
// @Failure      500  {object}  httpx.Response
// @Security     BearerAuth
// @Router       /users/{id}/models/{model_id} [delete]
func (h *GrantHandler) revoke(c *gin.Context) {
	if err := h.store.RevokeModel(c.Request.Context(), c.Param("id"), c.Param("model_id")); err != nil {
		httpx.Err(c, 500, 50003, err.Error())
		return
	}
	httpx.OK(c, gin.H{"revoked": c.Param("model_id")})
}

// listMine 当前登录用户的可用模型（前端模型下拉用；仅需登录，不强制 RBAC）。
//
// @Summary      当前用户可用模型
// @Tags         compute,grant
// @Produce      json
// @Success      200  {object}  httpx.Response{data=[]Model}
// @Failure      500  {object}  httpx.Response
// @Security     BearerAuth
// @Router       /users/me/models [get]
func (h *GrantHandler) listMine(c *gin.Context) {
	uid := c.GetString(auth.CtxUserDBID)
	list, err := h.store.ListGrants(c.Request.Context(), uid)
	if err != nil {
		httpx.Err(c, 500, 50003, err.Error())
		return
	}
	httpx.OK(c, list)
}

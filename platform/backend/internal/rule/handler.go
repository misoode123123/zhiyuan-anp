package rule

import (
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"zhiyuan-anp/platform/backend/internal/httpx"
)

// Handler 规则治理 HTTP 接口。
type Handler struct {
	store *Store
	v     *validator.Validate
}

// NewHandler 构造 Handler。
func NewHandler(store *Store, v *validator.Validate) *Handler {
	return &Handler{store: store, v: v}
}

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	r.GET("/rules", h.List)
	r.POST("/rules", h.Create)
	r.PUT("/rules/:id", h.Update)
	r.PATCH("/rules/:id/enabled", h.SetEnabled)
	r.DELETE("/rules/:id", h.Delete)
	r.POST("/rules/check", h.Check)
}

// List 全部规则。
//
// @Summary      列出规则
// @Tags         rule
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "规则列表"
// @Security     BearerAuth
// @Router       /rules [get]
func (h *Handler) List(c *gin.Context) {
	list, err := h.store.List(c.Request.Context())
	if err != nil {
		httpx.Err(c, 500, 50006, err.Error())
		return
	}
	httpx.OK(c, list)
}

type createRequest struct {
	Name           string `json:"name" binding:"required"`
	Category       string `json:"category"`
	Type           string `json:"type"`
	Condition      string `json:"condition" binding:"required"`
	ConditionField string `json:"condition_field"`
	Action         string `json:"action"`
	Scope          string `json:"scope"`
	Enabled        bool   `json:"enabled"`
	Description    string `json:"description"`
}

func defaults(in *createRequest) {
	if in.Category == "" {
		in.Category = "general"
	}
	if in.Type == "" {
		in.Type = "mandatory"
	}
	if in.ConditionField == "" {
		in.ConditionField = "prompt"
	}
	if in.Action == "" {
		in.Action = "block"
	}
	if in.Scope == "" {
		in.Scope = "all"
	}
}

// Create 新建规则。
//
// @Summary      新建规则
// @Tags         rule
// @Accept       json
// @Produce      json
// @Param        body  body  createRequest  true  "规则(name/condition 等)"
// @Success      200  {object}  map[string]interface{}  "创建的规则"
// @Failure      400  {object}  map[string]interface{}  "invalid body"
// @Security     BearerAuth
// @Router       /rules [post]
func (h *Handler) Create(c *gin.Context) {
	var in createRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, err.Error())
		return
	}
	defaults(&in)
	r := &Rule{
		Name: in.Name, Category: in.Category, Type: in.Type,
		Condition: in.Condition, ConditionField: in.ConditionField,
		Action: in.Action, Scope: in.Scope, Enabled: true, // 新建默认启用
		Description: in.Description,
	}
	if err := h.store.Create(c.Request.Context(), r); err != nil {
		httpx.Err(c, 500, 50006, err.Error())
		return
	}
	httpx.Created(c, r)
}

type updateRequest struct {
	Name           string `json:"name" binding:"required"`
	Category       string `json:"category"`
	Type           string `json:"type"`
	Condition      string `json:"condition" binding:"required"`
	ConditionField string `json:"condition_field"`
	Action         string `json:"action"`
	Scope          string `json:"scope"`
	Enabled        bool   `json:"enabled"`
	Description    string `json:"description"`
}

// Update 更新规则。
//
// @Summary      更新规则
// @Tags         rule
// @Accept       json
// @Produce      json
// @Param        id    path  string         true  "规则ID"
// @Param        body  body  updateRequest  true  "规则新值"
// @Success      200  {object}  map[string]interface{}  "更新后的规则"
// @Failure      400  {object}  map[string]interface{}  "invalid body"
// @Security     BearerAuth
// @Router       /rules/{id} [put]
func (h *Handler) Update(c *gin.Context) {
	var in updateRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, err.Error())
		return
	}
	r := &Rule{
		ID: c.Param("id"), Name: in.Name, Category: in.Category, Type: in.Type,
		Condition: in.Condition, ConditionField: in.ConditionField,
		Action: in.Action, Scope: in.Scope, Enabled: in.Enabled, Description: in.Description,
	}
	if err := h.store.Update(c.Request.Context(), r); err != nil {
		httpx.Err(c, 500, 50006, err.Error())
		return
	}
	httpx.OK(c, r)
}

type setEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

// SetEnabled 启用/禁用。
//
// @Summary      启用/禁用规则
// @Tags         rule
// @Accept       json
// @Produce      json
// @Param        id    path  string             true  "规则ID"
// @Param        body  body  setEnabledRequest  true  "enabled"
// @Success      200  {object}  map[string]interface{}  "id/enabled"
// @Failure      400  {object}  map[string]interface{}  "invalid body"
// @Security     BearerAuth
// @Router       /rules/{id}/enabled [patch]
func (h *Handler) SetEnabled(c *gin.Context) {
	var in setEnabledRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, err.Error())
		return
	}
	if err := h.store.SetEnabled(c.Request.Context(), c.Param("id"), in.Enabled); err != nil {
		httpx.Err(c, 500, 50006, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": c.Param("id"), "enabled": in.Enabled})
}

// Delete 删除规则。
//
// @Summary      删除规则
// @Tags         rule
// @Produce      json
// @Param        id   path  string  true  "规则ID"
// @Success      200  {object}  map[string]interface{}  "id/deleted"
// @Security     BearerAuth
// @Router       /rules/{id} [delete]
func (h *Handler) Delete(c *gin.Context) {
	if err := h.store.Delete(c.Request.Context(), c.Param("id")); err != nil {
		httpx.Err(c, 500, 50006, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": c.Param("id"), "deleted": true})
}

type checkRequest struct {
	Scope   string `json:"scope"`
	Field   string `json:"field"`
	Content string `json:"content"`
}

// Check 手动校验（也可供其他模块内部调用）。
//
// @Summary      规则校验
// @Tags         rule
// @Accept       json
// @Produce      json
// @Param        body  body  checkRequest  true  "校验入参(scope/field/content)"
// @Success      200  {object}  map[string]interface{}  "violations/blocked"
// @Failure      400  {object}  map[string]interface{}  "invalid body"
// @Security     BearerAuth
// @Router       /rules/check [post]
func (h *Handler) Check(c *gin.Context) {
	var in checkRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, err.Error())
		return
	}
	engine := NewEngine(h.store)
	vs, err := engine.Check(c.Request.Context(), in.Scope, in.Field, in.Content)
	if err != nil {
		httpx.Err(c, 500, 50006, err.Error())
		return
	}
	httpx.OK(c, gin.H{"violations": vs, "blocked": HasBlock(vs)})
}

package standard

import (
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"zhiyuan-anp/platform/backend/internal/httpx"
)

// Handler 编码规范 HTTP 接口。
type Handler struct {
	store *Store
	v     *validator.Validate
}

// NewHandler 构造。
func NewHandler(store *Store, v *validator.Validate) *Handler {
	return &Handler{store: store, v: v}
}

// Register 模块级装配:main 调用,内部 new handler + 注册路由(减少 main.go 集中 new)。
func Register(r gin.IRouter, store *Store, v *validator.Validate) {
	NewHandler(store, v).Register(r)
}

// Register 注册路由：全局 /standards；项目级 /project-spaces/:id/standards[...]
func (h *Handler) Register(r gin.IRouter) {
	r.GET("/standards", h.ListGlobal)
	r.POST("/standards", h.CreateGlobal)
	r.PUT("/standards/:id", h.Update)
	r.PATCH("/standards/:id/enabled", h.SetEnabled)
	r.DELETE("/standards/:id", h.Delete)

	r.GET("/project-spaces/:id/standards", h.ListByPS)
	r.POST("/project-spaces/:id/standards", h.CreateByPS)
	r.GET("/project-spaces/:id/standards/effective", h.Effective)
}

// ListGlobal 全局规范。
//
// @Summary      列出全局规范
// @Tags         standard
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "全局规范列表"
// @Security     BearerAuth
// @Router       /standards [get]
func (h *Handler) ListGlobal(c *gin.Context) {
	list, err := h.store.ListGlobal(c.Request.Context())
	if err != nil {
		httpx.Err(c, 500, 50007, err.Error())
		return
	}
	httpx.OK(c, list)
}

type createBody struct {
	Name     string `json:"name" binding:"required"`
	Category string `json:"category"`
	Content  string `json:"content" binding:"required"`
	Priority int    `json:"priority"`
	Enabled  bool   `json:"enabled"`
}

func (b *createBody) defaults() {
	if b.Category == "" {
		b.Category = "general"
	}
	if b.Priority == 0 {
		b.Priority = 100
	}
}

// CreateGlobal 新建全局规范（强制 project_space_id=nil）。
//
// @Summary      新建全局规范
// @Tags         standard
// @Accept       json
// @Produce      json
// @Param        body  body  createBody  true  "规范(name/content 等)"
// @Success      200  {object}  map[string]interface{}  "创建的规范"
// @Failure      400  {object}  map[string]interface{}  "invalid body"
// @Security     BearerAuth
// @Router       /standards [post]
func (h *Handler) CreateGlobal(c *gin.Context) {
	var in createBody
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, err.Error())
		return
	}
	in.defaults()
	st := &Standard{Name: in.Name, Category: in.Category, Content: in.Content, Priority: in.Priority, Enabled: true}
	if err := h.store.Create(c.Request.Context(), st); err != nil {
		httpx.Err(c, 500, 50007, err.Error())
		return
	}
	httpx.Created(c, st)
}

// CreateByPS 新建项目级规范（project_space_id 取自路径，不信任请求体）。
//
// @Summary      新建项目级规范
// @Tags         standard
// @Accept       json
// @Produce      json
// @Param        id    path  string      true  "项目空间ID"
// @Param        body  body  createBody  true  "规范(name/content 等)"
// @Success      200  {object}  map[string]interface{}  "创建的规范"
// @Failure      400  {object}  map[string]interface{}  "invalid body"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/standards [post]
func (h *Handler) CreateByPS(c *gin.Context) {
	var in createBody
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, err.Error())
		return
	}
	in.defaults()
	psID := c.Param("id")
	st := &Standard{ProjectSpaceID: &psID, Name: in.Name, Category: in.Category, Content: in.Content, Priority: in.Priority, Enabled: true}
	if err := h.store.Create(c.Request.Context(), st); err != nil {
		httpx.Err(c, 500, 50007, err.Error())
		return
	}
	httpx.Created(c, st)
}

// ListByPS 项目级规范。
//
// @Summary      列出项目级规范
// @Tags         standard
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Success      200  {object}  map[string]interface{}  "项目级规范列表"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/standards [get]
func (h *Handler) ListByPS(c *gin.Context) {
	list, err := h.store.ListByProjectSpace(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50007, err.Error())
		return
	}
	httpx.OK(c, list)
}

// Effective 预览某空间生效规范（全局+项目级）+ 拼出的 prompt 片段。
//
// @Summary      预览生效规范
// @Tags         standard
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Success      200  {object}  map[string]interface{}  "standards/prompt_section"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/standards/effective [get]
func (h *Handler) Effective(c *gin.Context) {
	list, err := h.store.ListEffective(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50007, err.Error())
		return
	}
	httpx.OK(c, gin.H{"standards": list, "prompt_section": BuildPromptSection(list)})
}

type updateBody struct {
	Name     string `json:"name" binding:"required"`
	Category string `json:"category"`
	Content  string `json:"content" binding:"required"`
	Priority int    `json:"priority"`
	Enabled  bool   `json:"enabled"`
}

// Update 改（层级不可改）。
//
// @Summary      更新规范
// @Tags         standard
// @Accept       json
// @Produce      json
// @Param        id    path  string      true  "规范ID"
// @Param        body  body  updateBody  true  "规范新值"
// @Success      200  {object}  map[string]interface{}  "更新后的规范"
// @Failure      400  {object}  map[string]interface{}  "invalid body"
// @Security     BearerAuth
// @Router       /standards/{id} [put]
func (h *Handler) Update(c *gin.Context) {
	var in updateBody
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, err.Error())
		return
	}
	st := &Standard{ID: c.Param("id"), Name: in.Name, Category: in.Category, Content: in.Content, Priority: in.Priority, Enabled: in.Enabled}
	if err := h.store.Update(c.Request.Context(), st); err != nil {
		httpx.Err(c, 500, 50007, err.Error())
		return
	}
	httpx.OK(c, st)
}

type setEnabledBody struct {
	Enabled bool `json:"enabled"`
}

// SetEnabled 启用/禁用规范。
//
// @Summary      启用/禁用规范
// @Tags         standard
// @Accept       json
// @Produce      json
// @Param        id    path  string          true  "规范ID"
// @Param        body  body  setEnabledBody  true  "enabled"
// @Success      200  {object}  map[string]interface{}  "id/enabled"
// @Failure      400  {object}  map[string]interface{}  "invalid body"
// @Security     BearerAuth
// @Router       /standards/{id}/enabled [patch]
func (h *Handler) SetEnabled(c *gin.Context) {
	var in setEnabledBody
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, err.Error())
		return
	}
	if err := h.store.SetEnabled(c.Request.Context(), c.Param("id"), in.Enabled); err != nil {
		httpx.Err(c, 500, 50007, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": c.Param("id"), "enabled": in.Enabled})
}

// Delete 删除。
//
// @Summary      删除规范
// @Tags         standard
// @Produce      json
// @Param        id   path  string  true  "规范ID"
// @Success      200  {object}  map[string]interface{}  "id/deleted"
// @Security     BearerAuth
// @Router       /standards/{id} [delete]
func (h *Handler) Delete(c *gin.Context) {
	if err := h.store.Delete(c.Request.Context(), c.Param("id")); err != nil {
		httpx.Err(c, 500, 50007, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": c.Param("id"), "deleted": true})
}

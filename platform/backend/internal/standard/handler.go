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
func (h *Handler) ListByPS(c *gin.Context) {
	list, err := h.store.ListByProjectSpace(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50007, err.Error())
		return
	}
	httpx.OK(c, list)
}

// Effective 预览某空间生效规范（全局+项目级）+ 拼出的 prompt 片段。
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
func (h *Handler) Delete(c *gin.Context) {
	if err := h.store.Delete(c.Request.Context(), c.Param("id")); err != nil {
		httpx.Err(c, 500, 50007, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": c.Param("id"), "deleted": true})
}

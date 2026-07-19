package workspace

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"zhiyuan-anp/platform/backend/internal/httpx"
)

// Handler 项目空间 HTTP 接口。
type Handler struct {
	svc      *Service
	validate *validator.Validate
}

// NewHandler 构造 Handler。
func NewHandler(svc *Service, v *validator.Validate) *Handler {
	return &Handler{svc: svc, validate: v}
}

// Register 注册路由到给定 group（由 main 挂到 /api/v1）。
func (h *Handler) Register(r gin.IRouter) {
	r.POST("/project-spaces", h.CreateProjectSpace)
	r.GET("/project-spaces", h.ListProjectSpaces)
	r.GET("/project-spaces/:id", h.GetProjectSpace)
	r.GET("/project-spaces/:id/overview", h.Overview)
	r.POST("/project-spaces/:id/projects", h.CreateProject)
	r.GET("/project-spaces/:id/projects", h.ListProjects)
}

// CreateProjectSpace 创建项目空间。
//
// @Summary      创建项目空间
// @Tags         workspace
// @Accept       json
// @Produce      json
// @Param        body  body  CreateProjectSpaceInput  true  "项目空间(name+slug)"
// @Success      200  {object}  map[string]interface{}  "创建的项目空间"
// @Failure      400  {object}  map[string]interface{}  "invalid body"
// @Security     BearerAuth
// @Router       /project-spaces [post]
func (h *Handler) CreateProjectSpace(c *gin.Context) {
	var in CreateProjectSpaceInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	if err := h.validate.Struct(in); err != nil {
		httpx.Err(c, 400, 40002, err.Error())
		return
	}
	ps, err := h.svc.CreateProjectSpace(c.Request.Context(), in)
	if err != nil {
		httpx.Err(c, 500, 50001, err.Error())
		return
	}
	httpx.Created(c, ps)
}

// ListProjectSpaces 列出项目空间。
//
// @Summary      列出项目空间
// @Tags         workspace
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "项目空间列表"
// @Security     BearerAuth
// @Router       /project-spaces [get]
func (h *Handler) ListProjectSpaces(c *gin.Context) {
	list, err := h.svc.ListProjectSpaces(c.Request.Context())
	if err != nil {
		httpx.Err(c, 500, 50001, err.Error())
		return
	}
	httpx.OK(c, list)
}

// GetProjectSpace 获取项目空间详情。
//
// @Summary      获取项目空间详情
// @Tags         workspace
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Success      200  {object}  map[string]interface{}  "项目空间"
// @Failure      404  {object}  map[string]interface{}  "project space not found"
// @Security     BearerAuth
// @Router       /project-spaces/{id} [get]
func (h *Handler) GetProjectSpace(c *gin.Context) {
	ps, err := h.svc.GetProjectSpace(c.Request.Context(), c.Param("id"))
	if errors.Is(err, ErrNotFound) {
		httpx.Err(c, 404, 40401, "project space not found")
		return
	}
	if err != nil {
		httpx.Err(c, 500, 50001, err.Error())
		return
	}
	httpx.OK(c, ps)
}

// Overview 空间概览：元信息 + 成员/应用/需求/变更/发布计数。
//
// @Summary      项目空间概览
// @Tags         workspace
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Success      200  {object}  map[string]interface{}  "概览统计"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/overview [get]
func (h *Handler) Overview(c *gin.Context) {
	o, err := h.svc.Overview(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50001, err.Error())
		return
	}
	httpx.OK(c, o)
}

// CreateProject 在指定项目空间下创建项目（project_space_id 只取自路径，多租户隔离）。
//
// @Summary      创建项目
// @Tags         workspace
// @Accept       json
// @Produce      json
// @Param        id    path  string              true  "项目空间ID"
// @Param        body  body  CreateProjectInput  true  "项目(name+slug)"
// @Success      200  {object}  map[string]interface{}  "创建的项目"
// @Failure      400  {object}  map[string]interface{}  "invalid body"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/projects [post]
func (h *Handler) CreateProject(c *gin.Context) {
	// 多租户隔离：project_space_id 只取自路径，不信任请求体
	psID := c.Param("id")
	var in CreateProjectInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, err.Error())
		return
	}
	if err := h.validate.Struct(in); err != nil {
		httpx.Err(c, 400, 40002, err.Error())
		return
	}
	p, err := h.svc.CreateProject(c.Request.Context(), psID, in)
	if err != nil {
		httpx.Err(c, 500, 50001, err.Error())
		return
	}
	httpx.Created(c, p)
}

// ListProjects 列出项目空间下的项目。
//
// @Summary      列出项目空间下的项目
// @Tags         workspace
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Success      200  {object}  map[string]interface{}  "项目列表"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/projects [get]
func (h *Handler) ListProjects(c *gin.Context) {
	list, err := h.svc.ListProjects(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50001, err.Error())
		return
	}
	httpx.OK(c, list)
}

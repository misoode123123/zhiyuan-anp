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
	r.POST("/project-spaces/:id/projects", h.CreateProject)
	r.GET("/project-spaces/:id/projects", h.ListProjects)
}

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

func (h *Handler) ListProjectSpaces(c *gin.Context) {
	list, err := h.svc.ListProjectSpaces(c.Request.Context())
	if err != nil {
		httpx.Err(c, 500, 50001, err.Error())
		return
	}
	httpx.OK(c, list)
}

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

func (h *Handler) ListProjects(c *gin.Context) {
	list, err := h.svc.ListProjects(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50001, err.Error())
		return
	}
	httpx.OK(c, list)
}

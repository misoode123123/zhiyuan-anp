package requirement

import (
	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/httpx"
)

// Handler 需求工作台 HTTP 接口。
type Handler struct {
	svc *Service
}

// NewHandler 构造 Handler。
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	r.POST("/project-spaces/:id/requirements", h.Create)
	r.GET("/project-spaces/:id/requirements", h.List)
	r.POST("/project-spaces/:id/requirements/:rid/dispatch-code", h.DispatchCode)
}

type createRequest struct {
	Description string   `json:"description" binding:"required"`
	Images      []string `json:"images,omitempty"` // data URL 或 http URL
}

// Create 业务描述（可带图片）→ AI 生成规格（多模态走 GLM-4V）→ 入库。
func (h *Handler) Create(c *gin.Context) {
	var in createRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	psID := c.Param("id")
	req, err := h.svc.Create(c.Request.Context(), CreateInput{
		ProjectSpaceID: psID, Description: in.Description, Images: in.Images,
	})
	if err != nil {
		httpx.Err(c, 500, 50003, err.Error())
		return
	}
	httpx.Created(c, req)
}

// List 列出项目空间下的需求。
func (h *Handler) List(c *gin.Context) {
	list, err := h.svc.List(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50003, err.Error())
		return
	}
	httpx.OK(c, list)
}

type dispatchRequest struct {
	RepoDir string `json:"repo_dir" binding:"required"`
	Model   string `json:"model,omitempty"`
}

// DispatchCode 需求规格 → 异步编码（立即返回 task_id）。
func (h *Handler) DispatchCode(c *gin.Context) {
	var in dispatchRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	psID := c.Param("id")
	rid := c.Param("rid")
	t, err := h.svc.Dispatch(c.Request.Context(), psID, rid, in.RepoDir, in.Model)
	if err != nil {
		httpx.Err(c, 500, 50004, err.Error())
		return
	}
	httpx.OK(c, gin.H{
		"requirement_id": rid,
		"task_id":        t.ID,
		"status":         "running",
		"note":           "异步编码中，轮询 GET /api/v1/code-tasks/:id 查进度",
	})
}

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
	r.GET("/project-spaces/:id/apps/:aid/requirements", h.ListByApp) // 应用一等公民：应用的需求池
	r.POST("/project-spaces/:id/requirements/:rid/dispatch-code", h.DispatchCode)
	r.POST("/project-spaces/:id/requirements/:rid/breakdown", h.Breakdown) // AI 拆解需求→子任务
}

type createRequest struct {
	ApplicationID string   `json:"application_id,omitempty"` // 可选：归属应用
	Description   string   `json:"description" binding:"required"`
	Images        []string `json:"images,omitempty"` // data URL 或 http URL
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
		ProjectSpaceID: psID, ApplicationID: in.ApplicationID, Description: in.Description, Images: in.Images,
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

// ListByApp 列出某应用的需求池。
func (h *Handler) ListByApp(c *gin.Context) {
	list, err := h.svc.ListByApp(c.Request.Context(), c.Param("aid"))
	if err != nil {
		httpx.Err(c, 500, 50003, err.Error())
		return
	}
	httpx.OK(c, list)
}

type dispatchRequest struct {
	RepoDir string `json:"repo_dir,omitempty"` // 可选；空=用需求归属应用的托管仓库
	Model   string `json:"model,omitempty"`
}

// Breakdown AI 把需求拆成子任务清单,存 tasks 并返回。
func (h *Handler) Breakdown(c *gin.Context) {
	tasks, err := h.svc.Breakdown(c.Request.Context(), c.Param("rid"))
	if err != nil {
		httpx.Err(c, 500, 50003, err.Error())
		return
	}
	httpx.OK(c, gin.H{"tasks": tasks})
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

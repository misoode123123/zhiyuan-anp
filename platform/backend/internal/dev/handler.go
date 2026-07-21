package dev

import (
	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/httpx"
)

// Handler 研发工作台 HTTP 接口（异步编码）。
type Handler struct {
	agent *CodingAgent
}

// NewHandler 构造 Handler。
func NewHandler(agent *CodingAgent) *Handler { return &Handler{agent: agent} }

// Register 模块级装配：内部 new + Register，供 main 直接调用。
func Register(r gin.IRouter, agent *CodingAgent) { NewHandler(agent).Register(r) }

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	r.POST("/code", h.Code)
	r.GET("/code-tasks/:id", h.GetTask)
	r.GET("/project-spaces/:id/code-tasks", h.ListTasks)
}

type codeRequest struct {
	RepoDir string `json:"repo_dir" binding:"required"`
	Prompt  string `json:"prompt" binding:"required"`
	Model   string `json:"model,omitempty"`
}

// Code 异步提交编码任务，立即返回 task_id。
//
// @Summary      异步提交编码任务（立即返回 task_id）
// @Tags         dev
// @Accept       json
// @Produce      json
// @Param        body  body  codeRequest  true  "编码任务(repo_dir+prompt+model)"
// @Success      200  {object}  map[string]interface{}  "task_id+status=running+note"
// @Failure      400  {object}  map[string]interface{}  "invalid body"
// @Failure      500  {object}  map[string]interface{}  "提交失败"
// @Security     BearerAuth
// @Router       /code [post]
func (h *Handler) Code(c *gin.Context) {
	var req codeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	psID := c.GetString("project_space_id")
	t, err := h.agent.Submit(c.Request.Context(), psID, "code", "", req.RepoDir, req.Prompt, req.Model)
	if err != nil {
		httpx.Err(c, 500, 50002, err.Error())
		return
	}
	httpx.OK(c, gin.H{
		"task_id": t.ID,
		"status":  "running",
		"note":    "异步执行中，轮询 GET /api/v1/code-tasks/:id 查进度",
	})
}

// GetTask 查询异步任务状态/产出。
//
// @Summary      查询编码任务状态/产出
// @Tags         dev
// @Produce      json
// @Param        id  path  string  true  "任务ID"
// @Success      200  {object}  map[string]interface{}  "任务详情"
// @Failure      404  {object}  map[string]interface{}  "任务不存在"
// @Security     BearerAuth
// @Router       /code-tasks/{id} [get]
func (h *Handler) GetTask(c *gin.Context) {
	t, err := h.agent.tasks.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 404, 40402, "任务不存在")
		return
	}
	httpx.OK(c, t)
}

// ListTasks 列出项目空间的编码任务。
//
// @Summary      列出项目空间的编码任务
// @Tags         dev
// @Produce      json
// @Param        id  path  string  true  "项目空间ID"
// @Success      200  {object}  map[string]interface{}  "任务列表"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/code-tasks [get]
func (h *Handler) ListTasks(c *gin.Context) {
	list, err := h.agent.tasks.ListByProjectSpace(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50002, err.Error())
		return
	}
	httpx.OK(c, list)
}

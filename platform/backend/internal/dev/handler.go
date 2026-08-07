package dev

import (
	"context"
	"log"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/auth"
	"zhiyuan-anp/platform/backend/internal/httpx"
)

// computeGrantChecker 授权校验接口（局部，鸭子类型解耦对 compute.Store 的直接依赖）。
// *compute.Store 实现了 IsGranted，满足此接口；测试可传 fake。
type computeGrantChecker interface {
	IsGranted(ctx context.Context, userID, modelID string) (bool, error)
}

// Handler 研发工作台 HTTP 接口（异步编码）。
type Handler struct {
	agent *CodingAgent
	grant computeGrantChecker // 可为 nil（则跳过 /code 授权校验，兼容无 computeStore 的场景）
}

// NewHandler 构造 Handler。grant 可为 nil（跳过 /code 授权校验）。
func NewHandler(agent *CodingAgent, grant computeGrantChecker) *Handler {
	return &Handler{agent: agent, grant: grant}
}

// Register 模块级装配：内部 new + Register，供 main 直接调用。
func Register(r gin.IRouter, agent *CodingAgent, grant computeGrantChecker) {
	NewHandler(agent, grant).Register(r)
}

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
	// 授权校验（第二道防线）：/code 不经 Gateway，故在派发前单独校验。
	// req.Model 非空且 grant 可用时，校验当前用户是否被授权该模型；越权即拒 403，不 fallback。
	// Model 空=走默认路由（兼容旧调用）；grant nil=未注入 computeStore（跳过，兼容）。
	if req.Model != "" && h.grant != nil {
		uid := c.GetString(auth.CtxUserDBID)
		ok, err := h.grant.IsGranted(c.Request.Context(), uid, req.Model)
		if err != nil {
			// fail-closed：DB 校验出错时保守按未授权拒绝（err 时 ok=false → 落入 !ok 分支返 403）。
			// 记 warn 供 ops 可见（对齐 gateway route.go，用 stdlib log；注入 zap 超出本修复范围）。
			// 日志含 userID+model，不含 key。
			log.Printf("warn: IsGranted 校验出错 user=%s model=%s: %v", uid, req.Model, err)
		}
		if !ok {
			httpx.Err(c, 403, 40302, "无权使用该模型")
			return
		}
	}
	psID := c.GetString("project_space_id")
	t, err := h.agent.Submit(c.Request.Context(), psID, c.GetString(auth.CtxUserDBID), "code", "", req.RepoDir, req.Prompt, req.Model)
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

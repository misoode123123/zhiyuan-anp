package requirement

import (
	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/auth"
	"zhiyuan-anp/platform/backend/internal/change"
	"zhiyuan-anp/platform/backend/internal/httpx"
)

// Handler 需求工作台 HTTP 接口。
type Handler struct {
	svc       *Service
	chgStore  *change.Store  // 变更(用于 my-tasks 聚合待审批/上线);nil=不聚合
	authStore *auth.Store    // 用户角色(用于 my-tasks RBAC 过滤);nil=不过滤
}

// NewHandler 构造 Handler。chgStore/authStore 可为 nil。
func NewHandler(svc *Service, chgStore *change.Store, authStore *auth.Store) *Handler {
	return &Handler{svc: svc, chgStore: chgStore, authStore: authStore}
}

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	r.GET("/project-spaces/:id/my-tasks", h.MyTasks) // 我的任务聚合(待认领/开发中/待审批/待上线)
	r.POST("/project-spaces/:id/requirements", h.Create)
	r.GET("/project-spaces/:id/requirements", h.List)
	r.GET("/project-spaces/:id/apps/:aid/requirements", h.ListByApp) // 应用一等公民：应用的需求池
	r.POST("/project-spaces/:id/requirements/:rid/dispatch-code", h.DispatchCode)
	r.POST("/project-spaces/:id/requirements/:rid/breakdown", h.Breakdown) // AI 拆解需求→子任务
	r.POST("/project-spaces/:id/requirements/:rid/assign", h.Assign)       // 认领需求(互斥)
	r.POST("/project-spaces/:id/requirements/:rid/release", h.Release)     // 释放认领
}

// MyTasks 聚合当前用户各开发阶段的待办(待认领需求/我的开发中/待审批变更/待上线变更),供首页"我的任务"。
//
// @Summary      我的任务聚合
// @Tags         requirement
// @Produce      json
// @Param        id      path    string  true   "项目空间ID"
// @Param        X-User  header  string  false  "用户名(默认 anonymous)"
// @Success      200  {object}  map[string]interface{}  "roles/toClaim/myDev/toApprove/toRelease"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/my-tasks [get]
func (h *Handler) MyTasks(c *gin.Context) {
	psID := c.Param("id")
	user := c.GetHeader("X-User")
	if user == "" {
		user = "anonymous"
	}
	ctx := c.Request.Context()
	reqs, _ := h.svc.List(ctx, psID)
	toClaim, myDev := []Requirement{}, []Requirement{}
	for _, q := range reqs {
		if q.Assignee == "" && q.Status == "specified" {
			toClaim = append(toClaim, q)
		}
		if q.Assignee == user && q.Status == "developing" {
			myDev = append(myDev, q)
		}
	}
	toApprove, toRelease := []change.ChangeRequest{}, []change.ChangeRequest{}
	if h.chgStore != nil {
		chgs, _ := h.chgStore.List(ctx, "")
		for _, ch := range chgs {
			if ch.Status == "pending" {
				toApprove = append(toApprove, ch)
			}
			if ch.Status == "approved" {
				toRelease = append(toRelease, ch)
			}
		}
	}
	// RBAC:查用户角色(先按 name 解析 user_id),前端据此过滤可见阶段
	roles := []string{}
	if h.authStore != nil {
		if u, err := h.authStore.GetUserByName(ctx, user); err == nil && u != nil {
			if r, _ := h.authStore.Roles(ctx, u.ID, psID); r != nil {
				roles = r
			}
		}
	}
	httpx.OK(c, gin.H{"roles": roles, "toClaim": toClaim, "myDev": myDev, "toApprove": toApprove, "toRelease": toRelease})
}

type createRequest struct {
	ApplicationID string   `json:"application_id,omitempty"` // 可选：归属应用
	Description   string   `json:"description" binding:"required"`
	Images        []string `json:"images,omitempty"` // data URL 或 http URL
}

// Create 业务描述（可带图片）→ AI 生成规格（多模态走 GLM-4V）→ 入库。
//
// @Summary      创建需求(AI生成规格)
// @Tags         requirement
// @Accept       json
// @Produce      json
// @Param        id    path  string         true  "项目空间ID"
// @Param        body  body  createRequest  true  "需求(application_id?/description/images?)"
// @Success      200  {object}  map[string]interface{}  "创建的需求"
// @Failure      400  {object}  map[string]interface{}  "invalid body"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/requirements [post]
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
//
// @Summary      列出项目空间需求
// @Tags         requirement
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Success      200  {object}  map[string]interface{}  "需求列表"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/requirements [get]
func (h *Handler) List(c *gin.Context) {
	list, err := h.svc.List(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50003, err.Error())
		return
	}
	httpx.OK(c, list)
}

// ListByApp 列出某应用的需求池。
//
// @Summary      列出应用需求池
// @Tags         requirement
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Param        aid  path  string  true  "应用ID"
// @Success      200  {object}  map[string]interface{}  "需求列表"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/requirements [get]
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
//
// @Summary      AI拆解需求为子任务
// @Tags         requirement
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Param        rid  path  string  true  "需求ID"
// @Success      200  {object}  map[string]interface{}  "tasks"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/requirements/{rid}/breakdown [post]
func (h *Handler) Breakdown(c *gin.Context) {
	tasks, err := h.svc.Breakdown(c.Request.Context(), c.Param("rid"))
	if err != nil {
		httpx.Err(c, 500, 50003, err.Error())
		return
	}
	httpx.OK(c, gin.H{"tasks": tasks})
}

// Assign 认领需求(互斥:已被他人认领返回 409)。
//
// @Summary      认领需求
// @Tags         requirement
// @Produce      json
// @Param        id      path    string  true   "项目空间ID"
// @Param        rid     path    string  true   "需求ID"
// @Param        X-User  header  string  false  "用户名(默认 anonymous)"
// @Success      200  {object}  map[string]interface{}  "assigned_to"
// @Failure      409  {object}  map[string]interface{}  "已被他人认领"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/requirements/{rid}/assign [post]
func (h *Handler) Assign(c *gin.Context) {
	user := c.GetHeader("X-User")
	if user == "" {
		user = "anonymous"
	}
	if err := h.svc.Assign(c.Request.Context(), c.Param("rid"), user); err != nil {
		httpx.Err(c, 409, 40901, err.Error())
		return
	}
	httpx.OK(c, gin.H{"assigned_to": user})
}

// Release 释放需求认领。
//
// @Summary      释放需求认领
// @Tags         requirement
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Param        rid  path  string  true  "需求ID"
// @Success      200  {object}  map[string]interface{}  "released"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/requirements/{rid}/release [post]
func (h *Handler) Release(c *gin.Context) {
	if err := h.svc.Release(c.Request.Context(), c.Param("rid")); err != nil {
		httpx.Err(c, 500, 50003, err.Error())
		return
	}
	httpx.OK(c, gin.H{"released": true})
}

// DispatchCode 需求规格 → 异步编码（立即返回 task_id）。
//
// @Summary      派发编码任务
// @Tags         requirement
// @Accept       json
// @Produce      json
// @Param        id    path  string           true  "项目空间ID"
// @Param        rid   path  string           true  "需求ID"
// @Param        body  body  dispatchRequest  true  "编码参数(repo_dir?/model?)"
// @Success      200  {object}  map[string]interface{}  "task_id/status"
// @Failure      400  {object}  map[string]interface{}  "invalid body"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/requirements/{rid}/dispatch-code [post]
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

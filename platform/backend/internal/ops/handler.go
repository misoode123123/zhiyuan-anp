package ops

import (
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"zhiyuan-anp/platform/backend/internal/httpx"
)

// Handler 运维中心 HTTP 接口。
type Handler struct {
	store           *Store
	agentRuntimeURL string
	v               *validator.Validate
}

// NewHandler 构造 Handler。agentRuntimeURL 用于健康探测。
func NewHandler(store *Store, agentRuntimeURL string, v *validator.Validate) *Handler {
	return &Handler{store: store, agentRuntimeURL: agentRuntimeURL, v: v}
}

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	// 看板与健康
	r.GET("/project-spaces/:id/ops/dashboard", h.Dashboard)
	r.GET("/project-spaces/:id/ops/health", h.Health)
	r.POST("/project-spaces/:id/ops/inspect", h.Inspect) // 巡检：跑健康检查，失败自动告警

	// 告警
	r.GET("/project-spaces/:id/ops/alerts", h.ListAlerts)
	r.POST("/project-spaces/:id/ops/alerts", h.CreateAlert)
	r.POST("/project-spaces/:id/ops/alerts/:aid/resolve", h.ResolveAlert)

	// SOP 预案
	r.GET("/project-spaces/:id/ops/sops", h.ListSOPs)
	r.POST("/project-spaces/:id/ops/sops", h.CreateSOP)
	r.PUT("/project-spaces/:id/ops/sops/:sid", h.UpdateSOP)
	r.DELETE("/project-spaces/:id/ops/sops/:sid", h.DeleteSOP)
}

// Dashboard 运维总览看板（健康 + 统计 + 用量 + 活动 + 告警计数）。
//
// @Summary      运维总览看板
// @Tags         ops
// @Produce      json
// @Param        id  path  string  true  "项目空间ID"
// @Success      200  {object}  map[string]interface{}  "看板(健康+统计+用量+活动+告警计数)"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/ops/dashboard [get]
func (h *Handler) Dashboard(c *gin.Context) {
	psID := c.Param("id")
	ctx := c.Request.Context()

	comps := h.store.Components(ctx, h.agentRuntimeURL)
	stats, err := h.store.Stats(ctx, psID)
	if err != nil {
		httpx.Err(c, 500, 50070, err.Error())
		return
	}
	usage, err := h.store.Usage(ctx, psID)
	if err != nil {
		httpx.Err(c, 500, 50070, err.Error())
		return
	}
	activity, err := h.store.Activity(ctx, psID)
	if err != nil {
		httpx.Err(c, 500, 50070, err.Error())
		return
	}
	open, _ := h.store.CountOpenAlerts(ctx, psID)
	httpx.OK(c, Dashboard{
		OverallHealth: OverallHealth(comps),
		Components:    comps,
		Stats:         stats,
		Usage:         usage,
		Activity:      activity,
		OpenAlerts:    open,
	})
}

// Health 组件健康详情。
//
// @Summary      组件健康详情
// @Tags         ops
// @Produce      json
// @Param        id  path  string  true  "项目空间ID"
// @Success      200  {object}  map[string]interface{}  "总体健康+组件健康列表"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/ops/health [get]
func (h *Handler) Health(c *gin.Context) {
	comps := h.store.Components(c.Request.Context(), h.agentRuntimeURL)
	httpx.OK(c, gin.H{"overall": OverallHealth(comps), "components": comps})
}

// Inspect 触发巡检：跑健康检查，对 down/degraded 组件自动产生告警（去重），返回报告。
//
// @Summary      触发巡检(自动告警)
// @Tags         ops
// @Produce      json
// @Param        id  path  string  true  "项目空间ID"
// @Success      200  {object}  map[string]interface{}  "巡检报告(健康+新增/抑制告警计数)"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/ops/inspect [post]
func (h *Handler) Inspect(c *gin.Context) {
	psID := c.Param("id")
	ctx := c.Request.Context()
	comps := h.store.Components(ctx, h.agentRuntimeURL)
	created := 0
	for _, comp := range comps {
		if comp.Status == "healthy" {
			continue
		}
		sev := "critical"
		if comp.Status == "degraded" {
			sev = "warning"
		}
		title := "组件异常: " + comp.Name
		fp := fingerprint("patrol", title)
		exist, err := h.store.HasFiringFingerprint(ctx, fp)
		if err == nil && !exist {
			_ = h.store.CreateAlert(ctx, &Alert{
				ProjectSpaceID: psID, Source: "patrol", Severity: sev,
				Title: title, Description: comp.Detail,
			})
			created++
		}
	}
	httpx.OK(c, gin.H{
		"overall":           OverallHealth(comps),
		"components":        comps,
		"alerts_created":    created,
		"alerts_suppressed": len(comps) - countHealthy(comps) - created,
	})
}

func countHealthy(comps []ComponentHealth) int {
	n := 0
	for _, c := range comps {
		if c.Status == "healthy" {
			n++
		}
	}
	return n
}

// ---------------- 告警接口 ----------------

// ListAlerts 列出告警（可按 severity/status 过滤）。
//
// @Summary      告警列表
// @Tags         ops
// @Produce      json
// @Param        id        path   string  true  "项目空间ID"
// @Param        severity  query  string  false "严重度过滤(critical/warning/info)"
// @Param        status    query  string  false "状态过滤(firing/resolved)"
// @Success      200  {object}  map[string]interface{}  "告警列表"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/ops/alerts [get]
func (h *Handler) ListAlerts(c *gin.Context) {
	list, err := h.store.ListAlerts(c.Request.Context(), c.Param("id"), c.Query("severity"), c.Query("status"))
	if err != nil {
		httpx.Err(c, 500, 50070, err.Error())
		return
	}
	httpx.OK(c, list)
}

type alertBody struct {
	Source      string `json:"source"`
	Severity    string `json:"severity" binding:"required"`
	Title       string `json:"title" binding:"required"`
	Description string `json:"description"`
}

// CreateAlert 新建告警。
//
// @Summary      新建告警
// @Tags         ops
// @Accept       json
// @Produce      json
// @Param        id    path  alertBody  true  "项目空间ID"
// @Param        body  body  alertBody  true  "告警{source,severity,title,description}"
// @Success      200  {object}  map[string]interface{}  "创建的告警"
// @Failure      400  {object}  map[string]interface{}  "invalid body"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/ops/alerts [post]
func (h *Handler) CreateAlert(c *gin.Context) {
	var in alertBody
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	if in.Source == "" {
		in.Source = "custom"
	}
	a := &Alert{
		ProjectSpaceID: c.Param("id"), Source: in.Source, Severity: in.Severity,
		Title: in.Title, Description: in.Description, Status: "firing",
	}
	if err := h.store.CreateAlert(c.Request.Context(), a); err != nil {
		httpx.Err(c, 500, 50070, err.Error())
		return
	}
	httpx.Created(c, a)
}

// ResolveAlert 解决（关闭）某条告警。
//
// @Summary      解决告警
// @Tags         ops
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Param        aid  path  string  true  "告警ID"
// @Success      200  {object}  map[string]interface{}  "解决结果(resolved)"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/ops/alerts/{aid}/resolve [post]
func (h *Handler) ResolveAlert(c *gin.Context) {
	if err := h.store.ResolveAlert(c.Request.Context(), c.Param("id"), c.Param("aid")); err != nil {
		httpx.Err(c, 500, 50070, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": c.Param("aid"), "status": "resolved"})
}

// ---------------- SOP 接口 ----------------

// ListSOPs 列出 SOP 预案（可按 status 过滤）。
//
// @Summary      SOP 预案列表
// @Tags         ops
// @Produce      json
// @Param        id      path   string  true  "项目空间ID"
// @Param        status  query  string  false "状态过滤(draft/active/archived)"
// @Success      200  {object}  map[string]interface{}  "SOP 列表"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/ops/sops [get]
func (h *Handler) ListSOPs(c *gin.Context) {
	list, err := h.store.ListSOPs(c.Request.Context(), c.Param("id"), c.Query("status"))
	if err != nil {
		httpx.Err(c, 500, 50070, err.Error())
		return
	}
	httpx.OK(c, list)
}

type sopBody struct {
	Code             string `json:"code" binding:"required"`
	Name             string `json:"name" binding:"required"`
	Description      string `json:"description"`
	Category         string `json:"category"`
	RiskLevel        string `json:"risk_level"`
	Steps            string `json:"steps"`
	Rollback         string `json:"rollback"`
	RequiresApproval bool   `json:"requires_approval"`
	Status           string `json:"status"`
}

func (b *sopBody) defaults() {
	if b.Category == "" {
		b.Category = "restart"
	}
	if b.RiskLevel == "" {
		b.RiskLevel = "low"
	}
	if b.Status == "" {
		b.Status = "draft"
	}
}

// CreateSOP 新建 SOP 预案。
//
// @Summary      新建 SOP 预案
// @Tags         ops
// @Accept       json
// @Produce      json
// @Param        id    path  sopBody  true  "项目空间ID"
// @Param        body  body  sopBody  true  "SOP{code,name,steps,rollback,...}"
// @Success      200  {object}  map[string]interface{}  "创建的 SOP"
// @Failure      400  {object}  map[string]interface{}  "invalid body"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/ops/sops [post]
func (h *Handler) CreateSOP(c *gin.Context) {
	var in sopBody
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	in.defaults()
	sop := &SOP{
		ProjectSpaceID: c.Param("id"), Code: in.Code, Name: in.Name, Description: in.Description,
		Category: in.Category, RiskLevel: in.RiskLevel, Steps: in.Steps, Rollback: in.Rollback,
		RequiresApproval: in.RequiresApproval, Status: in.Status,
	}
	if err := h.store.CreateSOP(c.Request.Context(), sop); err != nil {
		httpx.Err(c, 500, 50070, err.Error())
		return
	}
	httpx.Created(c, sop)
}

// UpdateSOP 更新 SOP 预案。
//
// @Summary      更新 SOP 预案
// @Tags         ops
// @Accept       json
// @Produce      json
// @Param        id    path  sopBody  true  "项目空间ID"
// @Param        sid   path  string   true  "SOP ID"
// @Param        body  body  sopBody  true  "SOP{code,name,steps,rollback,...}"
// @Success      200  {object}  map[string]interface{}  "更新后的 SOP"
// @Failure      400  {object}  map[string]interface{}  "invalid body"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/ops/sops/{sid} [put]
func (h *Handler) UpdateSOP(c *gin.Context) {
	var in sopBody
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	in.defaults()
	sop := &SOP{
		ID: c.Param("sid"), ProjectSpaceID: c.Param("id"), Code: in.Code, Name: in.Name,
		Description: in.Description, Category: in.Category, RiskLevel: in.RiskLevel,
		Steps: in.Steps, Rollback: in.Rollback, RequiresApproval: in.RequiresApproval, Status: in.Status,
	}
	if err := h.store.UpdateSOP(c.Request.Context(), sop); err != nil {
		httpx.Err(c, 500, 50070, err.Error())
		return
	}
	httpx.OK(c, sop)
}

// DeleteSOP 删除 SOP 预案。
//
// @Summary      删除 SOP 预案
// @Tags         ops
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Param        sid  path  string  true  "SOP ID"
// @Success      200  {object}  map[string]interface{}  "删除结果"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/ops/sops/{sid} [delete]
func (h *Handler) DeleteSOP(c *gin.Context) {
	if err := h.store.DeleteSOP(c.Request.Context(), c.Param("id"), c.Param("sid")); err != nil {
		httpx.Err(c, 500, 50070, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": c.Param("sid"), "deleted": true})
}

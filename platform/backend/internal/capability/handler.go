package capability

import (
	"errors"
	"strings"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/httpx"
)

// Handler 能力市场 HTTP 接口。
type Handler struct {
	store   *Store
	gateway *Gateway
}

// NewHandler 构造。
func NewHandler(store *Store, gateway *Gateway) *Handler {
	return &Handler{store: store, gateway: gateway}
}

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	// 公共目录（浏览，无需 APIKey）
	r.GET("/capabilities/skills", h.Catalog)
	r.GET("/capabilities/skills/:sid", h.SkillDetail)

	// 统一调用入口（APIKey 鉴权）
	r.POST("/capabilities/invoke", h.RequireAPIKey(), h.Invoke)

	// 技能管理
	r.GET("/project-spaces/:id/capabilities/skills", h.ListSkills)
	r.POST("/project-spaces/:id/capabilities/skills", h.CreateSkill)
	r.PUT("/project-spaces/:id/capabilities/skills/:sid", h.UpdateSkill)
	r.DELETE("/project-spaces/:id/capabilities/skills/:sid", h.DeleteSkill)
	r.POST("/project-spaces/:id/capabilities/skills/:sid/submit", h.SubmitSkill)
	r.POST("/project-spaces/:id/capabilities/skills/:sid/approve", h.ApproveSkill)
	r.POST("/project-spaces/:id/capabilities/skills/:sid/offline", h.OfflineSkill)

	// APIKey 管理
	r.GET("/project-spaces/:id/capabilities/api-keys", h.ListAPIKeys)
	r.POST("/project-spaces/:id/capabilities/api-keys", h.CreateAPIKey)
	r.POST("/project-spaces/:id/capabilities/api-keys/:kid/revoke", h.RevokeAPIKey)

	// 用量看板
	r.GET("/project-spaces/:id/capabilities/usage", h.UsageList)
	r.GET("/project-spaces/:id/capabilities/usage/by-skill", h.UsageBySkill)

	// 领域 Agent
	r.GET("/project-spaces/:id/capabilities/domain-agents", h.ListDomainAgents)
	r.POST("/project-spaces/:id/capabilities/domain-agents", h.CreateDomainAgent)
	r.DELETE("/project-spaces/:id/capabilities/domain-agents/:did", h.DeleteDomainAgent)
}

// ---------------- APIKey 中间件 ----------------

const CtxAPIKey = "api_key"

// RequireAPIKey 从 Authorization: Bearer 或 X-Api-Key 取 key 校验。
func (h *Handler) RequireAPIKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		plain := c.GetHeader("X-Api-Key")
		if plain == "" {
			if auth := c.GetHeader("Authorization"); strings.HasPrefix(auth, "Bearer ") {
				plain = strings.TrimPrefix(auth, "Bearer ")
			}
		}
		if plain == "" {
			httpx.Err(c, 401, 40109, "缺少 APIKey（Authorization: Bearer sk_anp_xxx 或 X-Api-Key）")
			c.Abort()
			return
		}
		k, err := h.store.LookupAPIKey(c.Request.Context(), plain)
		if err != nil || k == nil {
			httpx.Err(c, 401, 40109, "APIKey 无效或已失效")
			c.Abort()
			return
		}
		c.Set(CtxAPIKey, k)
		c.Set("project_space_id", k.ProjectSpaceID)
		c.Next()
	}
}

// ---------------- 调用 ----------------

type invokeBody struct {
	SkillCode  string `json:"skill_code" binding:"required"`
	Input      string `json:"input" binding:"required"`
	RenderHint string `json:"render_hint"` // card/form/chart/text
}

// Invoke 统一技能调用入口：网关鉴权 → 校验技能 → 调模型 → 记用量。
func (h *Handler) Invoke(c *gin.Context) {
	var in invokeBody
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	key, _ := c.Get(CtxAPIKey)
	apiKey, _ := key.(*APIKey)
	// 重新取出明文 key（中间件持有哈希版；这里从请求头再取一次给 Lookup）
	plain := c.GetHeader("X-Api-Key")
	if plain == "" {
		plain = strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	}
	res, err := h.gateway.Invoke(c.Request.Context(), plain, in.SkillCode, in.Input, in.RenderHint, c.GetString("trace_id"))
	if err != nil {
		switch {
		case errors.Is(err, ErrAuth):
			httpx.Err(c, 401, 40109, err.Error())
		case errors.Is(err, ErrSkillUnavailable):
			httpx.Err(c, 404, 40409, err.Error())
		case errors.Is(err, ErrNotAllowed):
			httpx.Err(c, 403, 40309, err.Error())
		default:
			httpx.Err(c, 502, 50209, err.Error())
		}
		return
	}
	httpx.OK(c, gin.H{
		"result": res, "api_key_id": apiKey.ID, "caller_app": apiKey.AppName,
	})
}

// ---------------- 公共目录 ----------------

func (h *Handler) Catalog(c *gin.Context) {
	list, err := h.store.ListSkills(c.Request.Context(), "", "", true)
	if err != nil {
		httpx.Err(c, 500, 50090, err.Error())
		return
	}
	httpx.OK(c, list)
}

func (h *Handler) SkillDetail(c *gin.Context) {
	sk, err := h.store.GetSkill(c.Request.Context(), c.Param("sid"))
	if err != nil {
		httpx.Err(c, 404, 40409, "技能不存在")
		return
	}
	httpx.OK(c, sk)
}

// ---------------- 技能管理 ----------------

func (h *Handler) ListSkills(c *gin.Context) {
	list, err := h.store.ListSkills(c.Request.Context(), c.Param("id"), c.Query("status"), false)
	if err != nil {
		httpx.Err(c, 500, 50090, err.Error())
		return
	}
	httpx.OK(c, list)
}

type skillBody struct {
	Code            string `json:"code" binding:"required"`
	Name            string `json:"name" binding:"required"`
	Description     string `json:"description"`
	Category        string `json:"category"`
	PromptTemplate  string `json:"prompt_template"`
	Version         string `json:"version"`
	RiskLevel       string `json:"risk_level"`
	IsPublic        bool   `json:"is_public"`
	DataAccessScope string `json:"data_access_scope"`
}

func (b *skillBody) defaults() {
	if b.Category == "" {
		b.Category = "assistant"
	}
	if b.RiskLevel == "" {
		b.RiskLevel = "low"
	}
}

func (h *Handler) CreateSkill(c *gin.Context) {
	var in skillBody
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	in.defaults()
	sk := &Skill{
		ProjectSpaceID: c.Param("id"), Code: in.Code, Name: in.Name, Description: in.Description,
		Category: in.Category, PromptTemplate: in.PromptTemplate, Version: in.Version, RiskLevel: in.RiskLevel,
		IsPublic: in.IsPublic, DataAccessScope: in.DataAccessScope,
	}
	if err := h.store.CreateSkill(c.Request.Context(), sk); err != nil {
		httpx.Err(c, 500, 50090, err.Error())
		return
	}
	httpx.Created(c, sk)
}

func (h *Handler) UpdateSkill(c *gin.Context) {
	var in skillBody
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	in.defaults()
	sk := &Skill{
		ID: c.Param("sid"), Code: in.Code, Name: in.Name, Description: in.Description,
		Category: in.Category, PromptTemplate: in.PromptTemplate, Version: in.Version, RiskLevel: in.RiskLevel,
		IsPublic: in.IsPublic, DataAccessScope: in.DataAccessScope,
	}
	if err := h.store.UpdateSkill(c.Request.Context(), sk); err != nil {
		httpx.Err(c, 500, 50090, err.Error())
		return
	}
	httpx.OK(c, sk)
}

func (h *Handler) DeleteSkill(c *gin.Context) {
	if err := h.store.DeleteSkill(c.Request.Context(), c.Param("sid")); err != nil {
		httpx.Err(c, 500, 50090, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": c.Param("sid"), "deleted": true})
}

// SubmitSkill 提交上架评审（draft → pending_review）。
func (h *Handler) SubmitSkill(c *gin.Context) {
	if err := h.store.SetSkillStatus(c.Request.Context(), c.Param("sid"), "pending_review"); err != nil {
		httpx.Err(c, 500, 50090, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": c.Param("sid"), "status": "pending_review"})
}

// ApproveSkill 审批通过上架（→ active）。
func (h *Handler) ApproveSkill(c *gin.Context) {
	if err := h.store.SetSkillStatus(c.Request.Context(), c.Param("sid"), "active"); err != nil {
		httpx.Err(c, 500, 50090, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": c.Param("sid"), "status": "active"})
}

// OfflineSkill 下线/紧急熔断（→ offline，网关即时拦截）。
func (h *Handler) OfflineSkill(c *gin.Context) {
	if err := h.store.SetSkillStatus(c.Request.Context(), c.Param("sid"), "offline"); err != nil {
		httpx.Err(c, 500, 50090, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": c.Param("sid"), "status": "offline"})
}

// ---------------- APIKey 管理 ----------------

func (h *Handler) ListAPIKeys(c *gin.Context) {
	list, err := h.store.ListAPIKeys(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50090, err.Error())
		return
	}
	httpx.OK(c, list)
}

type apiKeyBody struct {
	AppName       string `json:"app_name" binding:"required"`
	AllowedSkills string `json:"allowed_skills"`
	Scope         string `json:"scope"`
}

func (h *Handler) CreateAPIKey(c *gin.Context) {
	var in apiKeyBody
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	k := &APIKey{ProjectSpaceID: c.Param("id"), AppName: in.AppName, AllowedSkills: in.AllowedSkills, Scope: in.Scope}
	plain, err := h.store.CreateAPIKey(c.Request.Context(), k)
	if err != nil {
		httpx.Err(c, 500, 50090, err.Error())
		return
	}
	httpx.Created(c, gin.H{"api_key": k, "secret": plain, "note": "明文仅此一次返回，请妥善保存"})
}

func (h *Handler) RevokeAPIKey(c *gin.Context) {
	if err := h.store.RevokeAPIKey(c.Request.Context(), c.Param("id"), c.Param("kid")); err != nil {
		httpx.Err(c, 500, 50090, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": c.Param("kid"), "status": "revoked"})
}

// ---------------- 用量 ----------------

func (h *Handler) UsageList(c *gin.Context) {
	list, err := h.store.UsageList(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50090, err.Error())
		return
	}
	httpx.OK(c, list)
}

func (h *Handler) UsageBySkill(c *gin.Context) {
	list, err := h.store.UsageBySkill(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50090, err.Error())
		return
	}
	httpx.OK(c, list)
}

// ---------------- 领域 Agent ----------------

func (h *Handler) ListDomainAgents(c *gin.Context) {
	list, err := h.store.ListDomainAgents(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50090, err.Error())
		return
	}
	httpx.OK(c, list)
}

type domainAgentBody struct {
	Code           string `json:"code" binding:"required"`
	Name           string `json:"name" binding:"required"`
	Domain         string `json:"domain"`
	ComposedSkills string `json:"composed_skills"`
	Status         string `json:"status"`
}

func (h *Handler) CreateDomainAgent(c *gin.Context) {
	var in domainAgentBody
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	if in.Domain == "" {
		in.Domain = "custom"
	}
	d := &DomainAgent{
		ProjectSpaceID: c.Param("id"), Code: in.Code, Name: in.Name, Domain: in.Domain,
		ComposedSkills: in.ComposedSkills, Status: in.Status,
	}
	if err := h.store.CreateDomainAgent(c.Request.Context(), d); err != nil {
		httpx.Err(c, 500, 50090, err.Error())
		return
	}
	httpx.Created(c, d)
}

func (h *Handler) DeleteDomainAgent(c *gin.Context) {
	if err := h.store.DeleteDomainAgent(c.Request.Context(), c.Param("id"), c.Param("did")); err != nil {
		httpx.Err(c, 500, 50090, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": c.Param("did"), "deleted": true})
}

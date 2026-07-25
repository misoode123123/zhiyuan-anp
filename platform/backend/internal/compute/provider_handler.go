package compute

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"zhiyuan-anp/platform/backend/internal/httpx"
)

// ProviderHandler 供应商/模型管理 HTTP 接口。
type ProviderHandler struct {
	store    *Store
	validate *validator.Validate
}

// NewProviderHandler 构造。
func NewProviderHandler(store *Store, v *validator.Validate) *ProviderHandler {
	return &ProviderHandler{store: store, validate: v}
}

// RegisterProvider 注册供应商/模型路由。
func (h *ProviderHandler) RegisterProvider(r gin.IRouter) {
	r.GET("/compute/providers", h.ListProviders)
	r.POST("/compute/providers", h.CreateProvider)
	r.PUT("/compute/providers/:id", h.UpdateProvider)
	r.DELETE("/compute/providers/:id", h.DeleteProvider)

	r.GET("/compute/models", h.ListModels)
	r.POST("/compute/models", h.CreateModel)
	r.PUT("/compute/models/:id", h.UpdateModel)
	r.DELETE("/compute/models/:id", h.DeleteModel)

	// 路由策略
	r.GET("/compute/routes", h.ListRoutes)
	r.PUT("/compute/routes/:task_type", h.UpsertRoute)

	// 统一网关
	r.POST("/compute/chat", h.Chat)

	// 动态 opencode.json
	r.GET("/compute/opencode-config", h.GetOpenCodeConfig)
}

// --- Provider ---

func (h *ProviderHandler) ListProviders(c *gin.Context) {
	list, err := h.store.ListProviders(c.Request.Context())
	if err != nil {
		httpx.Err(c, 500, 50010, err.Error())
		return
	}
	httpx.OK(c, list)
}

type createProviderReq struct {
	Name        string `json:"name" binding:"required"`
	Type        string `json:"type"`
	BaseURL     string `json:"base_url" binding:"required"`
	APIKey      string `json:"api_key"`
	Enabled     *bool  `json:"enabled"`
	Config      string `json:"config"`
	Description string `json:"description"`
}

func (h *ProviderHandler) CreateProvider(c *gin.Context) {
	var in createProviderReq
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, err.Error())
		return
	}
	p := &Provider{
		Name: in.Name, Type: def(in.Type, "api"), BaseURL: in.BaseURL,
		APIKey: in.APIKey, Enabled: in.Enabled == nil || *in.Enabled,
		Description: in.Description,
	}
	if in.Config != "" {
		p.Config = &in.Config
	}
	if err := h.store.CreateProvider(c.Request.Context(), p); err != nil {
		httpx.Err(c, 500, 50010, err.Error())
		return
	}
	httpx.Created(c, p)
	h.refreshOpenCodeConfig(c)
}

type updateProviderReq struct {
	Name        string `json:"name" binding:"required"`
	Type        string `json:"type"`
	BaseURL     string `json:"base_url" binding:"required"`
	APIKey      string `json:"api_key"`
	Enabled     *bool  `json:"enabled"`
	Config      string `json:"config"`
	Description string `json:"description"`
}

func (h *ProviderHandler) UpdateProvider(c *gin.Context) {
	var in updateProviderReq
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, err.Error())
		return
	}
	p := &Provider{
		ID: c.Param("id"), Name: in.Name, Type: def(in.Type, "api"), BaseURL: in.BaseURL,
		APIKey: in.APIKey, Enabled: in.Enabled != nil && *in.Enabled,
		Description: in.Description,
	}
	if in.Config != "" {
		p.Config = &in.Config
	}
	if err := h.store.UpdateProvider(c.Request.Context(), p); err != nil {
		httpx.Err(c, 500, 50010, err.Error())
		return
	}
	httpx.OK(c, p)
}

func (h *ProviderHandler) DeleteProvider(c *gin.Context) {
	if err := h.store.DeleteProvider(c.Request.Context(), c.Param("id")); err != nil {
		httpx.Err(c, 500, 50010, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": c.Param("id"), "deleted": true})
	h.refreshOpenCodeConfig(c)
}

// --- Model ---

func (h *ProviderHandler) ListModels(c *gin.Context) {
	list, err := h.store.ListModels(c.Request.Context(), c.Query("provider_id"))
	if err != nil {
		httpx.Err(c, 500, 50010, err.Error())
		return
	}
	httpx.OK(c, list)
}

type createModelReq struct {
	ProviderID    string  `json:"provider_id" binding:"required"`
	Name          string  `json:"name" binding:"required"`
	DisplayName   string  `json:"display_name"`
	Modality      string  `json:"modality"`
	ContextWindow int     `json:"context_window"`
	MaxOutput     int     `json:"max_output"`
	CostInput     float64 `json:"cost_input"`
	CostOutput    float64 `json:"cost_output"`
	Capabilities  string  `json:"capabilities"`
}

func (h *ProviderHandler) CreateModel(c *gin.Context) {
	var in createModelReq
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, err.Error())
		return
	}
	m := &Model{
		ProviderID: in.ProviderID, Name: in.Name, DisplayName: in.DisplayName,
		Modality: def(in.Modality, "text"), ContextWindow: in.ContextWindow,
		MaxOutput: in.MaxOutput, CostInput: in.CostInput, CostOutput: in.CostOutput,
		Enabled: true,
	}
	if in.Capabilities != "" {
		m.Capabilities = &in.Capabilities
	}
	if err := h.store.CreateModel(c.Request.Context(), m); err != nil {
		httpx.Err(c, 500, 50010, err.Error())
		return
	}
	httpx.Created(c, m)
	h.refreshOpenCodeConfig(c)
}

type updateModelReq struct {
	Name          string  `json:"name" binding:"required"`
	DisplayName   string  `json:"display_name"`
	Modality      string  `json:"modality"`
	ContextWindow int     `json:"context_window"`
	MaxOutput     int     `json:"max_output"`
	CostInput     float64 `json:"cost_input"`
	CostOutput    float64 `json:"cost_output"`
	Capabilities  string  `json:"capabilities"`
	Enabled       *bool   `json:"enabled"`
}

func (h *ProviderHandler) UpdateModel(c *gin.Context) {
	var in updateModelReq
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, err.Error())
		return
	}
	m := &Model{
		ID: c.Param("id"), Name: in.Name, DisplayName: in.DisplayName,
		Modality: def(in.Modality, "text"), ContextWindow: in.ContextWindow,
		MaxOutput: in.MaxOutput, CostInput: in.CostInput, CostOutput: in.CostOutput,
		Enabled: in.Enabled == nil || *in.Enabled,
	}
	if in.Capabilities != "" {
		m.Capabilities = &in.Capabilities
	}
	if err := h.store.UpdateModel(c.Request.Context(), m); err != nil {
		httpx.Err(c, 500, 50010, err.Error())
		return
	}
	httpx.OK(c, m)
}

func (h *ProviderHandler) DeleteModel(c *gin.Context) {
	if err := h.store.DeleteModel(c.Request.Context(), c.Param("id")); err != nil {
		httpx.Err(c, 500, 50010, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": c.Param("id"), "deleted": true})
	h.refreshOpenCodeConfig(c)
}

// SeedProviders 若 compute_provider 为空，seed 智谱（coding plan）。
func SeedProviders(ctx context.Context, db *sqlx.DB) error {
	var n int
	if err := db.GetContext(ctx, &n, `SELECT COUNT(*) FROM compute_provider`); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	// 智谱 coding plan provider
	var cpvCfg string
	p := &Provider{
		Name: "智谱 GLM (Coding Plan)", Type: "api",
		BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4",
		Enabled: true, Description: "智谱 GLM coding plan，glm-5.1/glm-5-turbo",
	}
	if err := db.QueryRowxContext(ctx,
		`INSERT INTO compute_provider (id, name, type, base_url, api_key, enabled, description)
		 VALUES ($1, $2, $3, $4, '', $5, $6) RETURNING id`,
		"cpv_"+uuid.NewString()[:19], p.Name, p.Type, p.BaseURL, p.Enabled, p.Description).Scan(&cpvCfg); err != nil {
		return err
	}
	// 默认模型
	models := []Model{
		{ProviderID: cpvCfg, Name: "glm-5.1", DisplayName: "GLM-5.1", Modality: "code", ContextWindow: 204800, MaxOutput: 131072, Enabled: true},
		{ProviderID: cpvCfg, Name: "glm-5-turbo", DisplayName: "GLM-5-Turbo", Modality: "text", ContextWindow: 204800, MaxOutput: 131072, Enabled: true},
	}
	for _, m := range models {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO compute_model (id, provider_id, name, display_name, modality, context_window, max_output, enabled)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE)`,
			"cmd_"+uuid.NewString()[:19], m.ProviderID, m.Name, m.DisplayName, m.Modality, m.ContextWindow, m.MaxOutput); err != nil {
			return err
		}
	}
	return nil
}

func def(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// --- Route ---

func (h *ProviderHandler) ListRoutes(c *gin.Context) {
	list, err := h.store.ListRoutes(c.Request.Context())
	if err != nil {
		httpx.Err(c, 500, 50010, err.Error())
		return
	}
	httpx.OK(c, list)
}

type upsertRouteReq struct {
	PrimaryModelID  string  `json:"primary_model_id" binding:"required"`
	FallbackModelID *string `json:"fallback_model_id"`
	Priority        int     `json:"priority"`
	Enabled         *bool   `json:"enabled"`
}

func (h *ProviderHandler) UpsertRoute(c *gin.Context) {
	var in upsertRouteReq
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, err.Error())
		return
	}
	r := &Route{
		TaskType: c.Param("task_type"), PrimaryModelID: in.PrimaryModelID,
		FallbackModelID: in.FallbackModelID, Priority: in.Priority,
		Enabled: in.Enabled == nil || *in.Enabled,
	}
	if err := h.store.UpsertRoute(c.Request.Context(), r); err != nil {
		httpx.Err(c, 500, 50010, err.Error())
		return
	}
	httpx.OK(c, r)
}

// --- 统一网关 Chat ---

var gateway *Gateway

// SetGateway 注入网关实例（main 装配时调）。
func SetGateway(gw *Gateway) { gateway = gw }

func (h *ProviderHandler) Chat(c *gin.Context) {
	var req ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Err(c, 400, 40001, err.Error())
		return
	}
	req.ProjectSpaceID = c.GetString("project_space_id")
	if gateway == nil {
		httpx.Err(c, 500, 50010, "网关未初始化")
		return
	}
	resp, err := gateway.Chat(c.Request.Context(), req)
	if err != nil {
		httpx.Err(c, 500, 50002, err.Error())
		return
	}
	httpx.OK(c, resp)
}

// --- 动态 opencode.json ---

var opencodeConfigPath string

// SetOpenCodeConfigPath 设置 opencode.json 写入路径（main 装配时调）。
func SetOpenCodeConfigPath(path string) { opencodeConfigPath = path }

// GetOpenCodeConfig 预览生成的 opencode.json（GET /compute/opencode-config）。
func (h *ProviderHandler) GetOpenCodeConfig(c *gin.Context) {
	cfg, err := h.store.GenerateOpenCodeConfig(c.Request.Context())
	if err != nil {
		httpx.Err(c, 500, 50010, err.Error())
		return
	}
	httpx.OK(c, cfg)
}

// refreshOpenCodeConfig 在 provider/model 变更后重新生成 opencode.json。
func (h *ProviderHandler) refreshOpenCodeConfig(c *gin.Context) {
	if opencodeConfigPath != "" {
		_ = h.store.WriteOpenCodeConfig(c.Request.Context(), opencodeConfigPath)
	}
}

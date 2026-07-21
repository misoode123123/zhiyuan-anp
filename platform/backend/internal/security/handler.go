package security

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/httpx"
)

// Handler 安全与合规中心 HTTP 接口。
type Handler struct {
	store *Store
}

// NewHandler 构造。
func NewHandler(store *Store) *Handler { return &Handler{store: store} }

// Register 模块级装配:main 调用,内部 new handler + 注册路由(减少 main.go 集中 new)。
func Register(r gin.IRouter, store *Store) {
	NewHandler(store).Register(r)
}

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	r.POST("/project-spaces/:id/security/scans", h.Scan)
	r.GET("/project-spaces/:id/security/findings", h.ListFindings)
	r.POST("/project-spaces/:id/security/findings/:fid/suppress", h.Suppress)
	r.GET("/project-spaces/:id/security/gate", h.Gate)

	r.GET("/project-spaces/:id/security/data-classifications", h.ListDC)
	r.POST("/project-spaces/:id/security/data-classifications", h.CreateDC)
	r.DELETE("/project-spaces/:id/security/data-classifications/:did", h.DeleteDC)

	r.GET("/project-spaces/:id/security/audit-logs", h.ListAudit)
}

type scanBody struct {
	Content  string `json:"content" binding:"required"`
	ScanType string `json:"scan_type"` // secret/sast/prompt/full（空=full）
}

// Scan 触发安全扫描：Go 原生正则引擎 → 落库 → 返回结果与发现。
//
// @Summary      触发安全扫描
// @Tags         security
// @Accept       json
// @Produce      json
// @Param        id    path  scanBody  true  "项目空间ID"
// @Param        body  body  scanBody  true  "扫描请求{content,scan_type}"
// @Success      200  {object}  map[string]interface{}  "扫描结果与发现{scan,findings}"
// @Failure      400  {object}  map[string]interface{}  "invalid body"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/security/scans [post]
func (h *Handler) Scan(c *gin.Context) {
	var in scanBody
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	psID := c.Param("id")
	res, findings, err := h.store.RunScan(c.Request.Context(), psID, in.Content, in.ScanType)
	if err != nil {
		httpx.Err(c, 500, 50050, err.Error())
		return
	}
	// 审计：记录本次扫描与放行/阻断决策
	decision := "allow"
	if res.CriticalCount > 0 || res.HighCount > 0 {
		decision = "deny"
	}
	_ = h.store.AppendAudit(c.Request.Context(), &AuditLog{
		ProjectSpaceID: psID, ActorType: "system", ActorID: c.GetString("user_id"),
		Action: "scan", ResourceType: "code",
		Detail:         res.ScanType + " 扫描: " + strconv.Itoa(res.TotalFindings) + " 发现，风险 " + res.RiskLevel,
		PolicyDecision: decision,
	})
	httpx.Created(c, gin.H{"scan": res, "findings": findings})
}

// ListFindings 列出安全发现（可按 severity/status 过滤）。
//
// @Summary      安全发现列表
// @Tags         security
// @Produce      json
// @Param        id        path   string  true  "项目空间ID"
// @Param        severity  query  string  false "严重度过滤(critical/high/medium/low)"
// @Param        status    query  string  false "状态过滤(open/suppressed)"
// @Success      200  {object}  map[string]interface{}  "安全发现列表"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/security/findings [get]
func (h *Handler) ListFindings(c *gin.Context) {
	list, err := h.store.ListFindings(c.Request.Context(), c.Param("id"), c.Query("severity"), c.Query("status"))
	if err != nil {
		httpx.Err(c, 500, 50050, err.Error())
		return
	}
	httpx.OK(c, list)
}

// Suppress 抑制（忽略）某条安全发现。
//
// @Summary      抑制安全发现
// @Tags         security
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Param        fid  path  string  true  "发现ID"
// @Success      200  {object}  map[string]interface{}  "抑制结果(suppressed)"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/security/findings/{fid}/suppress [post]
func (h *Handler) Suppress(c *gin.Context) {
	psID, fid := c.Param("id"), c.Param("fid")
	if err := h.store.SuppressFinding(c.Request.Context(), psID, fid); err != nil {
		httpx.Err(c, 500, 50050, err.Error())
		return
	}
	_ = h.store.AppendAudit(c.Request.Context(), &AuditLog{
		ProjectSpaceID: psID, ActorType: "human", ActorID: c.GetString("user_id"),
		Action: "suppress", ResourceType: "finding", Detail: "抑制发现 " + fid,
	})
	httpx.OK(c, gin.H{"id": fid, "status": "suppressed"})
}

// Gate 安全闸门：按项目空间汇总风险并给出放行/阻断决策。
//
// @Summary      安全闸门
// @Tags         security
// @Produce      json
// @Param        id  path  string  true  "项目空间ID"
// @Success      200  {object}  map[string]interface{}  "闸门状态(风险汇总+决策)"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/security/gate [get]
func (h *Handler) Gate(c *gin.Context) {
	g, err := h.store.Gate(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50050, err.Error())
		return
	}
	httpx.OK(c, g)
}

// ---------------- 数据分级 ----------------

// ListDC 列出数据分级配置。
//
// @Summary      数据分级列表
// @Tags         security
// @Produce      json
// @Param        id  path  string  true  "项目空间ID"
// @Success      200  {object}  map[string]interface{}  "数据分级列表"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/security/data-classifications [get]
func (h *Handler) ListDC(c *gin.Context) {
	list, err := h.store.ListDC(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50050, err.Error())
		return
	}
	httpx.OK(c, list)
}

type dcBody struct {
	FieldName        string `json:"field_name" binding:"required"`
	TableRef         string `json:"table_ref" binding:"required"`
	SensitivityLevel string `json:"sensitivity_level"`
	DataType         string `json:"data_type"`
	MaskingStrategy  string `json:"masking_strategy"`
}

func (b *dcBody) defaults() {
	if b.SensitivityLevel == "" {
		b.SensitivityLevel = "internal"
	}
	if b.DataType == "" {
		b.DataType = "pii"
	}
	if b.MaskingStrategy == "" {
		b.MaskingStrategy = "mask"
	}
}

// CreateDC 新增数据分级配置。
//
// @Summary      新增数据分级
// @Tags         security
// @Accept       json
// @Produce      json
// @Param        id    path  dcBody  true  "项目空间ID"
// @Param        body  body  dcBody  true  "数据分级{field_name,table_ref,...}"
// @Success      200  {object}  map[string]interface{}  "创建的数据分级"
// @Failure      400  {object}  map[string]interface{}  "invalid body"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/security/data-classifications [post]
func (h *Handler) CreateDC(c *gin.Context) {
	var in dcBody
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	in.defaults()
	dc := &DataClassification{
		ProjectSpaceID: c.Param("id"), FieldName: in.FieldName, TableRef: in.TableRef,
		SensitivityLevel: in.SensitivityLevel, DataType: in.DataType, MaskingStrategy: in.MaskingStrategy,
	}
	if err := h.store.CreateDC(c.Request.Context(), dc); err != nil {
		httpx.Err(c, 500, 50050, err.Error())
		return
	}
	httpx.Created(c, dc)
}

// DeleteDC 删除数据分级配置。
//
// @Summary      删除数据分级
// @Tags         security
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Param        did  path  string  true  "数据分级ID"
// @Success      200  {object}  map[string]interface{}  "删除结果"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/security/data-classifications/{did} [delete]
func (h *Handler) DeleteDC(c *gin.Context) {
	if err := h.store.DeleteDC(c.Request.Context(), c.Param("id"), c.Param("did")); err != nil {
		httpx.Err(c, 500, 50050, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": c.Param("did"), "deleted": true})
}

// ---------------- 审计 ----------------

// ListAudit 列出安全审计日志。
//
// @Summary      安全审计日志
// @Tags         security
// @Produce      json
// @Param        id  path  string  true  "项目空间ID"
// @Success      200  {object}  map[string]interface{}  "审计日志列表"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/security/audit-logs [get]
func (h *Handler) ListAudit(c *gin.Context) {
	list, err := h.store.ListAudit(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50050, err.Error())
		return
	}
	httpx.OK(c, list)
}

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

func (h *Handler) ListFindings(c *gin.Context) {
	list, err := h.store.ListFindings(c.Request.Context(), c.Param("id"), c.Query("severity"), c.Query("status"))
	if err != nil {
		httpx.Err(c, 500, 50050, err.Error())
		return
	}
	httpx.OK(c, list)
}

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

func (h *Handler) Gate(c *gin.Context) {
	g, err := h.store.Gate(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50050, err.Error())
		return
	}
	httpx.OK(c, g)
}

// ---------------- 数据分级 ----------------

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

func (h *Handler) DeleteDC(c *gin.Context) {
	if err := h.store.DeleteDC(c.Request.Context(), c.Param("id"), c.Param("did")); err != nil {
		httpx.Err(c, 500, 50050, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": c.Param("did"), "deleted": true})
}

// ---------------- 审计 ----------------

func (h *Handler) ListAudit(c *gin.Context) {
	list, err := h.store.ListAudit(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50050, err.Error())
		return
	}
	httpx.OK(c, list)
}

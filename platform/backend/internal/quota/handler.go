package quota

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"zhiyuan-anp/platform/backend/internal/httpx"
)

// Handler 配额管理 HTTP 接口。
type Handler struct {
	svc      *Service
	validate *validator.Validate
}

// NewHandler 构造。
func NewHandler(svc *Service, v *validator.Validate) *Handler {
	return &Handler{svc: svc, validate: v}
}

// Register 模块级装配：main 调用，内部 new handler + 注册路由。
func Register(r gin.IRouter, svc *Service, v *validator.Validate) {
	NewHandler(svc, v).Register(r)
}

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	r.GET("/project-spaces/:id/quota", h.GetQuota)
	r.PUT("/project-spaces/:id/quota", h.UpdateQuota)
}

// GetQuota 取配额 + 当前用量（管理 UI / 看板用）。
//
// @Summary      项目配额 + 当前用量
// @Tags         quota
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Success      200  {object}  map[string]interface{}  "配额 + 4 维度用量"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/quota [get]
func (h *Handler) GetQuota(c *gin.Context) {
	u, err := h.svc.Usage(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50060, err.Error())
		return
	}
	httpx.OK(c, u)
}

// updateBody 配额更新入参。4 个 max_* 都可选（min=0 允许设为 0 拦截全部）；
// 未传字段保持原值（用 *int 区分「未传」与「传 0」）。
type updateBody struct {
	MaxApps                  *int `json:"max_apps" validate:"omitempty,min=0,max=10000"`
	MaxDatabases             *int `json:"max_databases" validate:"omitempty,min=0,max=10000"`
	MaxTotalDBMb             *int `json:"max_total_db_mb" validate:"omitempty,min=0,max=1048576"` // 上限 1TB
	MaxCapabilityCallsPerDay *int `json:"max_capability_calls_per_day" validate:"omitempty,min=0,max=10000000"`
}

// UpdateQuota 修改配额（admin）。4 字段都可选；未传保留原值。
//
// @Summary      修改项目配额
// @Tags         quota
// @Accept       json
// @Produce      json
// @Param        id    path  string       true  "项目空间ID"
// @Param        body  body  updateBody   true  "4 个 max_*（可选，未传保留原值）"
// @Success      200   {object}  map[string]interface{}  "更新后的配额"
// @Failure      400   {object}  map[string]interface{}  "invalid body / 越界"
// @Failure      500   {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/quota [put]
func (h *Handler) UpdateQuota(c *gin.Context) {
	var in updateBody
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40060, "invalid body: "+err.Error())
		return
	}
	if err := h.validate.Struct(in); err != nil {
		httpx.Err(c, 400, 40061, err.Error())
		return
	}
	psID := c.Param("id")
	// 先取当前（GetOrCreate 兜底），未传字段保留原值
	cur, err := h.svc.store.GetOrCreate(c.Request.Context(), psID)
	if err != nil {
		httpx.Err(c, 500, 50060, err.Error())
		return
	}
	maxApps := cur.MaxApps
	maxDatabases := cur.MaxDatabases
	maxTotalDBMb := cur.MaxTotalDBMb
	maxCap := cur.MaxCapabilityCallsPerDay
	if in.MaxApps != nil {
		maxApps = *in.MaxApps
	}
	if in.MaxDatabases != nil {
		maxDatabases = *in.MaxDatabases
	}
	if in.MaxTotalDBMb != nil {
		maxTotalDBMb = *in.MaxTotalDBMb
	}
	if in.MaxCapabilityCallsPerDay != nil {
		maxCap = *in.MaxCapabilityCallsPerDay
	}
	q, err := h.svc.Set(c.Request.Context(), psID, maxApps, maxDatabases, maxTotalDBMb, maxCap)
	if err != nil {
		if errors.Is(err, ErrNotExists) {
			httpx.Err(c, 404, 40460, "项目空间配额不存在")
			return
		}
		httpx.Err(c, 500, 50060, err.Error())
		return
	}
	httpx.OK(c, q)
}

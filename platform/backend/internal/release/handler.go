package release

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/change"
	"zhiyuan-anp/platform/backend/internal/httpx"
	"zhiyuan-anp/platform/backend/internal/requirement"
)

// Handler 发布中心 HTTP 接口。
type Handler struct {
	store   *Store
	changes *change.Store
	reqRepo *requirement.Repository
}

// NewHandler 构造 Handler。
func NewHandler(store *Store, changes *change.Store, reqRepo *requirement.Repository) *Handler {
	return &Handler{store: store, changes: changes, reqRepo: reqRepo}
}

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	r.POST("/project-spaces/:id/releases", h.Create)
	r.GET("/project-spaces/:id/releases", h.List)
}

type createRequest struct {
	ChangeID string `json:"change_id" binding:"required"`
}

// Create 把已审批变更发布上线（🚪G5 后），版本号自增；
// 并追溯 change.source_id → 标记来源需求为"已交付"（需求生命周期闭环）。
func (h *Handler) Create(c *gin.Context) {
	psID := c.Param("id")
	var in createRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	n, err := h.store.Count(c.Request.Context(), psID)
	if err != nil {
		httpx.Err(c, 500, 50009, err.Error())
		return
	}
	r := &Release{
		ProjectSpaceID: psID,
		ChangeID:       in.ChangeID,
		Version:        fmt.Sprintf("v%d", n+1),
		Status:         "released",
	}
	if err := h.store.Create(c.Request.Context(), r); err != nil {
		httpx.Err(c, 500, 50009, err.Error())
		return
	}
	// 追溯 change → 标记来源需求"已交付"
	if h.changes != nil {
		if chg, err := h.changes.Get(c.Request.Context(), in.ChangeID); err == nil && chg != nil && chg.ID != "" && chg.SourceID != "" {
			if h.reqRepo != nil {
				_ = h.reqRepo.UpdateStatus(c.Request.Context(), chg.SourceID, "delivered")
			}
		}
	}
	httpx.Created(c, r)
}

// List 发布历史。
func (h *Handler) List(c *gin.Context) {
	list, err := h.store.List(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50009, err.Error())
		return
	}
	httpx.OK(c, list)
}
